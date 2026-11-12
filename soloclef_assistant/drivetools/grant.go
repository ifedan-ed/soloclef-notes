package drivetools

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivegrant"
)

// What the model is told when Drive cannot be used, each with what the
// person can do about it.
var (
	errNotConnected = errors.New("the user has not connected Google Drive, or has switched the connection off; it is set up in Connectors, Google Drive")
	errReconnect    = errors.New("Google no longer accepts the user's Drive connection; they need to connect Google Drive again in Connectors")
	errUnavailable  = errors.New("Google Drive could not be reached just now; try again shortly")
)

// tokenMargin is how much life a stored access token must have left to be
// used: longer than one tool call's work.
const tokenMargin = 2 * time.Minute

// accessToken is a token good for one tool call: the stored one while it
// lasts, or a renewed one, which is stored sealed so that the next call,
// the sync and the picker do not each ask Google again within the hour.
func (t *Tools) accessToken(ctx context.Context, userID string) (string, error) {
	token, _, err := t.grant(ctx, userID)
	return token, err
}

// grant is accessToken and the connection it was read from, whose parser a
// document read from Drive is read with.
func (t *Tools) grant(ctx context.Context, userID string) (string, Connection, error) {
	connection, found, err := t.Rows.Connection(ctx, userID)
	if err != nil {
		t.Logger.LogAttrs(ctx, slog.LevelError, "an account's Drive connection could not be read", slog.String("error", err.Error()), slog.String("user_id", userID))
		return "", Connection{}, errUnavailable
	}
	if !found {
		return "", Connection{}, errNotConnected
	}
	token, err := t.renewed(ctx, userID, connection)
	return token, connection, err
}

// renewed is a token good for one call from the connection's stored grant.
func (t *Tools) renewed(ctx context.Context, userID string, connection Connection) (string, error) {
	stored := drivegrant.Read(connection.Config)
	if stored.AuthExpired {
		return "", errReconnect
	}
	now := t.Now()
	if token, good := stored.OpenAccessToken(t.SecretKey, now, tokenMargin); good {
		return token, nil
	}
	refreshToken, readable := stored.OpenRefreshToken(t.SecretKey)
	if !readable {
		// No refresh token, or one sealed with another key: nothing this
		// deployment can renew.
		return "", errReconnect
	}
	renewed, err := t.Google.Refresh(ctx, refreshToken)
	if errors.Is(err, drivegrant.ErrInvalidGrant) {
		// Remembered, so the settings page offers to connect again and the
		// next call does not ask Google the same question.
		if markErr := t.Rows.MergeConfig(context.WithoutCancel(ctx), userID, connection.ID, []byte(`{"auth_expired": true}`)); markErr != nil {
			t.Logger.LogAttrs(ctx, slog.LevelWarn, "a refused Drive grant could not be flagged", slog.String("error", markErr.Error()))
		}
		return "", errReconnect
	}
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		t.Logger.LogAttrs(ctx, slog.LevelWarn, "renewing a Drive grant failed", slog.String("error", err.Error()))
		return "", errUnavailable
	}
	patch, err := stored.Renewed(t.SecretKey, renewed, now, rand.Reader)
	if err == nil {
		err = t.Rows.MergeConfig(context.WithoutCancel(ctx), userID, connection.ID, patch)
	}
	if err != nil {
		// The token in hand is good; only the saving of it failed, and the
		// next call renews again.
		t.Logger.LogAttrs(ctx, slog.LevelWarn, "a renewed Drive token could not be stored", slog.String("error", err.Error()))
	}
	return renewed.AccessToken, nil
}
