package driveconnect

import (
	"crypto/rand"
	"encoding/json"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivegrant"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/storedtoken"
)

// accountKeys are the config keys that belong to one Google account: the
// person's picks and the position in that account's change feed. They are
// dropped when a different account connects.
var accountKeys = []string{"selected_files", "selected_folders", "folder_tokens", "start_page_token"}

// storedGrant is a row's config after a (re)connect: the new grant, with
// what the row held kept beside it.
//
// The grant's fields are the ones the Python tier stored (the Google
// library's own credential JSON), with the two tokens and the client
// secret sealed and `_token_encrypted` saying so. Both tiers, the removal
// that revokes a grant (internal/connectionrevocation) and the worker that
// reads picked files all read that one shape.
func storedGrant(previous []byte, granted grant, email string, settings Settings, now time.Time) ([]byte, error) {
	kept := map[string]json.RawMessage{}
	if len(previous) > 0 {
		// A config that is not an object is replaced whole.
		_ = json.Unmarshal(previous, &kept)
	}
	var before string
	_ = json.Unmarshal(kept["account_email"], &before)
	if email == "" || before != email {
		for _, key := range accountKeys {
			delete(kept, key)
		}
	}
	set := func(key string, value any) error {
		encoded, err := json.Marshal(value)
		if err == nil {
			kept[key] = encoded
		}
		return err
	}
	for key, secret := range map[string]string{
		"token": granted.AccessToken, "refresh_token": granted.RefreshToken, "client_secret": settings.ClientSecret,
	} {
		sealed, err := storedtoken.Seal(settings.SecretKey, secret, now, rand.Reader)
		if err != nil {
			return nil, err
		}
		if err := set(key, sealed); err != nil {
			return nil, err
		}
	}
	var address any
	if email != "" {
		address = email
	}
	for key, value := range map[string]any{
		"token_uri":        settings.TokenURL,
		"client_id":        settings.ClientID,
		"scopes":           granted.Scopes,
		"universe_domain":  "googleapis.com",
		"account":          "",
		"expiry":           granted.Expiry.UTC().Format(drivegrant.ExpiryLayout),
		"_token_encrypted": true,
		"account_email":    address,
		"auth_expired":     false,
	} {
		if err := set(key, value); err != nil {
			return nil, err
		}
	}
	return json.Marshal(kept)
}
