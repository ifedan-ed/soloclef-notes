// Package expotest is a stand-in for Expo's push service, for tests: it
// takes pushes and receipt requests on Expo's own paths and in Expo's own
// shapes, records them, and answers as told. Tests in this module and in the
// worker point expo.Client at it, so the real client's encoding, batching
// and error handling are what is tested.
package expotest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"

	"git.danvics.com/danvics/soloclef/soloclef_notify/expo"
	"git.danvics.com/danvics/soloclef/soloclef_notify/pushsend"
)

// Server is the fake service.
type Server struct {
	*httptest.Server

	mu sync.Mutex
	// requests is every push request, each the messages it carried.
	requests [][]expo.Message
	// authorizations is each push request's Authorization header.
	authorizations []string
	// receiptAsks is every receipts request's ids.
	receiptAsks [][]string
	// gone are tokens answered DeviceNotRegistered at send.
	gone map[string]bool
	// goneLater are tokens accepted at send whose receipt says
	// DeviceNotRegistered.
	goneLater map[string]bool
	// failures is how many of the next push requests answer this status
	// before the service starts accepting again.
	failures []int
	// tokens remembers which token each ticket was issued for.
	tokens map[string]string
	next   int
}

// New starts the fake. It is closed with the test.
func New() *Server {
	s := &Server{gone: map[string]bool{}, goneLater: map[string]bool{}, tokens: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /--/api/v2/push/send", s.send)
	mux.HandleFunc("POST /--/api/v2/push/getReceipts", s.receipts)
	s.Server = httptest.NewServer(mux)
	return s
}

// Gone makes the service answer DeviceNotRegistered for the token at once.
func (s *Server) Gone(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gone[token] = true
}

// GoneLater makes the service accept a push to the token and report the
// token as not registered in its receipt.
func (s *Server) GoneLater(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.goneLater[token] = true
}

// FailNext makes the next push requests answer these statuses, in order.
func (s *Server) FailNext(statuses ...int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, statuses...)
}

// Requests is every push request answered with tickets so far, each with
// its messages. A request the fake was told to fail is not in it.
func (s *Server) Requests() [][]expo.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]expo.Message(nil), s.requests...)
}

// Messages is every message of every request that was answered with
// tickets.
func (s *Server) Messages() []expo.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	var all []expo.Message
	for _, request := range s.requests {
		all = append(all, request...)
	}
	return all
}

// Authorizations is each push request's Authorization header, "" for none.
func (s *Server) Authorizations() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.authorizations...)
}

// ReceiptAsks is the ids of every receipts request.
func (s *Server) ReceiptAsks() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]string(nil), s.receiptAsks...)
}

type ticket struct {
	Status  string            `json:"status"`
	ID      string            `json:"id,omitempty"`
	Message string            `json:"message,omitempty"`
	Details map[string]string `json:"details,omitempty"`
}

func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	var messages []expo.Message
	if err := json.NewDecoder(r.Body).Decode(&messages); err != nil {
		http.Error(w, `{"errors":[{"code":"VALIDATION_ERROR","message":"not a list of messages"}]}`, http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.failures) > 0 {
		status := s.failures[0]
		s.failures = s.failures[1:]
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"errors":[{"code":"INTERNAL_SERVER_ERROR","message":"the fake was told to fail"}]}`))
		return
	}
	if len(messages) > pushsend.MaxBatch {
		http.Error(w, `{"errors":[{"code":"PUSH_TOO_MANY_NOTIFICATIONS","message":"over 100"}]}`, http.StatusBadRequest)
		return
	}
	s.requests = append(s.requests, messages)
	s.authorizations = append(s.authorizations, r.Header.Get("Authorization"))
	tickets := make([]ticket, len(messages))
	for i, message := range messages {
		if s.gone[message.To] {
			tickets[i] = ticket{Status: "error", Message: fmt.Sprintf("%q is not a registered push notification recipient", message.To),
				Details: map[string]string{"error": expo.DeviceNotRegistered}}
			continue
		}
		s.next++
		id := fmt.Sprintf("ticket-%d", s.next)
		s.tokens[id] = message.To
		tickets[i] = ticket{Status: "ok", ID: id}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": tickets})
}

func (s *Server) receipts(w http.ResponseWriter, r *http.Request) {
	var asked struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&asked); err != nil {
		http.Error(w, `{"errors":[{"code":"VALIDATION_ERROR","message":"no ids"}]}`, http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.receiptAsks = append(s.receiptAsks, asked.IDs)
	answer := map[string]ticket{}
	for _, id := range asked.IDs {
		token, known := s.tokens[id]
		switch {
		case !known:
			// Expo leaves out an id it has no receipt for.
		case s.goneLater[token]:
			answer[id] = ticket{Status: "error", Message: "the device is gone", Details: map[string]string{"error": expo.DeviceNotRegistered}}
		default:
			answer[id] = ticket{Status: "ok"}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": answer})
}
