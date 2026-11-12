package nextcloudconnect

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/valkey-io/valkey-go"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/storedtoken"
)

// flowLifetime is how long a started sign-in is kept: as long as Nextcloud
// keeps its own half.
const flowLifetime = 20 * time.Minute

// flowKeyPrefix is where pending sign-ins live in Valkey, the Python
// tier's key, so a sign-in started on one tier can be finished on the
// other while both run.
const flowKeyPrefix = "soloclef:nextcloud:login-flow:"

// pendingLogin is a sign-in the server is still polling for.
type pendingLogin struct {
	// UserID is the account that started it, and the only one that may
	// poll or cancel it.
	UserID       string `json:"user_id"`
	PollEndpoint string `json:"poll_endpoint"`
	// PollToken is sealed in the stored record and opened by get.
	PollToken string `json:"poll_token"`
	LoginURL  string `json:"login_url"`
}

// KeyValue is the three commands pending sign-ins need of Valkey.
type KeyValue interface {
	Set(ctx context.Context, key, value string, lifetime time.Duration) error
	// Get returns found false for a key that is absent or has expired.
	Get(ctx context.Context, key string) (value string, found bool, err error)
	Delete(ctx context.Context, key string) error
}

// Pending keeps started sign-ins until they finish or expire. The poll
// token is sealed under the deployment's secret key before it leaves the
// process, so a reader of Valkey alone cannot finish somebody's sign-in.
type Pending struct {
	Store     KeyValue
	SecretKey string
	Now       func() time.Time
}

// newFlowID is the random id the browser holds: 24 bytes, URL-safe.
func newFlowID() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (p Pending) add(ctx context.Context, flowID string, login pendingLogin) error {
	sealed, err := storedtoken.Seal(p.SecretKey, login.PollToken, p.Now(), rand.Reader)
	if err != nil {
		return err
	}
	login.PollToken = sealed
	record, err := json.Marshal(login)
	if err != nil {
		return err
	}
	return p.Store.Set(ctx, flowKeyPrefix+flowID, string(record), flowLifetime)
}

// get returns the sign-in, or nil when there is none: never started,
// expired, finished, or kept in a form this key cannot open.
func (p Pending) get(ctx context.Context, flowID string) (*pendingLogin, error) {
	record, found, err := p.Store.Get(ctx, flowKeyPrefix+flowID)
	if err != nil || !found {
		return nil, err
	}
	var login pendingLogin
	if json.Unmarshal([]byte(record), &login) != nil {
		return nil, nil
	}
	token, err := storedtoken.Open(p.SecretKey, login.PollToken)
	if err != nil {
		return nil, nil
	}
	login.PollToken = token
	return &login, nil
}

func (p Pending) remove(ctx context.Context, flowID string) error {
	return p.Store.Delete(ctx, flowKeyPrefix+flowID)
}

// Valkey is KeyValue over the deployment's Valkey. It connects on first
// use, like the tier's other Valkey users: a Valkey that is down when the
// process starts must not stop it serving everything else.
type Valkey struct {
	option valkey.ClientOption

	mu     sync.Mutex
	client valkey.Client
}

// NewValkey reads the address. It does not connect.
func NewValkey(address string) (*Valkey, error) {
	option, err := valkey.ParseURL(address)
	if err != nil {
		return nil, fmt.Errorf("nextcloudconnect: valkey address: %w", err)
	}
	// A pending sign-in is read to be acted on once; a client-side copy
	// could outlive its removal.
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
		return nil, fmt.Errorf("nextcloudconnect: valkey: %w", err)
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

func (v *Valkey) Set(ctx context.Context, key, value string, lifetime time.Duration) error {
	client, err := v.connection()
	if err != nil {
		return err
	}
	return client.Do(ctx, client.B().Set().Key(key).Value(value).Ex(lifetime).Build()).Error()
}

func (v *Valkey) Get(ctx context.Context, key string) (string, bool, error) {
	client, err := v.connection()
	if err != nil {
		return "", false, err
	}
	value, err := client.Do(ctx, client.B().Get().Key(key).Build()).ToString()
	if valkey.IsValkeyNil(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (v *Valkey) Delete(ctx context.Context, key string) error {
	client, err := v.connection()
	if err != nil {
		return err
	}
	return client.Do(ctx, client.B().Del().Key(key).Build()).Error()
}
