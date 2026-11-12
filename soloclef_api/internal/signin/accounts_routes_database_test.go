package signin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer"
)

// recordingMailer keeps what it was asked to send, and can be told to fail.
type recordingMailer struct {
	mutex sync.Mutex
	sent  []mailer.Message
	fail  error
}

func (m *recordingMailer) Send(_ context.Context, message mailer.Message) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if m.fail != nil {
		return m.fail
	}
	m.sent = append(m.sent, message)
	return nil
}

func (m *recordingMailer) messages() []mailer.Message {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return append([]mailer.Message(nil), m.sent...)
}

// variant serves the same routes over the same database with one thing
// about the deployment changed.
func (h *harness) variant(change func(*Routes)) func(*http.Request) *httptest.ResponseRecorder {
	routes := h.routes
	change(&routes)
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	routes.Register(api)
	return func(request *http.Request) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		api.Adapter().ServeHTTP(recorder, request)
		return recorder
	}
}

func jsonPost(path string, body any) *http.Request {
	raw, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
	request.Header.Set("Content-Type", "application/json")
	return request
}

// refusalOf is the message of the tier's error envelope.
func refusalOf(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("not an error envelope: %s", recorder.Body)
	}
	return envelope.Error.Message
}

// tokenIn is the reset token in a mail's link.
func tokenIn(t *testing.T, message mailer.Message) string {
	t.Helper()
	found := regexp.MustCompile(`https://notes\.example/reset-password#token=(\S+)`).FindStringSubmatch(message.TextBody)
	if found == nil {
		t.Fatalf("the mail carries no reset link: %s", message.TextBody)
	}
	return found[1]
}

func TestSignUpMakesAnUnverifiedOrdinaryAccountThatCanSignIn(t *testing.T) {
	h, count := newHarness(t)

	// The body claims to be an administrator and verified; neither is read.
	made := h.do(jsonPost(RegisterPath, map[string]any{
		"email": " Carol@Example.Test ", "password": "a long enough password",
		"is_superuser": true, "is_verified": true,
	}))
	if made.Code != http.StatusCreated {
		t.Fatalf("the sign-up answered %d %s", made.Code, made.Body)
	}
	var account registered
	if err := json.Unmarshal(made.Body.Bytes(), &account); err != nil {
		t.Fatal(err)
	}
	if account.Email != "Carol@Example.Test" || !account.IsActive || account.IsSuperuser || account.IsVerified {
		t.Errorf("the new account is %+v", account)
	}
	if n := count(`SELECT count(*) FROM "user" WHERE id = $1 AND NOT is_superuser AND NOT is_verified AND hashed_password LIKE '$argon2id$%'`, account.ID); n != 1 {
		t.Error("the stored account is not an ordinary, unverified one with an Argon2id hash")
	}
	// It signs in, in any spelling of the address, as itself.
	signedIn := h.webSignIn("carol@example.test", "a long enough password")
	if signedIn.Code != http.StatusOK || userOf(t, cookiesOf(signedIn)["s_session"].Value) != account.ID {
		t.Errorf("the new account's sign-in answered %d", signedIn.Code)
	}

	for _, refused := range []struct {
		why  string
		body map[string]any
		want string
	}{
		{"an address that has an account, in another case", map[string]any{"email": "ALICE@example.test", "password": "a long enough password"}, registerAlreadyExists},
		{"a short password", map[string]any{"email": "dave@example.test", "password": "short"}, registerInvalidPassword},
		{"the address as the password", map[string]any{"email": "dave@example.test", "password": "dave@example.test"}, registerInvalidPassword},
		{"no address", map[string]any{"password": "a long enough password"}, registerInvalidEmail},
		{"a name around the address", map[string]any{"email": "Dave <dave@example.test>", "password": "a long enough password"}, registerInvalidEmail},
		{"an address with no domain", map[string]any{"email": "dave@localhost", "password": "a long enough password"}, registerInvalidEmail},
	} {
		got := h.do(jsonPost(RegisterPath, refused.body))
		if got.Code != http.StatusBadRequest || refusalOf(t, got) != refused.want {
			t.Errorf("%s answered %d %s, want 400 %s", refused.why, got.Code, got.Body, refused.want)
		}
	}
	// Alice's account is as it was: a sign-up in her name changed nothing
	// of hers, and Bob's password still opens only Bob's.
	if got := h.webSignIn("alice@example.test", "a long enough password"); got.Code != http.StatusBadRequest {
		t.Errorf("a refused sign-up in Alice's name gave her account a password: %d", got.Code)
	}
	if n := count(`SELECT count(*) FROM "user"`); n != 3 {
		t.Errorf("%d accounts, want Alice, Bob and Carol", n)
	}
}

