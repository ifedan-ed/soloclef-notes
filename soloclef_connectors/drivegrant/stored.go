package drivegrant

import (
	"encoding/json"
	"io"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/storedtoken"
)

// ExpiryLayout is how a stored grant writes when its access token runs
// out: UTC with microseconds and a Z, the form Google's Python library
// wrote and every reader of a Drive row has always been given.
const ExpiryLayout = "2006-01-02T15:04:05.000000Z"

// Stored is what of a connection's config says about its grant.
type Stored struct {
	// AccessToken and RefreshToken are as stored: sealed when Encrypted.
	AccessToken  string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	Expiry       string `json:"expiry"`
	Encrypted    bool   `json:"_token_encrypted"`
	AccountEmail string `json:"account_email"`
	// AuthExpired is set once Google has refused the grant; the settings
	// page then offers to connect again.
	AuthExpired bool `json:"auth_expired"`
}

// Read is the grant in a connection's config. A config that is not an
// object, or holds no grant, reads as the zero Stored.
func Read(config []byte) Stored {
	var stored Stored
	_ = json.Unmarshal(config, &stored)
	return stored
}

// open is one stored token in the clear. ok is false when there is none or
// this deployment's key cannot open it.
func (s Stored) open(secretKey, held string) (string, bool) {
	if held == "" {
		return "", false
	}
	if !s.Encrypted {
		return held, true
	}
	opened, err := storedtoken.Open(secretKey, held)
	return opened, err == nil && opened != ""
}

// OpenRefreshToken is the stored refresh token in the clear.
func (s Stored) OpenRefreshToken(secretKey string) (string, bool) {
	return s.open(secretKey, s.RefreshToken)
}

// OpenAccessToken is the stored access token, if it is still good at
// `now` with `margin` to spare. The margin is the caller's: how long the
// work it is about to do with the token may take.
func (s Stored) OpenAccessToken(secretKey string, now time.Time, margin time.Duration) (string, bool) {
	expiry, err := time.Parse(ExpiryLayout, s.Expiry)
	if err != nil || !now.Add(margin).Before(expiry) {
		return "", false
	}
	return s.open(secretKey, s.AccessToken)
}

// Renewed is the keys of the config a renewal changes, as a JSON object to
// merge over the row's config: the new access token and when it runs out.
// Everything else on the row (the refresh token, the person's picks, the
// position in the change feed) is not in it and so is kept.
//
// The token is written in the form the row is already in, sealed on a
// sealed row, so every reader of the row keeps reading one form.
func (s Stored) Renewed(secretKey string, renewed Grant, now time.Time, random io.Reader) ([]byte, error) {
	token := renewed.AccessToken
	if s.Encrypted {
		sealed, err := storedtoken.Seal(secretKey, token, now, random)
		if err != nil {
			return nil, err
		}
		token = sealed
	}
	return json.Marshal(map[string]string{"token": token, "expiry": renewed.Expiry.UTC().Format(ExpiryLayout)})
}
