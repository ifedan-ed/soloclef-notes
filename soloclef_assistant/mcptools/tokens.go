package mcptools

import (
	"context"
	"errors"
	"fmt"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/mcpserver"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/sealedsecret"
)

// errSignInNeeded is an OAuth server whose sign-in cannot be used: never
// made, revoked, or expired with no way to renew it. The person signs in
// again; nothing here can.
var errSignInNeeded = errors.New("mcptools: the server's OAuth sign-in has ended")

// renewEarly is how long before its stated expiry an access token is
// renewed, so that a token does not run out between being read and being
// sent.
const renewEarly = 60

// currentTokens is an OAuth server's tokens, renewed first when the access
// token has run out or is about to.
func (s *Source) currentTokens(ctx context.Context, userID string, row Connection, stored mcpserver.Stored, origin string) (*mcpserver.Tokens, error) {
	tokens, err := FreshTokens(ctx, TokenRenewal{
		OAuth: s.oauth, Seal: s.seal, Rows: s.rows, Now: s.now().Unix(),
		Hosts: OAuthHosts(origin, s.hosts(target{origin: origin, address: stored.URL}, row.SiteHosts)),
	}, userID, row.ID, stored.Auth.OAuth)
	if errors.Is(err, errSignInNeeded) {
		s.expire(ctx, userID, row.ID)
	}
	return tokens, err
}

// TokenRenewal is what renewing a server's access token needs. The chat's
// connections and the settings page's test both renew through FreshTokens,
// so a token is renewed one way.
type TokenRenewal struct {
	OAuth OAuth
	Seal  sealedsecret.Seal
	Rows  TokenRows
	// Now is the time in Unix seconds.
	Now   int64
	Hosts HostsFor
}

// TokenRows is the part of the database a renewal writes.
type TokenRows interface {
	// StoreTokens replaces a server's sealed tokens, but only while the
	// row still holds previous: two processes that renew at once cannot
	// both win, and the loser reads the winner's (Connection).
	StoreTokens(ctx context.Context, userID string, connectorID int64, previous, sealed string, expiresAt int64) (stored bool, err error)
	Connection(ctx context.Context, userID string, connectorID int64) (*Connection, error)
}

// FreshTokens opens an OAuth server's tokens and, when the access token has
// run out, renews it with the refresh token and stores the new ones sealed.
//
// The renewal is not done under a lock held across the network: the write
// is conditional on the stored tokens being the ones read, and the process
// that loses that race uses what the winner stored instead of renewing
// again, which a server that rotates refresh tokens would refuse.
func FreshTokens(ctx context.Context, renewal TokenRenewal, userID string, connectorID int64, oauth *mcpserver.StoredOAuth) (*mcpserver.Tokens, error) {
	if !oauth.Authorized() {
		return nil, errSignInNeeded
	}
	tokens, err := mcpserver.OpenTokens(renewal.Seal, userID, oauth)
	if err != nil {
		return nil, err
	}
	if oauth.ExpiresAt == 0 || renewal.Now < oauth.ExpiresAt-renewEarly {
		return tokens, nil
	}
	if tokens.RefreshToken == "" || oauth.TokenEndpoint == "" {
		return nil, errSignInNeeded
	}
	secret, err := mcpserver.OpenClientSecret(renewal.Seal, userID, oauth)
	if err != nil {
		return nil, err
	}
	client := Client{ID: oauth.ClientID, Secret: secret, AuthMethod: oauth.ClientAuth}
	granted, err := renewal.OAuth.Refresh(ctx, oauth.TokenEndpoint, client, tokens.RefreshToken, oauth.Resource, renewal.Hosts)
	if IsInvalidGrant(err) {
		return nil, errSignInNeeded
	}
	if err != nil {
		return nil, fmt.Errorf("renewing the server's access token: %w", err)
	}
	renewed := mcpserver.Tokens{AccessToken: granted.AccessToken, RefreshToken: granted.RefreshToken}
	if renewed.RefreshToken == "" {
		// Not rotated: the old refresh token stays good.
		renewed.RefreshToken = tokens.RefreshToken
	}
	sealed, err := mcpserver.SealTokens(renewal.Seal, userID, renewed)
	if err != nil {
		return nil, err
	}
	expiresAt := int64(0)
	if granted.ExpiresIn > 0 {
		expiresAt = renewal.Now + granted.ExpiresIn
	}
	stored, err := renewal.Rows.StoreTokens(ctx, userID, connectorID, oauth.Tokens, sealed, expiresAt)
	if err != nil {
		return nil, err
	}
	if stored {
		return &renewed, nil
	}
	// Another process renewed first, or the server was edited meanwhile:
	// the row as it is now is the answer, and it is not renewed again.
	row, err := renewal.Rows.Connection(ctx, userID, connectorID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, errSignInNeeded
	}
	current, _, err := readServer(row.Config)
	if err != nil || current.Auth.Type != mcpserver.AuthOAuth || !current.Auth.OAuth.Authorized() {
		return nil, errSignInNeeded
	}
	return mcpserver.OpenTokens(renewal.Seal, userID, current.Auth.OAuth)
}

// IsSignInNeeded reports whether err is an OAuth sign-in that has ended.
func IsSignInNeeded(err error) bool { return errors.Is(err, errSignInNeeded) }