func TestSignUpIsRefusedWhereTheDeploymentDoesNotAllowIt(t *testing.T) {
	h, count := newHarness(t)
	closed := h.variant(func(r *Routes) { r.SignupAllowed = false })
	got := closed(jsonPost(RegisterPath, map[string]any{"email": "carol@example.test", "password": "a long enough password"}))
	if got.Code != http.StatusForbidden || refusalOf(t, got) != registerDisabled {
		t.Errorf("answered %d %s", got.Code, got.Body)
	}
	if n := count(`SELECT count(*) FROM "user"`); n != 2 {
		t.Errorf("%d accounts after a refused sign-up", n)
	}
}

func TestAResetLinkIsMailedOnlyToAnAccountAndTheAnswerNeverSaysWhich(t *testing.T) {
	h, _ := newHarness(t)
	h.exec(`UPDATE "user" SET is_active = false WHERE id = $1`, testpostgres.Bob)

	answers := map[string]string{}
	for _, address := range []string{"ALICE@example.test", "bob@example.test", "nobody@example.test", "not an address"} {
		got := h.do(jsonPost(ForgotPath, map[string]string{"email": address}))
		if got.Code != http.StatusAccepted {
			t.Errorf("%q answered %d %s", address, got.Code, got.Body)
		}
		answers[got.Body.String()] = address
	}
	if len(answers) != 1 {
		t.Errorf("the answers differ by address: %v", answers)
	}
	// One mail: to Alice, at the address the account holds, from the
	// deployment's default sender, with a link to the web app.
	sent := h.mail.messages()
	if len(sent) != 1 || sent[0].To != "alice@example.test" || sent[0].Sender != "" ||
		sent[0].Subject != "Reset your Example Notes password" {
		t.Fatalf("mailed %+v", sent)
	}
	token := tokenIn(t, sent[0])
	if !strings.Contains(sent[0].HTMLBody, "https://notes.example/reset-password#token="+token) {
		t.Error("the HTML part does not carry the same link")
	}
}

