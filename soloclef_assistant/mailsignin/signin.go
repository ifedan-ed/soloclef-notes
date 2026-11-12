package mailsignin

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/imapmail"
)

// What a person is told when a Microsoft mailbox cannot be signed in to.
const (
	ReconnectMessage = "Microsoft no longer accepts this mailbox's sign-in. " +
		"Reconnect it in Connectors, Email (IMAP)."
	UnreachableMessage = "Couldn't reach Microsoft to sign in to this mailbox. Try again shortly."
	// This deployment's app registration was refused, not the mailbox.
	ServerRefusedMessage = "Microsoft refused this server's app credentials. " +
		"The administrator needs to renew the Microsoft client secret."
	NotSetUpMessage = "Microsoft accounts aren't set up on this server."
)

// refreshMargin: a stored access token with less than this left is
// refreshed before use, so an IMAP session never starts on a token about
// to lapse (they last about an hour).
const refreshMargin = 5 * time.Minute

// serverErrors are the OAuth errors about this deployment's app
// registration, not about the mailbox.
var serverErrors = map[string]bool{"invalid_client": true, "unauthorized_client": true}

// Merger writes keys into one of an account's Microsoft mailbox records,
// in one statement that names the owner, and reports whether there was
// one: a refreshed token, or the flag that says to reconnect.
type Merger interface {
	Merge(ctx context.Context, ownerID, accountID string, fields []byte) (bool, error)
}

// SignIn turns a stored mailbox into IMAP settings that work.
//
// For a Microsoft mailbox, each use checks the stored token and, when it
// is about to lapse, trades the refresh token for a new pair and stores it
// sealed. When Microsoft refuses the grant itself the record is marked
// `needs_reconnect`, and every later use fails at once with the same
// sentence, without asking Microsoft again, until the owner reconnects.
type SignIn struct {
	// SecretKey is the deployment's secret key, which every stored
	// credential is sealed with.
	SecretKey string
	// Microsoft is the token endpoint of the deployment's app
	// registration, or nil for a deployment that has not set Microsoft
	// mail up: its Microsoft mailboxes then answer that it is not set up.
	Microsoft *Endpoint
	Rows      Merger
	Now       func() time.Time
	Logger    *slog.Logger
}

// Check reports what is missing. Called once at start-up.
func (s SignIn) Check() error {
	if s.SecretKey == "" || s.Rows == nil || s.Now == nil || s.Logger == nil {
		return errors.New("mailsignin: SecretKey, Rows, Now and Logger are all required")
	}
	return nil
}

// refused is a failure worded for the person at the mailbox's row.
func refused(message string) error { return &imapmail.ConnectionError{Message: message} }

// Settings is the IMAP settings for any of ownerID's stored mailboxes. A
// *imapmail.ConnectionError is a mailbox that cannot be signed in to, in
// words for its owner; any other error is a fault.
func (s SignIn) Settings(ctx context.Context, ownerID string, stored []byte) (imapmail.Settings, error) {
	var held Record
	if json.Unmarshal(stored, &held) != nil {
		return imapmail.Settings{}, errors.New("mailsignin: the record is not a mailbox")
	}
	if held.IsMicrosoft() {
		return s.microsoftMailbox(ctx, ownerID, held)
	}
	opened, readable := password(held, s.SecretKey)
	if !readable {
		return imapmail.Settings{}, refused(PasswordUnreadable)
	}
	var port int
	_ = json.Unmarshal(held["port"], &port)
	// A record written before the choice existed means TLS.
	useTLS := true
	if raw, stated := held["use_tls"]; stated {
		_ = json.Unmarshal(raw, &useTLS)
	}
	return imapmail.Settings{Host: held.Text("host"), Port: port, UseTLS: useTLS, Username: held.Text("username"), Password: opened}, nil
}

// MicrosoftMailbox is Settings for a record that must be a Microsoft
// mailbox: any other record is a fault, not a refusal.
func (s SignIn) MicrosoftMailbox(ctx context.Context, ownerID string, stored []byte) (imapmail.Settings, error) {
	var held Record
	if json.Unmarshal(stored, &held) != nil || !held.IsMicrosoft() {
		return imapmail.Settings{}, errors.New("mailsignin: the record is not a Microsoft mailbox")
	}
	return s.microsoftMailbox(ctx, ownerID, held)
}

func (s SignIn) microsoftMailbox(ctx context.Context, ownerID string, held Record) (imapmail.Settings, error) {
	if held.Truth(ReconnectKey) {
		return imapmail.Settings{}, refused(ReconnectMessage)
	}
	if s.Microsoft == nil {
		return imapmail.Settings{}, refused(NotSetUpMessage)
	}
	accountID := held.Text("id")
	access, expires, refresh, err := StoredTokens(held, s.SecretKey)
	if err != nil {
		s.flag(ctx, ownerID, accountID)
		return imapmail.Settings{}, refused(ReconnectMessage)
	}

	now := s.Now()
	if access == "" || expires.Sub(now) < refreshMargin {
		fresh, err := s.Microsoft.Refresh(ctx, refresh, now)
		if _, dead := Revoked(err); dead {
			s.flag(ctx, ownerID, accountID)
			return imapmail.Settings{}, refused(ReconnectMessage)
		}
		var endpoint *EndpointError
		if errors.As(err, &endpoint) {
			s.Logger.LogAttrs(ctx, slog.LevelWarn, "refreshing a Microsoft mailbox's token failed", slog.String("error", err.Error()))
			if serverErrors[endpoint.Code] {
				return imapmail.Settings{}, refused(ServerRefusedMessage)
			}
			return imapmail.Settings{}, refused(UnreachableMessage)
		}
		if err != nil {
			return imapmail.Settings{}, err
		}
		// Stored before it is used: Microsoft may have rotated the refresh
		// token, and a row left holding the old one is a mailbox that
		// stops working when that one lapses.
		fields, err := TokenFields(fresh, s.SecretKey, now)
		var encoded []byte
		if err == nil {
			encoded, err = json.Marshal(fields)
		}
		if err == nil {
			// Whether or not the request still waits: the refresh is spent.
			_, err = s.Rows.Merge(context.WithoutCancel(ctx), ownerID, accountID, encoded)
		}
		if err != nil {
			return imapmail.Settings{}, err
		}
		access = fresh.AccessToken
	}
	return imapmail.Settings{Host: IMAPHost, Port: IMAPPort, UseTLS: true,
		Username: held.Text("username"), AccessToken: access}, nil
}

// flag marks the mailbox as needing a reconnect. A flag that cannot be
// written costs one more question to Microsoft next time, so it is logged
// and not returned.
func (s SignIn) flag(ctx context.Context, ownerID, accountID string) {
	if _, err := s.Rows.Merge(context.WithoutCancel(ctx), ownerID, accountID, []byte(`{"`+ReconnectKey+`": true}`)); err != nil {
		s.Logger.LogAttrs(ctx, slog.LevelWarn, "a Microsoft mailbox could not be flagged for reconnecting", slog.String("error", err.Error()))
	}
}
