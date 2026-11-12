package drivesync

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivegrant"
)

// errReconnect: the connection has no grant Google accepts. The remedy is
// the person's: connect Drive again.
var errReconnect = errors.New("drivesync: the connection needs a new consent at Google")

// accessToken is a token good for this job: the stored one while it lasts,
// or a renewed one, which is stored sealed for the next job and for the
// Picker so that neither asks Google again within the hour.
func (j *Job) accessToken(ctx context.Context, userID string, connection *Connection) (string, error) {
	stored := drivegrant.Read(connection.Config)
	if stored.AuthExpired {
		return "", errReconnect
	}
	now := j.Now()
	if token, good := stored.OpenAccessToken(j.SecretKey, now, tokenMargin); good {
		return token, nil
	}
	refreshToken, readable := stored.OpenRefreshToken(j.SecretKey)
	if !readable {
		// No refresh token, or one sealed with another key: nothing this
		// deployment can renew.
		return "", errReconnect
	}
	renewed, err := j.Google.Refresh(ctx, refreshToken)
	if errors.Is(err, drivegrant.ErrInvalidGrant) {
		// Remembered, so the settings page offers to connect again and
		// the next tick does not ask Google the same question.
		if markErr := j.Rows.MarkExpired(context.WithoutCancel(ctx), userID, connection.ID); markErr != nil {
			j.Log.Warn("a refused Drive grant could not be flagged", "connector", connection.ID, "error", markErr.Error())
		}
		return "", errReconnect
	}
	if err != nil {
		return "", fmt.Errorf("%w: %w", errUnavailable, err)
	}
	patch, err := stored.Renewed(j.SecretKey, renewed, now, rand.Reader)
	if err == nil {
		err = j.Rows.MergeConfig(ctx, userID, connection.ID, patch)
	}
	if err != nil {
		// The token in hand is good; only the saving of it failed, and the
		// next job renews again.
		j.Log.Warn("a renewed Drive token could not be stored", "connector", connection.ID, "error", err.Error())
	}
	return renewed.AccessToken, nil
}
