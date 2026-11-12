package llm

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
)

// ownOpener records the own models it was asked to open.
type ownOpener struct{ asked []string }

func (o *ownOpener) Open(_ context.Context, userID string, id int64) (model.Model, error) {
	o.asked = append(o.asked, fmt.Sprintf("%s/%d", userID, id))
	return nil, nil
}

// A podcast or a video drafted by an account's own model opens that model
// for that account; a proxy name still goes through the proxy.
func TestAWriterIsTheAccountsOwnModelOrTheProxys(t *testing.T) {
	own := &ownOpener{}
	writers := Writers{
		Proxy: Proxy{BaseURL: "http://proxy.invalid/v1", Key: "fake-proxy-key", HTTP: &http.Client{}, CallTimeout: time.Minute,
			Retry: model.RetryPolicy{Attempts: 1, Backoff: time.Millisecond, MaxBackoff: time.Millisecond}},
		Own: own,
	}
	if err := writers.Check(); err != nil {
		t.Fatal(err)
	}
	if _, err := writers.Client(context.Background(), "account-a", "own:12"); err != nil {
		t.Fatal(err)
	}
	proxied, err := writers.Client(context.Background(), "account-a", "deepseek-v4.1")
	if err != nil || proxied == nil {
		t.Fatalf("the proxy name opened %v, %v", proxied, err)
	}
	if fmt.Sprint(own.asked) != "[account-a/12]" {
		t.Fatalf("own models opened: %v", own.asked)
	}
	if err := (Writers{Proxy: writers.Proxy}).Check(); err == nil {
		t.Fatal("writers without the account's own connections passed the check")
	}
}
