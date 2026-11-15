package jmapaccounts

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapmail"
)

// What a person, or the model on their behalf, is told about a mailbox
// that cannot be signed in to.
const (
	ReconnectMessage = "This mailbox must be connected again in Connectors, Stalwart (JMAP), before it can be read"
	unreadableSecret = "This mailbox's stored credentials can no longer be read; connect it again in Connectors, Stalwart (JMAP)"
)

// SignIn turns a stored mailbox into a credential the server accepts.
type SignIn struct {
	Rows   Rows
	Seal   Seal
	OAuth  OAuth
	Now    func() time.Time
	Logger *slog.Logger
}

// Check reports what is missing.
func (s SignIn) Check() error {
	if s.Rows == nil || !s.Seal.Ready() || s.Now == nil || s.Logger == nil {
		return errors.New("jmapaccounts: SignIn needs Rows, a Seal made by NewSeal, Now and Logger")
	}
	return s.OAuth.Check()
}

// Credential is a working credential for one of the owner's mailboxes, and
// the mailbox. ErrNoAccount is a mailbox that is not the owner's; a
// *jmapmail.Failure with SignIn set is one to be reconnected, worded for
// its owner.
func (s SignIn) Credential(ctx context.Context, ownerID, accountID string) (Account, jmapmail.Credential, error) {
	row, err := s.Rows.account(ctx, ownerID, accountID)
	if err != nil {
		return Account{}, jmapmail.Credential{}, err
	}
	if row.NeedsReconnect {
		return row.Account, jmapmail.Credential{}, &jmapmail.Failure{Message: ReconnectMessage, SignIn: true}
	}
	secrets, err := s.Seal.open(ownerID, row.Sealed)
	if err != nil {
		return row.Account, jmapmail.Credential{}, &jmapmail.Failure{Message: unreadableSecret, SignIn: true}
	}
	if row.Auth == AuthAppPassword {
		return row.Account, jmapmail.Credential{Username: row.Username, Password: secrets.Password}, nil
	}
	if secrets.AccessToken != "" && s.Now().Add(refreshMargin).Before(secrets.AccessExpires) {
		return row.Account, jmapmail.Credential{AccessToken: secrets.AccessToken}, nil
	}
	return s.renewed(ctx, ownerID, accountID)
}

// renewed refreshes the mailbox's token under its row's lock. The lock is
// what makes a refresh safe: whoever waited on it finds the token the
// first refresh stored and does not present the refresh token that refresh
// replaced, which Stalwart answers by deleting the whole grant.
func (s SignIn) renewed(ctx context.Context, ownerID, accountID string) (Account, jmapmail.Credential, error) {
	var fresh Secrets
	refused := false
	row, err := s.Rows.refreshing(ctx, ownerID, accountID, func(locked stored) (string, error) {
		current, err := s.Seal.open(ownerID, locked.Sealed)
		if err != nil {
			return "", err
		}
		now := s.Now()
		if current.AccessToken != "" && now.Add(refreshMargin).Before(current.AccessExpires) {
			// Another refresh finished while this one waited.
			fresh = current
			return "", nil
		}
		if current.RefreshToken == "" || locked.TokenEndpoint == "" {
			refused = true
			return "", nil
		}
		// The refresh is made while the lock is held and survives the
		// caller giving up: once the server has replaced the refresh token,
		// the new one must be stored or the grant is lost.
		grant, err := s.OAuth.refresh(context.WithoutCancel(ctx), locked.TokenEndpoint, locked.OAuthClientID, current.RefreshToken, now)
		if errors.Is(err, ErrGrantRefused) {
			refused = true
			return "", nil
		}
		if err != nil {
			return "", err
		}
		fresh = Secrets{RefreshToken: grant.RefreshToken, AccessToken: grant.AccessToken, AccessExpires: grant.ExpiresAt}
		if fresh.RefreshToken == "" {
			// A server that does not rotate keeps the one it has.
			fresh.RefreshToken = current.RefreshToken
		}
		return s.Seal.Close(ownerID, fresh)
	})
	switch {
	case errors.Is(err, ErrNoAccount):
		return Account{}, jmapmail.Credential{}, err
	case errors.Is(err, errUnreadable):
		return row.Account, jmapmail.Credential{}, &jmapmail.Failure{Message: unreadableSecret, SignIn: true}
	case err != nil:
		var said *Refused
		if errors.As(err, &said) {
			return row.Account, jmapmail.Credential{}, &jmapmail.Failure{Message: said.Message}
		}
		return row.Account, jmapmail.Credential{}, err
	case refused:
		if markErr := s.Rows.MarkReconnect(ctx, ownerID, accountID); markErr != nil {
			s.Logger.LogAttrs(ctx, slog.LevelError, "a JMAP mailbox could not be marked to reconnect",
				slog.String("user_id", ownerID), slog.String("error", markErr.Error()))
		}
		s.Logger.LogAttrs(ctx, slog.LevelInfo, "a JMAP mailbox's grant was refused; it must be reconnected", slog.String("user_id", ownerID))
		return row.Account, jmapmail.Credential{}, &jmapmail.Failure{Message: ReconnectMessage, SignIn: true}
	}
	return row.Account, jmapmail.Credential{AccessToken: fresh.AccessToken}, nil
}

// Revoke ends a removed or replaced mailbox's grant at its server, and
// reports whether the server confirmed. An app password cannot be revoked
// from here (that takes the account's security scope, which this app never
// asks for): its owner deletes it in Stalwart, and false says so.
func (s SignIn) Revoke(ctx context.Context, removed Removed) bool {
	if removed.row.Auth != AuthOAuth {
		return false
	}
	secrets, err := s.Seal.open(removed.OwnerID, removed.row.Sealed)
	if err != nil {
		return false
	}
	return s.OAuth.Revoke(ctx, removed.row.RevocationEndpoint, removed.row.OAuthClientID, secrets.RefreshToken)
}
