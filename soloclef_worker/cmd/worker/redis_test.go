package main

import (
	"strings"
	"testing"
)

// The broker is named, never guessed: a worker that fell back to a default
// address could consume the live queues from a dev build.
func TestTheBrokerUrlIsRequiredRatherThanGuessed(t *testing.T) {
	_, err := openRedis(func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), EnvRedisURL) {
		t.Fatalf("err = %v, want it to name %s", err, EnvRedisURL)
	}
}

func TestTheBrokerIsTheOneTheUrlNames(t *testing.T) {
	rdb, err := openRedis(func(name string) string {
		if name == EnvRedisURL {
			return "redis://127.0.0.1:39637/3"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("openRedis: %v", err)
	}
	defer func() { _ = rdb.Close() }()
	if options := rdb.Options(); options.Addr != "127.0.0.1:39637" || options.DB != 3 {
		t.Errorf("client points at %s db %d", options.Addr, options.DB)
	}
}

func TestAnUnreadableBrokerUrlIsRefused(t *testing.T) {
	_, err := openRedis(func(string) string { return "not a url" })
	if err == nil {
		t.Error("an unreadable broker url was accepted")
	}
}

// The name goes in every result row as Celery's hostname does, so it says
// which worker ran the job without having to be configured per container.
func TestTheWorkerNamesItselfAfterItsHostUnlessItIsTold(t *testing.T) {
	told := workerName(func(string) string { return "go-worker@somewhere" })
	if told != "go-worker@somewhere" {
		t.Errorf("name = %q, want the one it was given", told)
	}
	derived := workerName(func(string) string { return "" })
	if !strings.HasPrefix(derived, "go-worker@") || len(derived) <= len("go-worker@") {
		t.Errorf("name = %q, want go-worker@<host>", derived)
	}
}
