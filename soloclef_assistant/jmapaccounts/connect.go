package jmapaccounts

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapmail"
)

// Connecting and disconnecting a mailbox: what the API's connect routes do
// with what a person typed or what the server's sign-in granted. Each
// connect signs in first and stores nothing that did not.

// ConnectAppPassword connects a mailbox with an app password: it signs in
// to the server's session with it, and only then seals and stores it. A
// *jmapmail.Failure is the server's answer in words for the person.
//
// The mailbox is stored under the address the server calls itself, which
// may not be the one typed (see sameServer); the account's ServerURL says
// which.
func (m *Mail) ConnectAppPassword(ctx context.Context, ownerID, label, serverURL, username, password string) (Account, error) {
	origin, err := jmapmail.Origin(serverURL)
	if err != nil {
		return Account{}, &jmapmail.Failure{Message: "The server's address must be an https address, such as https://mail.example.org"}
	}
	credential := jmapmail.Credential{Username: username, Password: password}
	session, err := m.Client.Session(ctx, origin, credential)
	if own, ok := m.sameServer(ctx, origin, err); ok {
		origin = own
		session, err = m.Client.Session(ctx, origin, credential)
	}
	if err != nil {
		return Account{}, err
	}
	sealed, err := m.SignIn.Seal.Close(ownerID, Secrets{Password: password})
	if err != nil {
		return Account{}, err
	}
	return m.save(ctx, ownerID, NewMailbox{Label: label, ServerURL: origin, Username: username, Auth: AuthAppPassword,
		Sealed: sealed, CanSend: session.CanSend})
}

// sameServer decides whether an app password may follow a session that
// named another origin as the server's own (jmapmail.Failure.Elsewhere),
// and returns that origin when it may.
//
// Why this way. The password is sent first to the typed address, the
// host the person chose, exactly as before; nothing is looked up ahead of
// it, so a server that answers on the typed address costs no extra request.
// Following the session's apiUrl on its word alone would send the password
// to a host the person did not type, so it is followed only when the typed
// address's own sign-in metadata, read without any credential, names the
// same origin as its issuer and that origin's metadata names itself
// (OAuth.OwnAddress): two documents of the typed server, and the other's
// own, agreeing that they are one server. Then the password goes to the
// server's own address, once, and the mailbox is stored under it. In every
// other case (no metadata, an issuer that is the typed address or yet
// another, one that does not confirm itself) the session's refusal stands
// unchanged, saying which address the server calls itself.
func (m *Mail) sameServer(ctx context.Context, typed string, sessionErr error) (string, bool) {
	var failure *jmapmail.Failure
	if !errors.As(sessionErr, &failure) || failure.Elsewhere == "" {
		return "", false
	}
	own, err := m.SignIn.OAuth.OwnAddress(ctx, typed)
	if err != nil || own == typed || own != failure.Elsewhere {
		return "", false
	}
	return own, true
}

// errNoMailScope is a grant a person narrowed on the consent page so that
// it has no mail.
var errNoMailScope = &jmapmail.Failure{Message: "The sign-in did not grant access to mail. Connect again and allow mail access."}

// ConnectGrant connects a mailbox with what the server's sign-in granted:
// it finds the session with the access token (which says whose mailbox it
// is), and stores the grant sealed. The mailbox's username is the
// server's, never anything the browser said.
func (m *Mail) ConnectGrant(ctx context.Context, ownerID, serverURL, clientID string, metadata Metadata, grant Grant) (Account, error) {
	if !grant.HasMail() {
		return Account{}, errNoMailScope
	}
	if grant.RefreshToken == "" {
		return Account{}, &jmapmail.Failure{Message: "The server's sign-in gave no refresh token, so the mailbox could not stay connected"}
	}
	origin, err := jmapmail.Origin(serverURL)
	if err != nil {
		return Account{}, err
	}
	session, err := m.Client.Session(ctx, origin, jmapmail.Credential{AccessToken: grant.AccessToken})
	if err != nil {
		return Account{}, err
	}
	if strings.TrimSpace(session.Username) == "" {
		return Account{}, &jmapmail.Failure{Message: "The server did not say whose mailbox this is"}
	}
	sealed, err := m.SignIn.Seal.Close(ownerID, Secrets{RefreshToken: grant.RefreshToken, AccessToken: grant.AccessToken, AccessExpires: grant.ExpiresAt})
	if err != nil {
		return Account{}, err
	}
	return m.save(ctx, ownerID, NewMailbox{ServerURL: origin, Username: session.Username, Auth: AuthOAuth,
		OAuthClientID: clientID, TokenEndpoint: metadata.TokenEndpoint, RevocationEndpoint: metadata.RevocationEndpoint,
		Sealed: sealed, CanSend: session.CanSend})
}

// save stores a connected mailbox and ends the grant it replaced, if it
// replaced one: a reconnect leaves no second live grant at the server.
func (m *Mail) save(ctx context.Context, ownerID string, mailbox NewMailbox) (Account, error) {
	account, previous, err := m.SignIn.Rows.Save(ctx, ownerID, mailbox)
	if err != nil {
		return Account{}, err
	}
	if previous != nil && !m.SignIn.Revoke(context.WithoutCancel(ctx), *previous) && previous.row.Auth == AuthOAuth {
		m.SignIn.Logger.LogAttrs(ctx, slog.LevelWarn, "a replaced JMAP grant could not be revoked at its server", slog.String("user_id", ownerID))
	}
	return account, nil
}

// Disconnect removes a mailbox, its drafts with it, and ends its grant at
// the server. revoked says the server confirmed; false for an app
// password, which the person deletes in Stalwart themselves, and for a
// server that could not be reached (the row is gone either way: a
// mailbox a person removed is never read again).
func (m *Mail) Disconnect(ctx context.Context, ownerID, accountID string) (bool, error) {
	removed, err := m.SignIn.Rows.Remove(ctx, ownerID, accountID)
	if err != nil {
		return false, err
	}
	// A cached session is keyed by a hash of its credential, which the
	// removed row no longer gives; the cache is dropped whole instead, which
	// costs every mailbox one session lookup.
	m.mu.Lock()
	m.sessions = nil
	m.mu.Unlock()
	return m.SignIn.Revoke(context.WithoutCancel(ctx), removed), nil
}

// IsNoAccount reports whether err is a mailbox that is not the owner's.
func IsNoAccount(err error) bool { return errors.Is(err, ErrNoAccount) }
