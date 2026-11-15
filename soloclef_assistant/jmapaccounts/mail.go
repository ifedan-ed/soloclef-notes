package jmapaccounts

import (
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"sync"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapmail"
)

// sessionLifetime is how long a found session is reused. Finding one is a
// round trip (two, with Stalwart's redirect) before every call; a session
// changes when the server's configuration does, which is rare, and a call
// the server refuses drops it at once.
const sessionLifetime = 5 * time.Minute

// mostSessions bounds the cache; past it, it starts again empty.
const mostSessions = 2000

// Mail is one of an account's JMAP mailboxes opened for use: signed in,
// its session found.
type Mail struct {
	SignIn SignIn
	Client jmapmail.Client

	mu       sync.Mutex
	sessions map[string]cachedSession
}

type cachedSession struct {
	session jmapmail.Session
	until   time.Time
}

// Check reports what is missing.
func (m *Mail) Check() error {
	if err := m.SignIn.Check(); err != nil {
		return err
	}
	return m.Client.Check()
}

// Opened is a mailbox ready for jmapmail's calls.
type Opened struct {
	Account    Account
	Session    jmapmail.Session
	Credential jmapmail.Credential
}

// cacheKey ties a session to the credential it was found with, so a new
// token or password never reuses a session found with the old one.
func cacheKey(accountID string, credential jmapmail.Credential) string {
	digest := sha256.Sum256([]byte(accountID + "\x00" + credential.Username + "\x00" + credential.Password + "\x00" + credential.AccessToken))
	return string(digest[:])
}

// Open signs in to one of the owner's mailboxes and finds its session.
func (m *Mail) Open(ctx context.Context, ownerID, accountID string) (Opened, error) {
	account, credential, err := m.SignIn.Credential(ctx, ownerID, accountID)
	if err != nil {
		return Opened{}, err
	}
	key := cacheKey(account.ID, credential)
	now := m.SignIn.Now()
	m.mu.Lock()
	held, cached := m.sessions[key]
	m.mu.Unlock()
	if cached && now.Before(held.until) {
		return Opened{Account: account, Session: held.session, Credential: credential}, nil
	}
	session, err := m.Client.Session(ctx, account.ServerURL, credential)
	if err != nil {
		m.refused(ctx, ownerID, account.ID, err)
		return Opened{}, err
	}
	m.mu.Lock()
	if m.sessions == nil || len(m.sessions) >= mostSessions {
		m.sessions = map[string]cachedSession{}
	}
	m.sessions[key] = cachedSession{session: session, until: now.Add(sessionLifetime)}
	m.mu.Unlock()
	return Opened{Account: account, Session: session, Credential: credential}, nil
}

// Use opens the mailbox and runs work with it. A server that refuses the
// credential in the middle of work marks the mailbox to be reconnected and
// forgets its session, so the next call does not try it again blind.
func (m *Mail) Use(ctx context.Context, ownerID, accountID string, work func(Opened) error) error {
	opened, err := m.Open(ctx, ownerID, accountID)
	if err != nil {
		return err
	}
	err = work(opened)
	if err != nil {
		m.refused(ctx, ownerID, accountID, err)
		if jmapmail.IsSignInRefused(err) {
			m.mu.Lock()
			delete(m.sessions, cacheKey(accountID, opened.Credential))
			m.mu.Unlock()
		}
	}
	return err
}

// refused marks a mailbox whose credential the server refused.
func (m *Mail) refused(ctx context.Context, ownerID, accountID string, err error) {
	if !jmapmail.IsSignInRefused(err) {
		return
	}
	if markErr := m.SignIn.Rows.MarkReconnect(ctx, ownerID, accountID); markErr != nil {
		m.SignIn.Logger.LogAttrs(ctx, slog.LevelError, "a JMAP mailbox could not be marked to reconnect",
			slog.String("user_id", ownerID), slog.String("error", markErr.Error()))
	}
}

// Accounts is the owner's mailboxes.
func (m *Mail) Accounts(ctx context.Context, ownerID string) ([]Account, error) {
	return m.SignIn.Rows.Accounts(ctx, ownerID)
}

// Probe is what a test of a mailbox found.
type Probe struct {
	Mailboxes int
	CanSend   bool
}

// Test signs in to a mailbox afresh (no cached session), counts its
// folders and records whether it may send.
func (m *Mail) Test(ctx context.Context, ownerID, accountID string) (Probe, error) {
	account, credential, err := m.SignIn.Credential(ctx, ownerID, accountID)
	if err != nil {
		return Probe{}, err
	}
	session, err := m.Client.Session(ctx, account.ServerURL, credential)
	if err != nil {
		m.refused(ctx, ownerID, accountID, err)
		return Probe{}, err
	}
	folders, err := m.Client.Mailboxes(ctx, session, credential)
	if err != nil {
		m.refused(ctx, ownerID, accountID, err)
		return Probe{}, err
	}
	if session.CanSend != account.CanSend {
		if err := m.SignIn.Rows.SetCanSend(ctx, ownerID, accountID, session.CanSend); err != nil {
			return Probe{}, err
		}
	}
	return Probe{Mailboxes: len(folders), CanSend: session.CanSend}, nil
}

// ErrCannotSend is a mailbox whose sign-in may not send.
var ErrCannotSend = &jmapmail.Failure{Message: "This mailbox's sign-in may not send mail (for an app password, check its permissions in Stalwart); it can only be read"}

// Send sends one confirmed message from one of the owner's mailboxes. Only
// the send guard calls it (soloclef_assistant/maildrafts).
func (m *Mail) Send(ctx context.Context, ownerID, accountID string, message jmapmail.Outgoing) (string, error) {
	var sentID string
	err := m.Use(ctx, ownerID, accountID, func(opened Opened) error {
		if !opened.Session.CanSend {
			return ErrCannotSend
		}
		var err error
		sentID, err = m.Client.Send(ctx, opened.Session, opened.Credential, message)
		return err
	})
	if errors.Is(err, ErrNoAccount) {
		return "", &jmapmail.Failure{Message: "The mailbox this draft is from is no longer connected"}
	}
	return sentID, err
}