func TestAResetSetsThePasswordOnceAndOnlyForTheAccountTheLinkWasMailedTo(t *testing.T) {
	h, count := newHarness(t)
	// Both are signed in on a phone before anything is reset.
	alicePhone := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "Alice's phone"))
	bobPhone := pairOf(t, h.phoneSignIn("bob@example.test", bobPassword, "Bob's phone"))

	if got := h.do(jsonPost(ForgotPath, map[string]string{"email": "alice@example.test"})); got.Code != http.StatusAccepted {
		t.Fatal(got.Code)
	}
	token := tokenIn(t, h.mail.messages()[0])
	const fresh = "an entirely new passphrase"

	// A password the rules refuse leaves the token unspent.
	if got := h.do(jsonPost(ResetPath, map[string]string{"token": token, "password": "short"})); got.Code != http.StatusBadRequest || refusalOf(t, got) != resetInvalidPassword {
		t.Errorf("a short password answered %d %s", got.Code, got.Body)
	}
	if got := h.do(jsonPost(ResetPath, map[string]string{"token": token, "password": fresh})); got.Code != http.StatusOK {
		t.Fatalf("the reset answered %d %s", got.Code, got.Body)
	}

	// Alice: the new password works, the old one does not, her address is
	// now verified, and the phone she was signed in on is signed out.
	if got := h.webSignIn("alice@example.test", fresh); got.Code != http.StatusOK || userOf(t, cookiesOf(got)["s_session"].Value) != testpostgres.Alice {
		t.Errorf("the new password answered %d", got.Code)
	}
	if got := h.webSignIn("alice@example.test", alicePassword); got.Code != http.StatusBadRequest {
		t.Errorf("the old password answered %d", got.Code)
	}
	if n := count(`SELECT count(*) FROM "user" WHERE id = $1 AND is_verified`, testpostgres.Alice); n != 1 {
		t.Error("a reset by mail did not verify the address")
	}
	if got := h.bodyRefresh(RefreshPath, *alicePhone.RefreshToken); got.Code != http.StatusUnauthorized {
		t.Errorf("her earlier sign-in survived the reset: %d", got.Code)
	}
	// Bob: untouched. His password, his sign-in and his unverified address
	// are as they were.
	if got := h.webSignIn("bob@example.test", bobPassword); got.Code != http.StatusOK {
		t.Errorf("Bob's password answered %d after Alice's reset", got.Code)
	}
	if got := h.webSignIn("bob@example.test", fresh); got.Code != http.StatusBadRequest {
		t.Errorf("Alice's new password opened Bob's account: %d", got.Code)
	}
	if got := h.bodyRefresh(RefreshPath, *bobPhone.RefreshToken); got.Code != http.StatusOK {
		t.Errorf("Bob's sign-in was ended by Alice's reset: %d", got.Code)
	}
	if n := count(`SELECT count(*) FROM "user" WHERE id = $1 AND is_verified`, testpostgres.Bob); n != 0 {
		t.Error("Alice's reset verified Bob's address")
	}

	// The link works once: the password it was minted beside is gone.
	if got := h.do(jsonPost(ResetPath, map[string]string{"token": token, "password": "yet another passphrase"})); got.Code != http.StatusBadRequest || refusalOf(t, got) != resetBadToken {
		t.Errorf("the spent link answered %d %s", got.Code, got.Body)
	}
}

func TestAResetTokenIsRefusedWhenItIsNotThisTiersOrNoLongerFits(t *testing.T) {
	h, _ := newHarness(t)
	now := h.clock
	aliceHash := func() *string {
		var hash *string
		if err := h.pool.QueryRow(context.Background(), `SELECT hashed_password FROM "user" WHERE id = $1`, testpostgres.Alice).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		return hash
	}
	good, err := mintResetToken(secret, testpostgres.Alice, aliceHash(), now)
	if err != nil {
		t.Fatal(err)
	}
	forged, _ := mintResetToken("another-secret", testpostgres.Alice, aliceHash(), now)
	// A link minted for Bob's password does not fit Alice's account even
	// under the right signature: the fingerprint is of the wrong password.
	var bobHash *string
	_ = h.pool.QueryRow(context.Background(), `SELECT hashed_password FROM "user" WHERE id = $1`, testpostgres.Bob).Scan(&bobHash)
	crossed, _ := mintResetToken(secret, testpostgres.Alice, bobHash, now)
	// An access token is signed with the same secret and is not a reset.
	access, _, _ := mintAccess(secret, testpostgres.Alice, "0b9a6f3e-5d2c-4e8f-9a1b-2c3d4e5f6a7b", now)

	for why, token := range map[string]string{
		"a token signed with another secret":             forged,
		"a token carrying another account's fingerprint": crossed,
		"an access token":                                access,
		"nothing":                                        "",
		"not a token":                                    "not.a.token",
	} {
		got := h.do(jsonPost(ResetPath, map[string]string{"token": token, "password": "an entirely new passphrase"}))
		if got.Code != http.StatusBadRequest || refusalOf(t, got) != resetBadToken {
			t.Errorf("%s answered %d %s", why, got.Code, got.Body)
		}
	}

	// After its hour the good token is refused; just inside it, it works.
	h.clock = now.Add(resetLifetime + time.Second)
	if got := h.do(jsonPost(ResetPath, map[string]string{"token": good, "password": "an entirely new passphrase"})); got.Code != http.StatusBadRequest {
		t.Errorf("an expired link answered %d", got.Code)
	}
	h.clock = now.Add(resetLifetime - time.Minute)
	// A deactivated account's link does not work either.
	h.exec(`UPDATE "user" SET is_active = false WHERE id = $1`, testpostgres.Alice)
	if got := h.do(jsonPost(ResetPath, map[string]string{"token": good, "password": "an entirely new passphrase"})); got.Code != http.StatusBadRequest {
		t.Errorf("a deactivated account's link answered %d", got.Code)
	}
	h.exec(`UPDATE "user" SET is_active = true WHERE id = $1`, testpostgres.Alice)
	if got := h.do(jsonPost(ResetPath, map[string]string{"token": good, "password": "an entirely new passphrase"})); got.Code != http.StatusOK {
		t.Errorf("the link inside its hour answered %d %s", got.Code, got.Body)
	}
}

