package nextcloud

import (
	"errors"
	"strings"
	"testing"
)

// A token the Python tier's own library wrote for a secret made up for
// these tests (soloclef_connectors/storedtoken's tests say how, and test the reader).
const (
	vectorSecret = "test-vector-secret-not-a-real-key"
	vectorLong   = "gAAAAABq9Iq7NAQ3pWlKWW79LYRYl2pUa2e-F60LMLbmkg_d7PBJjjTciR3kxkfQw9zp6_TjtxReKRTayvKrw6CQrg83OsSDHGne0cjbivazpSPUqjsQo-s="
)

func credentials(t *testing.T) Credentials {
	t.Helper()
	held, err := NewCredentials(vectorSecret, "https://cloud.example/nc/")
	if err != nil {
		t.Fatal(err)
	}
	return held
}

func TestAConnectionsStoredSettingsBecomeAnAccount(t *testing.T) {
	account, err := credentials(t).Account([]byte(`{"server_url": "https://cloud.example/nc", "username": "ada",
		"login_name": "ada@example.org", "display_name": "Ada", "auth_method": "login_flow",
		"app_password": "` + vectorLong + `", "_token_encrypted": true}`))
	if err != nil {
		t.Fatal(err)
	}
	want := Account{BaseURL: "https://cloud.example/nc", Username: "ada", LoginName: "ada@example.org", AppPassword: "app-password-Abc12-xyZ34 é"}
	if account != want {
		t.Errorf("account %+v, want %+v", account, want)
	}

	// A row saved from a pasted app password, before the sign-in flow: the
	// password is in the clear and the login name is the user id.
	account, err = credentials(t).Account([]byte(`{"server_url": "https://cloud.example/nc", "username": "ada", "app_password": "pasted"}`))
	if err != nil || account.AppPassword != "pasted" || account.LoginName != "ada" {
		t.Errorf("an older row became %+v, %v", account, err)
	}
}

func TestAConnectionThatCannotBeUsedSaysToConnectAgain(t *testing.T) {
	for name, test := range map[string]struct{ config, says string }{
		"made for another server": {`{"server_url": "https://elsewhere.example", "username": "ada", "app_password": "x"}`, "for another server"},
		"the same host over http": {`{"server_url": "http://cloud.example/nc", "username": "ada", "app_password": "x"}`, "for another server"},
		"no password":             {`{"server_url": "https://cloud.example/nc", "username": "ada"}`, "incomplete"},
		"no user":                 {`{"server_url": "https://cloud.example/nc", "app_password": "x"}`, "incomplete"},
		"encrypted with another key": {`{"server_url": "https://cloud.example/nc", "username": "ada", "app_password": "gAAAAA-invented-not-real-ciphertext", "_token_encrypted": true}`,
			"can no longer be read"},
		"not an object": {`[]`, "incomplete"},
	} {
		_, err := credentials(t).Account([]byte(test.config))
		var reconnect *ReconnectError
		if !errors.As(err, &reconnect) || !strings.Contains(reconnect.Message, test.says) || !strings.Contains(reconnect.Message, "connect again") {
			t.Errorf("%s: %v, want a reconnect refusal saying %q", name, err, test.says)
		}
	}
}

func TestTheServerAddressIsAPlainURLOrNothing(t *testing.T) {
	for raw, want := range map[string]string{
		"https://cloud.example":                          "https://cloud.example",
		" https://cloud.example/ ":                       "https://cloud.example",
		"https://cloud.example/nc/":                      "https://cloud.example/nc",
		"https://cloud.example/nc/remote.php/dav/files/": "https://cloud.example/nc",
		"http://10.0.0.5:8080":                           "http://10.0.0.5:8080",
	} {
		if got, ok := NormalizeServerURL(raw); !ok || got != want {
			t.Errorf("NormalizeServerURL(%q) = %q, %v; want %q", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"", "cloud.example", "ftp://cloud.example", "https://", "https://ada:secret@cloud.example",
		"https://cloud.example/?next=1", "https://cloud.example/#x"} {
		if got, ok := NormalizeServerURL(raw); ok {
			t.Errorf("NormalizeServerURL(%q) was accepted as %q", raw, got)
		}
	}
	if _, err := NewCredentials("", "https://cloud.example"); err == nil {
		t.Error("credentials were built with no secret key")
	}
	if _, err := NewCredentials("k", "cloud.example"); err == nil {
		t.Error("credentials were built with no usable server address")
	}
}
