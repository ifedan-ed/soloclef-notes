package main

import (
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

// A route that streams a file needs two things of every writer between it
// and the connection, and this holds the assembled front door to both:
//
//   - the connection's write deadline can be moved through it, or a stream is
//     cut at the server's write timeout however well its client is reading
//     (internal/rollingdeadline);
//   - a copied body is passed down through it, or a file is copied through
//     this process instead of being sent by the kernel.
//
// A wrapper added to the chain without `Unwrap` and `ReadFrom` breaks one or
// both, silently as far as its own tests go. This is the test that notices.
// (The wrappers behind the credential gate are assembled in run, out of this
// test's reach; each is held to the same two things in its own package.)
func TestAStreamCanReachTheConnectionThroughTheFrontDoor(t *testing.T) {
	var deadline error
	var chain []string
	var cannotCopy []string
	gated := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		deadline = http.NewResponseController(response).SetWriteDeadline(time.Now().Add(time.Minute))
		// The same walk http.ResponseController makes, written out so that
		// each link can be asked the second question.
		for writer := response; writer != nil; {
			name := fmt.Sprintf("%T", writer)
			chain = append(chain, name)
			if _, copies := writer.(io.ReaderFrom); !copies {
				cannotCopy = append(cannotCopy, name)
			}
			inner, wraps := writer.(interface{ Unwrap() http.ResponseWriter })
			if !wraps {
				break
			}
			writer = inner.Unwrap()
		}
		response.WriteHeader(http.StatusNoContent)
	})
	server := assembled(t, servedRoutes(), gated)

	response, err := http.Get(server.URL + "/api/v1/artifacts/2/files/3/content")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("the gated handler was not reached: status %d", response.StatusCode)
	}

	if deadline != nil {
		t.Errorf("the write deadline cannot be moved from behind the front door: %v", deadline)
	}
	// The walk must end at net/http's own writer, the one that owns the
	// connection and can sendfile.
	if len(chain) < 2 || chain[len(chain)-1] != "*http.response" {
		t.Errorf("the writers do not unwrap to the connection: %v", chain)
	}
	if len(cannotCopy) > 0 {
		t.Errorf("a copied body stops at %v, of %v", cannotCopy, chain)
	}
}
