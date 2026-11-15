package citedsources

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

// library holds one account's passages and keeps what it was asked.
type library struct {
	owner string
	rows  map[int64]CitedPassagesRow
	err   error
	asked []CitedPassagesParams
}

func (l *library) CitedPassages(_ context.Context, arg CitedPassagesParams) ([]CitedPassagesRow, error) {
	l.asked = append(l.asked, arg)
	if l.err != nil {
		return nil, l.err
	}
	var out []CitedPassagesRow
	if arg.UserID != l.owner {
		return out, nil
	}
	for _, id := range arg.ChunkIds {
		if row, ok := l.rows[id]; ok {
			out = append(out, row)
		}
	}
	return out, nil
}

func serve(t *testing.T, passages Passages, userID, path string) (int, string) {
	t.Helper()
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Route{Passages: passages, Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}.Register(api)
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request = request.WithContext(principal.WithPrincipal(request.Context(),
		principal.Principal{UserID: userID, Method: principal.MethodSession}))
	recorder := httptest.NewRecorder()
	api.Adapter().ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}

const owner = "11111111-1111-4111-8111-111111111111"

func page(n int64) *int64 { return &n }

func TestEachCitedPassageIsNamedByItsDocumentAndPage(t *testing.T) {
	store := &library{owner: owner, rows: map[int64]CitedPassagesRow{
		90:  {ChunkID: 90, DocumentID: 3, Title: "Guideline.pdf", PageStart: page(2)},
		812: {ChunkID: 812, DocumentID: 4, Title: "Notes"},
	}}
	status, body := serve(t, store, owner, Path+"?ids=812,%2090,812,7")
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	want := `{"sources":[{"chunk_id":812,"document_id":4,"title":"Notes","page":null},` +
		`{"chunk_id":90,"document_id":3,"title":"Guideline.pdf","page":2}]}`
	if strings.TrimSpace(body) != want {
		t.Fatalf("body\n got %s\nwant %s", body, want)
	}
	// One statement for the whole answer, each id asked once, under the caller.
	if len(store.asked) != 1 || store.asked[0].UserID != owner ||
		len(store.asked[0].ChunkIds) != 3 {
		t.Fatalf("asked %+v", store.asked)
	}
}

func TestAnotherAccountsPassagesAreAbsentNotRefused(t *testing.T) {
	store := &library{owner: owner, rows: map[int64]CitedPassagesRow{90: {ChunkID: 90, DocumentID: 3, Title: "Mine"}}}
	status, body := serve(t, store, "22222222-2222-4222-8222-222222222222", Path+"?ids=90")
	if status != http.StatusOK || strings.TrimSpace(body) != `{"sources":[]}` {
		t.Fatalf("status %d: %s", status, body)
	}
}

func TestAListThatIsNotPassageIdsIsRefused(t *testing.T) {
	many := make([]string, maxIDs+1)
	for i := range many {
		many[i] = strconv.Itoa(i + 1)
	}
	for name, query := range map[string]string{
		"missing":       "",
		"empty":         "?ids=",
		"a word":        "?ids=1,two",
		"zero":          "?ids=0",
		"negative":      "?ids=-4",
		"an empty item": "?ids=1,,2",
		"too many":      "?ids=" + strings.Join(many, ","),
	} {
		store := &library{owner: owner}
		status, body := serve(t, store, owner, Path+query)
		if status != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d: %s", name, status, body)
		}
		if len(store.asked) != 0 {
			t.Errorf("%s: the database was asked", name)
		}
	}
}

func TestAFailedReadIsTheGeneric500(t *testing.T) {
	status, body := serve(t, &library{owner: owner, err: errors.New("connection reset")}, owner, Path+"?ids=1")
	if status != http.StatusInternalServerError || !strings.Contains(body, errorenvelope.GenericServerMessage) {
		t.Fatalf("status %d: %s", status, body)
	}
}
