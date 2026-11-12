package microsoftmail

import (
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mailsignin"

	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/storedtoken"
)

const secretKey = "a secret key made up for this test"

const clientID = "11111111-aaaa-4bbb-8ccc-222222222222"

// idToken is an unsigned id_token holding claims, as Microsoft's token
// endpoint returns one (the signature is not read; mailbox_identity.go).
func idToken(claims map[string]any) string {
	payload, _ := json.Marshal(claims)
	return "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func TestWhichMailboxAGrantIsFor(t *testing.T) {
	for name, test := range map[string]struct {
		claims map[string]any
		want   mailbox
		fails  bool
	}{
		"the email claim, and the tenant and object as the account": {
			claims: map[string]any{"aud": clientID, "email": "ada@example.test", "preferred_username": "a.l@example.test", "tid": "t1", "oid": "o1", "sub": "s1"},
			want:   mailbox{Address: "ada@example.test", Subject: "t1:o1"},
		},
		"a work account's sign-in name, and sub where there is no oid": {
			claims: map[string]any{"aud": []any{"other", clientID}, "preferred_username": "ada@corp.example.test", "tid": "t1", "sub": "s1"},
			want:   mailbox{Address: "ada@corp.example.test", Subject: "t1:s1"},
		},
		"issued to another app":      {claims: map[string]any{"aud": "someone-else", "email": "ada@example.test"}, fails: true},
		"no address at all":          {claims: map[string]any{"aud": clientID, "preferred_username": "ada"}, fails: true},
		"an address holding a space": {claims: map[string]any{"aud": clientID, "email": "ada @example.test"}, fails: true},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := mailboxFromIDToken(idToken(test.claims), clientID)
			if test.fails != errors.Is(err, errUnknownMailbox) || got != test.want {
				t.Errorf("got %+v, %v", got, err)
			}
		})
	}
	for _, broken := range []string{"", "one.two", "a.!!!.c", "a." + base64.RawURLEncoding.EncodeToString([]byte("[]")) + ".c"} {
		if _, err := mailboxFromIDToken(broken, clientID); !errors.Is(err, errUnknownMailbox) {
			t.Errorf("%q was read as a mailbox", broken)
		}
	}
}

func TestIMAPAccessIsRecognisedHoweverMicrosoftSpellsIt(t *testing.T) {
	for scope, want := range map[string]bool{
		"https://outlook.office.com/IMAP.AccessAsUser.All": true,
		"openid imap.accessasuser.all offline_access":      true,
		"openid email profile offline_access":              false,
		"https://outlook.office.com/SMTP.Send":             false,
		"":                                                 false,
	} {
		if got := (grant{Scopes: strings.Fields(scope)}).GrantsIMAP(); got != want {
			t.Errorf("%q grants IMAP: %v", scope, got)
		}
	}
}

