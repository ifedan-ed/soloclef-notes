package mailconfirm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/maildrafts"
)

const (
	ada   = "aaaaaaaa-0000-4000-8000-000000000001"
	draft = "0f1e2d3c-4b5a-4968-8776-655443322110"
)

// guard stands in for maildrafts.Guard: it answers with what each test
// sets and records what it was asked. The guard's own rules are tested
// against a real Postgres in soloclef_assistant/maildrafts.
type guard struct {
	err   error
	asked []string
}

func (g *guard) view(status string) maildrafts.View {
	return maildrafts.View{Draft: maildrafts.DraftView{ID: draft, Status: status, To: []maildrafts.Address{{Email: "ben@example.test"}}},
		Confirmation: &maildrafts.ConfirmationView{Token: "go-ahead", ContentHash: "sha256:abc"}}
}

func (g *guard) Confirm(_ context.Context, ownerID, draftID string) (maildrafts.View, error) {
	g.asked = append(g.asked, "confirm|"+ownerID+"|"+draftID)
	return g.view("pending"), g.err
}

func (g *guard) Send(_ context.Context, ownerID, draftID, token, hash string, acknowledged []string) (maildrafts.View, error) {
	g.asked = append(g.asked, "send|"+ownerID+"|"+draftID+"|"+token+"|"+hash+"|"+strings.Join(acknowledged, ","))
	return g.view("sent"), g.err
}

func (g *guard) Discard(_ context.Context, ownerID, draftID string) (maildrafts.View, error) {
	g.asked = append(g.asked, "discard|"+ownerID+"|"+draftID)
	return g.view("discarded"), g.err
}

func (g *guard) Waiting(_ context.Context, ownerID string) (maildrafts.WaitingList, error) {
	g.asked = append(g.asked, "waiting|"+ownerID)
	return maildrafts.WaitingList{Drafts: []maildrafts.WaitingDraft{{ID: draft, Status: "pending", Subject: "Invoice", AccountLabel: "Work"}}}, g.err
}

func serve(t *testing.T, g *guard, caller principal.Principal, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	routes := Routes{Guard: g, Errors: errorenvelope.Writer{ReportURL: "https://notes.example.test/contact"}, Logger: logger}
	if err := routes.Check(); err != nil {
		t.Fatal(err)
	}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	routes.Register(api)
	handler := requestcontext.Middleware(logger, requestbody.Middleware{Covers: requestbody.CoveringRoutes(BodyRoutes()...)}.Wrap(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			api.Adapter().ServeHTTP(w, r.WithContext(principal.WithPrincipal(r.Context(), caller)))
		})))
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func at(path string) string { return strings.Replace(path, "{draft_id}", draft, 1) }

var person = principal.Principal{UserID: ada, Method: principal.MethodSession}

func TestOnlyAPersonInASessionCanConfirmOrSend(t *testing.T) {
	for _, method := range []principal.Method{principal.MethodPersonalAccessToken, principal.MethodAgent} {
		g := &guard{}
		program := principal.Principal{UserID: ada, Method: method}
		for _, path := range []string{ConfirmationPath, SendPath, DiscardPath} {
			if got := serve(t, g, program, http.MethodPost, at(path), `{"confirmation":"go-ahead","content_hash":"sha256:abc"}`); got.Code != http.StatusForbidden {
				t.Errorf("%s reached %s: %d", method, path, got.Code)
			}
		}
		if got := serve(t, g, program, http.MethodGet, WaitingPath, ""); got.Code != http.StatusForbidden {
			t.Errorf("%s listed the waiting drafts: %d", method, got.Code)
		}
		if len(g.asked) != 0 {
			t.Errorf("%s reached the guard: %v", method, g.asked)
		}
	}
}

