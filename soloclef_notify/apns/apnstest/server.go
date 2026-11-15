// Package apnstest is a stand-in for Apple's push service, for tests: an
// HTTP/2 server over TLS (httptest's) that takes pushes on Apple's own path,
// records each one with its headers and payload, and answers as told. Tests
// point the real apns.Client at it, so the client's HTTP/2 transport, its
// headers, its JWT and its reading of Apple's answers are what is tested.
package apnstest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_notify/apns"
)

// Topic is the bundle id the fake's clients push to.
const Topic = "com.danvics.soloclef"

// Key ids of the test key: ten capitals or digits, as Apple's are.
const (
	KeyID  = "TESTKEY001"
	TeamID = "TESTTEAM01"
)

// Push is one request the fake was sent.
type Push struct {
	// Proto is the HTTP major version it came over: 2, or the client is
	// not speaking to Apple as Apple requires.
	Proto int
	// Token is the device token from the path.
	Token  string
	Header http.Header
	// Payload is the JSON body, decoded.
	Payload Payload
	// Answered is the status the fake answered it with.
	Answered int
}

// Payload is what an alert push carries.
type Payload struct {
	APS struct {
		Alert struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		} `json:"alert"`
		Badge    *int   `json:"badge"`
		Sound    string `json:"sound"`
		ThreadID string `json:"thread-id"`
	} `json:"aps"`
	Body map[string]any `json:"body"`
}

// Answer is a status and Apple's reason to answer one push with.
type Answer struct {
	Status int
	Reason string
}

// Server is the fake service.
type Server struct {
	*httptest.Server

	mu     sync.Mutex
	pushes []Push
	// answers are what the next pushes to a token are answered, in order;
	// a token with none left is accepted.
	answers map[string][]Answer
}

// New starts the fake, speaking HTTP/2 over TLS only as Apple does. It is
// closed with the test.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{answers: map[string][]Answer{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /3/device/{token}", s.push)
	s.Server = httptest.NewUnstartedServer(mux)
	s.EnableHTTP2 = true
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

// Answer makes the next pushes to the token answer these, in order, before
// the fake accepts it again.
func (s *Server) Answer(token string, answers ...Answer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answers[token] = append(s.answers[token], answers...)
}

// Pushes is every push the fake was sent, accepted or not.
func (s *Server) Pushes() []Push {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Push(nil), s.pushes...)
}

// Accepted is the tokens of the pushes the fake accepted, in order.
func (s *Server) Accepted() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var tokens []string
	for _, push := range s.pushes {
		if push.Answered == http.StatusOK {
			tokens = append(tokens, push.Token)
		}
	}
	return tokens
}

// Client is an apns.Client pushing to the fake with a signer of a key made
// for the test, over the transport production uses, trusting the fake's
// certificate.
func (s *Server) Client(t testing.TB, log *slog.Logger) *apns.Client {
	t.Helper()
	roots := x509.NewCertPool()
	roots.AddCert(s.Certificate())
	signer, err := apns.NewSigner(KeyPEM(t), KeyID, TeamID)
	if err != nil {
		t.Fatal(err)
	}
	client := &apns.Client{BaseURL: s.URL, Topic: Topic, Signer: signer, HTTP: apns.NewHTTPClient(roots), Log: log}
	if err := client.Check(); err != nil {
		t.Fatal(err)
	}
	return client
}

// KeyPEM is a new ECDSA P-256 key in the form Apple issues a .p8 in.
func KeyPEM(t testing.TB) []byte {
	t.Helper()
	pemBytes, _ := Key(t)
	return pemBytes
}

// Key is a new ECDSA P-256 key as a .p8 and as the key itself, for a test
// that checks a signature.
func Key(t testing.TB) ([]byte, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), key
}

func (s *Server) push(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	push := Push{Proto: r.ProtoMajor, Token: r.PathValue("token"), Header: r.Header.Clone()}
	_ = json.Unmarshal(body, &push.Payload)

	s.mu.Lock()
	answer := Answer{Status: http.StatusOK}
	if queued := s.answers[push.Token]; len(queued) > 0 {
		answer, s.answers[push.Token] = queued[0], queued[1:]
	}
	// Apple refuses anything but HTTP/2 and a push with no provider token
	// or topic; so does the fake, whatever it was told.
	switch {
	case r.ProtoMajor != 2:
		answer = Answer{Status: http.StatusBadRequest, Reason: "HTTP2Required"}
	case !strings.HasPrefix(r.Header.Get("authorization"), "bearer "):
		answer = Answer{Status: http.StatusForbidden, Reason: "MissingProviderToken"}
	case r.Header.Get("apns-topic") == "":
		answer = Answer{Status: http.StatusBadRequest, Reason: "MissingTopic"}
	}
	push.Answered = answer.Status
	s.pushes = append(s.pushes, push)
	s.mu.Unlock()

	w.Header().Set("apns-id", r.Header.Get("apns-id"))
	if answer.Status == http.StatusOK {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(answer.Status)
	_ = json.NewEncoder(w).Encode(map[string]any{"reason": answer.Reason})
}
