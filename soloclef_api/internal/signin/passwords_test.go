package signin

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// pythonHash was made by the Python tier's password library (pwdlib's
// Argon2Hasher, argon2-cffi 25.1) for "correct horse battery staple". A
// password set there must sign in here, so the formats are one format.
const pythonHash = "$argon2id$v=19$m=65536,t=3,p=4$Y61VF5QS1CFs+6noh0genQ$eI1ze6tOGVypNdjtJsCWxO4evBY2hS9U6Ex3x0NRIFI"

func TestAHashThePythonTierMadeVerifiesHere(t *testing.T) {
	hasher := NewHasher(1)
	matches, stale, err := hasher.Verify(context.Background(), "correct horse battery staple", pythonHash)
	if err != nil || !matches || stale {
		t.Fatalf("matches=%v stale=%v err=%v", matches, stale, err)
	}
	if matches, _, _ := hasher.Verify(context.Background(), "correct horse battery stapler", pythonHash); matches {
		t.Fatal("a different password matched")
	}
}

func TestANewHashVerifiesAndCarriesTodaysSettings(t *testing.T) {
	hasher := NewHasher(2)
	hash, err := hasher.Hash(context.Background(), "a long passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("hash %q is not in argon2-cffi's format", hash)
	}
	again, _ := hasher.Hash(context.Background(), "a long passphrase")
	if again == hash {
		t.Fatal("two hashes of one password are equal: the salt is not fresh")
	}
	matches, stale, err := hasher.Verify(context.Background(), "a long passphrase", hash)
	if err != nil || !matches || stale {
		t.Fatalf("matches=%v stale=%v err=%v", matches, stale, err)
	}
}

func TestAWeakerHashIsMarkedForReplacement(t *testing.T) {
	hasher := NewHasher(1)
	key := deriveForTest("pw", []byte("0123456789abcdef"), 1, 8*1024, 1)
	weak := encodeHash(argonParameters{time: 1, memory: 8 * 1024, threads: 1}, []byte("0123456789abcdef"), key)
	matches, stale, err := hasher.Verify(context.Background(), "pw", weak)
	if err != nil || !matches || !stale {
		t.Fatalf("matches=%v stale=%v err=%v", matches, stale, err)
	}
}

func TestAHashInAnotherFormatIsAFaultNotAWrongPassword(t *testing.T) {
	for _, stored := range []string{
		"",
		"$2b$12$abcdefghijklmnopqrstuuJ7Qpw0tH8Ff2E3F1sWx1sXbBf5a6Wm.", // bcrypt
		"$argon2i$v=19$m=65536,t=3,p=4$c2FsdA$a2V5",
		"$argon2id$v=16$m=65536,t=3,p=4$c2FsdA$a2V5",
		"$argon2id$v=19$m=0,t=3,p=4$c2FsdA$a2V5",
		"$argon2id$v=19$m=65536,t=3,p=4$!!$a2V5",
	} {
		if _, _, err := NewHasher(1).Verify(context.Background(), "pw", stored); !errors.Is(err, ErrUnreadableHash) {
			t.Errorf("%q: err=%v, want ErrUnreadableHash", stored, err)
		}
	}
}

func TestAWaitingVerificationGivesUpWithItsRequest(t *testing.T) {
	hasher := NewHasher(1)
	hasher.slots <- struct{}{} // the one slot is busy
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := hasher.Verify(ctx, "pw", pythonHash); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want the request's cancellation", err)
	}
}