func TestAnAttemptOpensOnlyForItsOwnStateAndInTime(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	begun, err := newAttempt("user-1", "/dashboard/settings/connectors?tab=mail", now)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := begun.seal(secretKey, now)
	if err != nil {
		t.Fatal(err)
	}
	if held, err := reopen(sealed, begun.State, secretKey, now.Add(9*time.Minute)); err != nil || held != begun {
		t.Fatalf("reopened %+v, %v", held, err)
	}
	for name, try := range map[string]func() (attempt, error){
		"after ten minutes": func() (attempt, error) { return reopen(sealed, begun.State, secretKey, now.Add(10*time.Minute)) },
		"another state":     func() (attempt, error) { return reopen(sealed, begun.State+"x", secretKey, now) },
		"another key":       func() (attempt, error) { return reopen(sealed, begun.State, "another key", now) },
		"no cookie":         func() (attempt, error) { return reopen("", begun.State, secretKey, now) },
		"a cookie edited":   func() (attempt, error) { return reopen(sealed[:len(sealed)-4]+"AAAA", begun.State, secretKey, now) },
	} {
		if _, err := try(); !errors.Is(err, errAttemptRefused) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A return path that a browser could read as another site is dropped.
	for _, path := range []string{"//evil.example.test", "https://evil.example.test", `/\evil.example.test`, "dashboard"} {
		if begun, _ := newAttempt("user-1", path, now); begun.ReturnPath != "" {
			t.Errorf("%q was kept as a return path", path)
		}
	}
}

func TestTheSettingsStillToBeSetAreNamed(t *testing.T) {
	if missing := (Settings{}).Missing(); !slices.Equal(missing, []string{
		"MICROSOFT_MAIL_CLIENT_ID", "MICROSOFT_MAIL_CLIENT_SECRET", "MICROSOFT_MAIL_TENANT", "MICROSOFT_MAIL_REDIRECT_URI"}) {
		t.Errorf("nothing set: %v", missing)
	}
	set := Settings{ClientID: clientID, ClientSecret: "fake-secret", Tenant: "common", RedirectURI: "https://api.example.test" + CallbackPath}
	if !set.Configured() {
		t.Errorf("everything set: %v", set.Missing())
	}
	// No tenant is assumed, and one that could change the address's path
	// is not a tenant.
	for _, tenant := range []string{"", "common/../x", "a b"} {
		set.Tenant = tenant
		if set.Configured() {
			t.Errorf("tenant %q was accepted", tenant)
		}
	}
	if TokenURL("consumers") != "https://login.microsoftonline.com/consumers/oauth2/v2.0/token" ||
		AuthorizeURL("consumers") != "https://login.microsoftonline.com/consumers/oauth2/v2.0/authorize" {
		t.Error("Microsoft's addresses are not the tenant's")
	}
}

func TestAMailboxIsPlacedInTheList(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	password := json.RawMessage(`{"id": "pw-1", "username": "ada@example.test", "app_password": "sealed", "_encrypted": true}`)
	first := grant{AccessToken: "fake-access-1", RefreshToken: "fake-refresh-1", Expiry: now.Add(time.Hour)}
	box := mailbox{Address: "ada@example.test", Subject: "t1:o1"}

	list, err := placed([]json.RawMessage{password}, box, first, secretKey, now)
	if err != nil || len(list) != 2 || string(list[0]) != string(password) {
		t.Fatalf("placed %s, %v", list, err)
	}
	var added record
	if err := json.Unmarshal(list[1], &added); err != nil {
		t.Fatal(err)
	}
	// A password mailbox with the same address is not the same mailbox.
	if !added.IsMicrosoft() || added.Text("host") != IMAPHost || added.Text("label") != box.Address ||
		!added.Truth(encryptedKey) || added.Truth(mailsignin.ReconnectKey) || len(added.Text("id")) != 36 {
		t.Errorf("the new record is %s", list[1])
	}
	access, expires, refresh, err := storedTokens(added, secretKey)
	if err != nil || access != "fake-access-1" || refresh != "fake-refresh-1" || !expires.Equal(now.Add(time.Hour)) {
		t.Errorf("stored tokens: %q until %v, %q, %v", access, expires, refresh, err)
	}

	// The same Microsoft account under a new address, flagged meanwhile:
	// the record keeps its id and label, takes the new tokens and address.
	_ = added.Set(map[string]any{mailsignin.ReconnectKey: true, "label": "Work"})
	flagged, _ := json.Marshal(added)
	moved := mailbox{Address: "ada.lovelace@example.test", Subject: "t1:o1"}
	// Microsoft kept the refresh token: the stored one stays.
	again, err := placed([]json.RawMessage{password, flagged}, moved, grant{AccessToken: "fake-access-2", Expiry: now.Add(2 * time.Hour)}, secretKey, now)
	if err != nil || len(again) != 2 {
		t.Fatalf("placed again %s, %v", again, err)
	}
	var updated record
	_ = json.Unmarshal(again[1], &updated)
	access, _, refresh, err = storedTokens(updated, secretKey)
	if updated.Text("id") != added.Text("id") || updated.Text("label") != "Work" || updated.Text("username") != moved.Address ||
		updated.Truth(mailsignin.ReconnectKey) || err != nil || access != "fake-access-2" || refresh != "fake-refresh-1" {
		t.Errorf("the reconnected record is %s (%v)", again[1], err)
	}

	// Another Microsoft account is another mailbox.
	other, err := placed(again, mailbox{Address: "bob@example.test", Subject: "t1:o2"}, first, secretKey, now)
	if err != nil || len(other) != 3 {
		t.Errorf("a second account placed %d records, %v", len(other), err)
	}
	if _, err := placed(nil, mailbox{Address: "a\x00@example.test"}, first, secretKey, now); !errors.Is(err, errUnstorable) {
		t.Errorf("an address jsonb cannot hold: %v", err)
	}
}

func TestStoredTokensThatCannotBeRead(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	fields, err := tokenFields(grant{AccessToken: "fake-access", RefreshToken: "fake-refresh", Expiry: now}, secretKey, now)
	if err != nil {
		t.Fatal(err)
	}
	whole := record{}
	_ = whole.Set(fields)
	_ = whole.Set(map[string]any{encryptedKey: true})
	if _, _, _, err := storedTokens(whole, "another key"); !errors.Is(err, errTokensUnreadable) {
		t.Errorf("another deployment's key: %v", err)
	}
	// No expiry: the access token is not trusted, the refresh token is.
	delete(whole, mailsignin.ExpiresKey)
	if access, _, refresh, err := storedTokens(whole, secretKey); err != nil || access != "" || refresh != "fake-refresh" {
		t.Errorf("no expiry: %q, %q, %v", access, refresh, err)
	}
	delete(whole, mailsignin.RefreshKey)
	if _, _, _, err := storedTokens(whole, secretKey); !errors.Is(err, errTokensUnreadable) {
		t.Errorf("no refresh token: %v", err)
	}
	// A plaintext token (no marker) is never used.
	plain := record{}
	_ = plain.Set(map[string]any{mailsignin.RefreshKey: "fake-plain"})
	if _, _, _, err := storedTokens(plain, secretKey); !errors.Is(err, errTokensUnreadable) {
		t.Errorf("an unsealed record: %v", err)
	}
	// What is stored is sealed.
	var sealed string
	_ = json.Unmarshal(whole[mailsignin.AccessKey], &sealed)
	if opened, err := storedtoken.Open(secretKey, sealed); err != nil || opened != "fake-access" || sealed == "fake-access" {
		t.Errorf("the access token is stored as %q", sealed)
	}
}
