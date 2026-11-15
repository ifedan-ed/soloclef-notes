package mcptools

import (
	"context"
	"encoding/json"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/mcpserver"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/sealedsecret"
)

// SignOut ends an OAuth server's sign-in at its authorization server when
// the server is removed (RFC 7009), so a removed connection is dead there
// too and not only forgotten here. Both tokens are revoked, the refresh
// token first: revoking it usually ends the whole grant, and the access
// token is revoked as well for a server that ends only what it is given.
type SignOut struct {
	OAuth OAuth
	Seal  sealedsecret.Seal
	// SiteHosts is the site's allowed hosts now, which a site server's
	// revocation endpoint is held to as its connections are.
	SiteHosts func(ctx context.Context) ([]string, error)
}

// The outcomes, for the removal's log line. None names a token.
const (
	SignOutRevoked      = "revoked"
	SignOutNothing      = "no credential was stored"
	SignOutNoEndpoint   = "not revoked: the authorization server offers no revocation"
	signOutUnreadable   = "not revoked: the stored settings could not be read"
	signOutUnopened     = "not revoked: the stored tokens cannot be opened with this deployment's secret"
	signOutUnreachable  = "not revoked: the authorization server did not take the revocation"
	signOutHostsUnknown = "not revoked: the site's allowed hosts could not be read"
)

// RevokeServer revokes the sign-in held in a removed MCP server's config,
// best effort: the server is removed whatever this answers, and the answer
// is for the log.
func (s SignOut) RevokeServer(ctx context.Context, ownerID string, config []byte) string {
	stored, origin, err := readServer(config)
	if err != nil {
		var top map[string]json.RawMessage
		if json.Unmarshal(config, &top) == nil && top[keyServer] == nil {
			return SignOutNothing
		}
		return signOutUnreadable
	}
	oauth := stored.Auth.OAuth
	if stored.Auth.Type != mcpserver.AuthOAuth || !oauth.Authorized() {
		return SignOutNothing
	}
	if oauth.RevocationEndpoint == "" {
		return SignOutNoEndpoint
	}
	tokens, err := mcpserver.OpenTokens(s.Seal, ownerID, oauth)
	if err != nil {
		return signOutUnopened
	}
	secret, err := mcpserver.OpenClientSecret(s.Seal, ownerID, oauth)
	if err != nil {
		return signOutUnopened
	}
	var site []string
	if origin != originMember {
		if site, err = s.SiteHosts(ctx); err != nil {
			return signOutHostsUnknown
		}
	}
	hosts := OAuthHosts(origin, site)
	client := Client{ID: oauth.ClientID, Secret: secret, AuthMethod: oauth.ClientAuth}
	outcome := SignOutRevoked
	if tokens.RefreshToken != "" {
		if err := s.OAuth.Revoke(ctx, oauth.RevocationEndpoint, client, tokens.RefreshToken, "refresh_token", hosts); err != nil {
			outcome = signOutUnreachable
		}
	}
	if err := s.OAuth.Revoke(ctx, oauth.RevocationEndpoint, client, tokens.AccessToken, "access_token", hosts); err != nil {
		outcome = signOutUnreachable
	}
	return outcome
}
