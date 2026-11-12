package threads

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/chat/turns"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/routeanalytics"
)

// Tool is one entry of the composer's tool picker.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// EnabledByDefault says the tool is on until a person switches it off.
	EnabledByDefault bool `json:"enabled_by_default"`
}

// Turns is the part of the running-turn registry a delete needs:
// *turns.Registry, the one the turn routes use.
type Turns interface {
	// Begin claims the thread, or answers turns.ErrBusy.
	Begin(parent context.Context, thread string) (context.Context, *turns.Turn, error)
	// Stop asks the turn running on the thread to stop.
	Stop(ctx context.Context, thread string) (bool, error)
}

// Service is what the routes run on. Every field but Events and
// FrontendURL is required.
type Service struct {
	Store Store

	// Turns is the registry the turn routes claim a thread in. Threads are
	// named there by their id in decimal, which is what package chat's
	// routes pass it; the two must agree, or a delete would hold a lease
	// no turn ever asks for.
	Turns Turns

	// Forget removes what the chat engine keeps for a thread outside the
	// thread's own rows: its saved conversation, and anything else that is
	// kept per thread. It is called while the thread's lease is held and
	// before the rows are deleted, and must succeed for a thread it has
	// nothing for.
	Forget func(ctx context.Context, threadID int64) error

	// Tools is what GET /agent/tools answers: the chat's own tools as the
	// tool picker names them. It is handed in, not written here, so that
	// the list is made where the tools are and cannot name one the chat
	// does not have.
	Tools []Tool

	// FrontendURL is the web app's address, which a public link's address
	// starts with. Empty is valid in this tier, as it is for the
	// account's list of links (internal/publiclinks): the address is
	// then a path.
	FrontendURL string

	// Events records "chat_created". The zero value records nothing, which
	// is a deployment with no analytics key.
	Events routeanalytics.Capture

	// StopWait is how long a delete waits for a running turn to stop and
	// free the thread before telling the caller to try again.
	StopWait time.Duration

	Logger *slog.Logger
	Now    func() time.Time
}

// Check reports what is missing. Called once at start-up.
func (s *Service) Check() error {
	switch {
	case s.Store.Pool == nil:
		return errors.New("threads: the database is not set")
	case s.Turns == nil:
		return errors.New("threads: the turn registry is not set")
	case s.Forget == nil:
		return errors.New("threads: Forget is not set")
	case s.Tools == nil:
		return errors.New("threads: the tool list is not set")
	case s.StopWait <= 0:
		return fmt.Errorf("threads: StopWait must be positive, got %s", s.StopWait)
	case s.Logger == nil:
		return errors.New("threads: the logger is not set")
	case s.Now == nil:
		return errors.New("threads: Now is not set")
	}
	seen := map[string]bool{}
	for _, tool := range s.Tools {
		if tool.Name == "" || tool.Description == "" {
			return fmt.Errorf("threads: a tool in the list has no name or no description: %+v", tool)
		}
		if seen[tool.Name] {
			return fmt.Errorf("threads: the tool %q is listed twice", tool.Name)
		}
		seen[tool.Name] = true
	}
	return nil
}

// newToken is a public link's secret: 48 random bytes, as 64 characters
// that are safe in an address. It is the only thing between a stranger and
// the conversation, so it comes from the system's generator and a failure
// to read that is a failure, never a weaker token.
func newToken() (string, error) {
	raw := make([]byte, 48)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("threads: no randomness for a link's token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// errStillStopping says a delete could not have the thread in time.
var errStillStopping = errors.New("threads: the thread's turn is still stopping")

// stopPoll is how often a delete asks again for a thread whose turn was
// told to stop.
const stopPoll = 40 * time.Millisecond

// hold claims the thread so that no turn runs on it, stopping the one that
// is. The caller must call release.
//
// This is what makes a delete safe beside a running turn. A turn writes its
// saved conversation as it goes; a delete that only removed rows could be
// followed by one more write from a turn still winding down, and that write
// would be a saved conversation for a thread that no longer exists. Holding
// the lease the turn itself must hold means the delete runs when no turn
// does, and a turn cannot start on the thread until the delete is done (and
// then finds no thread).
func (s *Service) hold(ctx context.Context, threadID int64) (release func(), err error) {
	key := itoa(threadID)
	deadline := time.Now().Add(s.StopWait)
	asked := false
	for {
		_, lease, err := s.Turns.Begin(ctx, key)
		if err == nil {
			return lease.End, nil
		}
		if !errors.Is(err, turns.ErrBusy) {
			return nil, err
		}
		if !asked {
			// Deleting a chat that is answering is the person saying they
			// do not want the answer.
			if _, err := s.Turns.Stop(ctx, key); err != nil {
				return nil, err
			}
			asked = true
		}
		if time.Now().After(deadline) {
			return nil, errStillStopping
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(stopPoll):
		}
	}
}