func TestTheWaitingListIsTheOwnersAndNeverCached(t *testing.T) {
	g := &guard{}
	got := serve(t, g, person, http.MethodGet, WaitingPath, "")
	var list maildrafts.WaitingList
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &list) != nil || len(list.Drafts) != 1 ||
		list.Drafts[0].ID != draft || list.Drafts[0].AccountLabel != "Work" || got.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("list = %d %s %v", got.Code, got.Body, got.Header())
	}
	// The account is the caller's own, never a parameter.
	if len(g.asked) != 1 || g.asked[0] != "waiting|"+ada {
		t.Fatalf("asked %v", g.asked)
	}
	// A failure to read is the generic 500, with nothing of the cause.
	failing := &guard{err: errors.New("pool closed")}
	if got := serve(t, failing, person, http.MethodGet, WaitingPath, ""); got.Code != http.StatusInternalServerError || strings.Contains(got.Body.String(), "pool") {
		t.Fatalf("a failed list = %d %s", got.Code, got.Body)
	}
}

func TestTheGuardsAnswersInHTTP(t *testing.T) {
	g := &guard{}
	shown := serve(t, g, person, http.MethodPost, at(ConfirmationPath), "")
	var view maildrafts.View
	if shown.Code != http.StatusOK || json.Unmarshal(shown.Body.Bytes(), &view) != nil || view.Confirmation.Token != "go-ahead" ||
		shown.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("confirmation = %d %s %v", shown.Code, shown.Body, shown.Header())
	}
	sent := serve(t, g, person, http.MethodPost, at(SendPath), `{"confirmation":"go-ahead","content_hash":"sha256:abc"}`)
	if sent.Code != http.StatusOK || g.asked[1] != "send|"+ada+"|"+draft+"|go-ahead|sha256:abc|" {
		t.Fatalf("send = %d %s, asked %v", sent.Code, sent.Body, g.asked)
	}
	// The unusual points the person ticked reach the guard as they were
	// sent; the guard, not the route, judges them.
	ticked := serve(t, g, person, http.MethodPost, at(SendPath),
		`{"confirmation":"go-ahead","content_hash":"sha256:abc","acknowledged":["many_recipients","new_recipients"]}`)
	if ticked.Code != http.StatusOK || g.asked[2] != "send|"+ada+"|"+draft+"|go-ahead|sha256:abc|many_recipients,new_recipients" {
		t.Fatalf("a ticked send = %d, asked %v", ticked.Code, g.asked)
	}
	// A body that is not a card's request is refused before the guard.
	flood := `{"confirmation":"go-ahead","content_hash":"sha256:abc","acknowledged":[` + strings.Repeat(`"x",`, 40) + `"x"]}`
	if got := serve(t, g, person, http.MethodPost, at(SendPath), flood); got.Code != http.StatusUnprocessableEntity || len(g.asked) != 3 {
		t.Fatalf("a flood of codes = %d, asked %v", got.Code, g.asked)
	}
	for err, want := range map[error]int{
		maildrafts.ErrNoDraft:                            http.StatusNotFound,
		maildrafts.ErrDraftChanged:                       http.StatusConflict,
		maildrafts.ErrConfirmationSpent:                  http.StatusGone,
		maildrafts.ErrConfirmationExpired:                http.StatusGone,
		maildrafts.ErrDraftNotPending:                    http.StatusGone,
		maildrafts.ErrConfirmationInvalid:                http.StatusForbidden,
		maildrafts.ErrUnusualNotConfirmed:                http.StatusPreconditionRequired,
		maildrafts.ErrUnusualChanged:                     http.StatusConflict,
		&maildrafts.SendFailure{Message: "no emailSend"}: http.StatusBadGateway,
	} {
		g := &guard{err: err}
		got := serve(t, g, person, http.MethodPost, at(SendPath), `{"confirmation":"go-ahead","content_hash":"sha256:abc"}`)
		var envelope struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(got.Body.Bytes(), &envelope)
		if got.Code != want || envelope.Error.Code == "" || strings.Contains(got.Body.String(), "go-ahead") {
			t.Errorf("%v: %d %s", err, got.Code, got.Body)
		}
	}
}
