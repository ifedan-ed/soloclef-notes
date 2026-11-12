package signin

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// The Argon2id settings every new hash is made with. They are argon2-cffi's
// defaults, which is what the Python tier's password library used, so a hash
// either tier made is the same string format and verifies in the other.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// ErrUnreadableHash is a stored hash in a format this tier does not verify.
// It is refused rather than treated as a wrong password, so that an account
// nobody can sign in to is a logged fault and not a mystery.
var ErrUnreadableHash = errors.New("the stored password hash is not an argon2id hash")

// Hasher computes and checks password hashes, a few at a time.
//
// One Argon2id computation takes 64 MiB for a few tens of milliseconds. A
// burst of sign-ins with no bound would hold that much memory per request
// at once; the slots make the burst wait instead.
type Hasher struct {
	slots chan struct{}
}

// NewHasher allows `concurrent` computations at once.
func NewHasher(concurrent int) *Hasher {
	if concurrent < 1 {
		concurrent = 1
	}
	return &Hasher{slots: make(chan struct{}, concurrent)}
}

// Hash is a new PHC-format hash of the password, with a fresh salt.
func (h *Hasher) Hash(ctx context.Context, password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return encodeHash(argonParameters{time: argonTime, memory: argonMemory, threads: argonThreads}, salt, key), nil
}

// Verify reports whether the password matches the stored hash, and whether
// the hash was made with settings weaker than today's and should be
// replaced while the plaintext is at hand.
func (h *Hasher) Verify(ctx context.Context, password, stored string) (matches, stale bool, err error) {
	parameters, salt, want, err := decodeHash(stored)
	if err != nil {
		return false, false, err
	}
	if err := h.acquire(ctx); err != nil {
		return false, false, err
	}
	defer h.release()
	got := argon2.IDKey([]byte(password), salt, parameters.time, parameters.memory, parameters.threads, uint32(len(want)))
	matches = subtle.ConstantTimeCompare(got, want) == 1
	stale = parameters.time < argonTime || parameters.memory < argonMemory || len(want) < argonKeyLen
	return matches, stale, nil
}

// dummyHash is verified against when the address has no account, or the
// account has no password, so that a refusal takes as long either way.
var dummyHash = encodeHash(argonParameters{time: argonTime, memory: argonMemory, threads: argonThreads},
	make([]byte, argonSaltLen), make([]byte, argonKeyLen))

// SpendTime does the work of a verification and discards it.
func (h *Hasher) SpendTime(ctx context.Context, password string) {
	_, _, _ = h.Verify(ctx, password, dummyHash)
}

func (h *Hasher) acquire(ctx context.Context) error {
	select {
	case h.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Hasher) release() { <-h.slots }

type argonParameters struct {
	time    uint32
	memory  uint32
	threads uint8
}

// encodeHash is the PHC string argon2-cffi writes:
// $argon2id$v=19$m=65536,t=3,p=4$<salt>$<key>, base64 without padding.
func encodeHash(p argonParameters, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version,
		p.memory, p.time, p.threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

func decodeHash(stored string) (argonParameters, []byte, []byte, error) {
	var p argonParameters
	parts := strings.Split(stored, "$")
	// "", "argon2id", "v=19", "m=…,t=…,p=…", salt, key
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, nil, nil, ErrUnreadableHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return p, nil, nil, ErrUnreadableHash
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil ||
		p.memory == 0 || p.time == 0 || p.threads == 0 {
		return p, nil, nil, ErrUnreadableHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return p, nil, nil, ErrUnreadableHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return p, nil, nil, ErrUnreadableHash
	}
	return p, salt, key, nil
}
