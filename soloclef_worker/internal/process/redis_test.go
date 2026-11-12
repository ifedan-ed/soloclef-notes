package process

import (
	"strings"
	"testing"
)

// The broker is named, never guessed: a worker that fell back to a default
// address could consume the live queues from a dev build.
func TestTheBrokerUrlIsRequiredRatherThanGuessed(t *testing.T) {
	_, err := OpenRedis(func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), EnvRedisURL) {
		t.Fatalf("err = %v, want it to name %s", err, EnvRedisURL)
	}
}

func TestTheBrokerIsTheOneTheUrlNames(t *testing.T) {
	rdb, err := OpenRedis(func(name string) string {
		if name == EnvRedisURL {
			return "redis://127.0.0.1:39637/3"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("OpenRedis: %v", err)
	}
	defer func() { _ = rdb.Close() }()
	if options := rdb.Options(); options.Addr != "127.0.0.1:39637" || options.DB != 3 {
		t.Errorf("client points at %s db %d", options.Addr, options.DB)
	}
}

func TestAnUnreadableBrokerUrlIsRefused(t *testing.T) {
	_, err := OpenRedis(func(string) string { return "not a url" })
	if err == nil {
		t.Error("an unreadable broker url was accepted")
	}
}