func TestAnAccountWithNoPasswordGainsOneByTheMailedLink(t *testing.T) {
	h, _ := newHarness(t)
	// As single sign-on makes one: no password at all.
	h.exec(`UPDATE "user" SET hashed_password = NULL WHERE id = $1`, testpostgres.Alice)
	if got := h.do(jsonPost(ForgotPath, map[string]string{"email": "alice@example.test"})); got.Code != http.StatusAccepted {
		t.Fatal(got.Code)
	}
	token := tokenIn(t, h.mail.messages()[0])
	if got := h.do(jsonPost(ResetPath, map[string]string{"token": token, "password": "her first passphrase"})); got.Code != http.StatusOK {
		t.Fatalf("answered %d %s", got.Code, got.Body)
	}
	if got := h.webSignIn("alice@example.test", "her first passphrase"); got.Code != http.StatusOK {
		t.Errorf("the first password answered %d", got.Code)
	}
}

func TestADeploymentThatSendsNoMailSaysSoAndAFailedSendIsNotAnError(t *testing.T) {
	h, _ := newHarness(t)
	silent := h.variant(func(r *Routes) { r.Mail = nil })
	// The same refusal for an account and for nobody.
	for _, address := range []string{"alice@example.test", "nobody@example.test"} {
		got := silent(jsonPost(ForgotPath, map[string]string{"email": address}))
		if got.Code != http.StatusServiceUnavailable || refusalOf(t, got) != resetUnavailable {
			t.Errorf("%s answered %d %s", address, got.Code, got.Body)
		}
	}
	// The mail server refusing is not the person's to hear about: the
	// answer went out before the send, and says what it always says.
	h.mail.fail = errors.New("the mail server is down")
	if got := h.do(jsonPost(ForgotPath, map[string]string{"email": "alice@example.test"})); got.Code != http.StatusAccepted {
		t.Errorf("a failed send answered %d", got.Code)
	}
}

func TestVerifyTokenNamesTheCallerAndHowTheyProvedIt(t *testing.T) {
	h, _ := newHarness(t)
	for _, caller := range []principal.Principal{
		{UserID: testpostgres.Alice, Method: principal.MethodAgent},
		{UserID: testpostgres.Bob, Method: principal.MethodSession},
	} {
		request := httptest.NewRequest(http.MethodGet, VerifyPath, nil)
		got := h.do(request.WithContext(principal.WithPrincipal(request.Context(), caller)))
		want := `{"message":"Token is valid","method":"` + string(caller.Method) + `","user_id":"` + caller.UserID + `"}`
		if got.Code != http.StatusOK || strings.TrimSpace(got.Body.String()) != want {
			t.Errorf("answered %d %s, want %s", got.Code, got.Body, want)
		}
	}
}
