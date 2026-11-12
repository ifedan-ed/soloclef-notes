package apphandoff

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/valkey-io/valkey-go"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/appreturn"
)

// The three secrets and how long each is good for. Each is spent by its
// first use, right or wrong.
const (
	// codeLifetime is how long the browser sheet has to open the code's
	// page. The app opens it at once; a minute covers a slow phone.
	codeLifetime = 60 * time.Second
	// ticketLifetime is how long the Picker's page has for its one import:
	// long enough to look through a Drive and pick.
	ticketLifetime = 10 * time.Minute
	// completionLifetime is how long the app has to confirm what a connect
	// held. It does so as soon as the sheet comes back.
	completionLifetime = 5 * time.Minute
)

// kind is one of the three secrets: its key prefix in Valkey and its
// lifetime. The prefixes are the Python tier's, so a deploy that switches
// tiers mid-flow finds nothing it cannot read as nothing.
type kind struct {
	prefix   string
	lifetime time.Duration
}

var (
	codes       = kind{"soloclef:app_handoff:code", codeLifetime}
	tickets     = kind{"soloclef:app_handoff:ticket", ticketLifetime}
	completions = kind{"soloclef:app_handoff:completion", completionLifetime}
)

// grant is what a secret stands for: whose flow, which one, and what it
// needs to go on. Every id in it was checked as the account's own when the
// secret was made, and is read with the account named again when used.
type grant struct {
	Purpose string `json:"purpose"`
	UserID  string `json:"user_id"`
	// NotebookID is the Drive Picker's: where the picked files go; nil, the
	// library only.
	NotebookID *int64 `json:"notebook_id,omitempty"`
	// AccountID is Microsoft mail's: the mailbox being reconnected.
	AccountID string `json:"account_id,omitempty"`
	// ConnectorID is a Picker ticket's: the Drive connection it imports
	// through.
	ConnectorID int64 `json:"connector_id,omitempty"`
	// Challenge is a connect's: the app's S256 challenge, carried from the
	// code to the completion.
	Challenge string `json:"challenge,omitempty"`
	// Sealed is a completion's: what the provider granted, sealed by the
	// provider with the deployment's secret key.
	Sealed string `json:"sealed,omitempty"`
}

// KeyValue is the part of Valkey the secrets need.
type KeyValue interface {
	// SetNew stores value under key for lifetime, only if key is unused,
	// and reports whether it did.
	SetNew(ctx context.Context, key, value string, lifetime time.Duration) (bool, error)
	// Take reads and removes key in one step, so a secret is spent once
	// even when two replicas are asked at the same moment.
	Take(ctx context.Context, key string) (string, bool, error)
}

// Secrets issues and spends the three secrets.
type Secrets struct {
	Store KeyValue
}

// errCollision is SetNew refusing a fresh key: 32 random bytes met an
// existing one, which does not happen; refused rather than overwritten.
var errCollision = errors.New("apphandoff: a new secret met an existing one")

// key is where a secret is kept: under its SHA-256, so Valkey holds
// nothing spendable.
func (k kind) key(secret string) string {
	digest := sha256.Sum256([]byte(secret))
	return k.prefix + ":" + hex.EncodeToString(digest[:])
}

func newSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s Secrets) issue(ctx context.Context, k kind, g grant) (string, error) {
	secret, err := newSecret()
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(g)
	if err != nil {
		return "", err
	}
	stored, err := s.Store.SetNew(ctx, k.key(secret), string(payload), k.lifetime)
	if err != nil {
		return "", err
	}
	if !stored {
		return "", errCollision
	}
	return secret, nil
}

// maxSecret bounds what is hashed and looked up: every secret issued here
// is 43 characters.
const maxSecret = 128

// redeem is the grant behind secret, once: nil when it was never issued,
// was spent or ran out (Valkey's expiry), or does not read as a grant.
func (s Secrets) redeem(ctx context.Context, k kind, secret string) (*grant, error) {
	if secret == "" || len(secret) > maxSecret {
		return nil, nil
	}
	payload, found, err := s.Store.Take(ctx, k.key(secret))
	if err != nil || !found {
		return nil, err
	}
	var g grant
	if json.Unmarshal([]byte(payload), &g) != nil || g.Purpose == "" || g.UserID == "" {
		return nil, nil
	}
	return &g, nil
}

// Holder is what a provider's callback holds an app's grant with:
// completions of purpose.
func (s Secrets) Holder(purpose string) appreturn.Holder { return holder{secrets: s, purpose: purpose} }

type holder struct {
	secrets Secrets
	purpose string
}

func (h holder) Hold(ctx context.Context, userID, challenge, sealed string) (string, error) {
	if userID == "" || challenge == "" || sealed == "" {
		return "", errors.New("apphandoff: a held grant needs its account, its challenge and its grant")
	}
	return h.secrets.issue(ctx, completions, grant{Purpose: h.purpose, UserID: userID, Challenge: challenge, Sealed: sealed})
}

// Valkey is KeyValue over a Valkey server, connected on first use so a
// Valkey that is down does not stop the process serving what needs none.
type Valkey struct {
	option valkey.ClientOption
	mu     sync.Mutex
	client valkey.Client
}

// NewValkey reads the address; nothing connects yet.
func NewValkey(address string) (*Valkey, error) {
	option, err := valkey.ParseURL(address)
	if err != nil {
		return nil, fmt.Errorf("apphandoff: valkey address: %w", err)
	}
	// A secret is read to be spent once; a client-side copy could outlive
	// its removal.
	option.DisableCache = true
	return &Valkey{option: option}, nil
}

func (v *Valkey) connection() (valkey.Client, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.client != nil {
		return v.client, nil
	}
	client, err := valkey.NewClient(v.option)
	if err != nil {
		return nil, fmt.Errorf("apphandoff: valkey: %w", err)
	}
	v.client = client
	return client, nil
}

// Close releases the connections.
func (v *Valkey) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.client != nil {
		v.client.Close()
		v.client = nil
	}
}

func (v *Valkey) SetNew(ctx context.Context, key, value string, lifetime time.Duration) (bool, error) {
	client, err := v.connection()
	if err != nil {
		return false, err
	}
	err = client.Do(ctx, client.B().Set().Key(key).Value(value).Nx().Ex(lifetime).Build()).Error()
	if valkey.IsValkeyNil(err) {
		return false, nil
	}
	return err == nil, err
}

func (v *Valkey) Take(ctx context.Context, key string) (string, bool, error) {
	client, err := v.connection()
	if err != nil {
		return "", false, err
	}
	value, err := client.Do(ctx, client.B().Getdel().Key(key).Build()).ToString()
	if valkey.IsValkeyNil(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}
