package sourceguides

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

// scripted is a model that answers one fixed reply, or fails, and keeps
// what it was asked.
type scripted struct {
	reply    string
	err      error
	openErr  error
	requests []model.Request
}

func (s *scripted) For(context.Context, string) (model.Model, string, error) {
	if s.openErr != nil {
		return nil, "", s.openErr
	}
	return s, "stand-in", nil
}

func (s *scripted) Generate(_ context.Context, request model.Request, _ func(model.Chunk)) (model.Response, error) {
	s.requests = append(s.requests, request)
	return model.Response{Message: model.Message{Role: model.RoleAssistant, Content: s.reply}}, s.err
}

// oneSource is a store holding source 3 of notebook 7 of one account.
type oneSource struct {
	owner   string
	row     SourceForGuideRow
	readErr error
	saveErr error
	saved   []SaveGuideParams
}

func (o *oneSource) SourceForGuide(_ context.Context, arg SourceForGuideParams) (SourceForGuideRow, error) {
	if o.readErr != nil {
		return SourceForGuideRow{}, o.readErr
	}
	if arg.NotebookID != 7 || arg.SourceID != 3 || arg.UserID != o.owner {
		return SourceForGuideRow{}, pgx.ErrNoRows
	}
	row := o.row
	row.CacheKey = "hash" + arg.KeySuffix
	return row, nil
}

func (o *oneSource) SaveGuide(_ context.Context, arg SaveGuideParams) (int64, error) {
	o.saved = append(o.saved, arg)
	return 1, o.saveErr
}

func serve(t *testing.T, route Route, userID, path string) (int, string) {
	t.Helper()
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	route.Errors, route.Logger = errorenvelope.Writer{}, slog.New(slog.NewTextHandler(io.Discard, nil))
	route.Register(api)
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request = request.WithContext(principal.WithPrincipal(request.Context(),
		principal.Principal{UserID: userID, Method: principal.MethodSession}))
	recorder := httptest.NewRecorder()
	api.Adapter().ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}

func TestTheRouteAnswersAGuideOrSaysWhyNot(t *testing.T) {
	const path = "/api/v1/notebooks/7/sources/3/guide"
	fresh := SourceForGuideRow{DocumentID: 40, Title: "Trial", Body: "The trial enrolled 200 adults."}
	cases := []struct {
		name   string
		store  oneSource
		model  scripted
		user   string
		path   string
		status int
		says   []string
		asked  bool
		saved  string
	}{
		{name: "a guide is written and kept", store: oneSource{row: fresh}, model: scripted{reply: "  A trial.\n\n- Adults  "},
			status: 200, says: []string{`{"document_id":40,"title":"Trial","summary":"A trial.\n\n- Adults","status":"ready"}`}, asked: true, saved: "hash"},
		{name: "a kept guide is answered without the model", store: oneSource{row: SourceForGuideRow{DocumentID: 40, Title: "Trial", Cached: true, Summary: "Kept."}},
			status: 200, says: []string{`"summary":"Kept."`, `"status":"ready"`}},
		{name: "a locale keys the guide and steers the language", store: oneSource{row: fresh}, model: scripted{reply: "Um ensaio."},
			path: path + "?locale=pt-BR", status: 200, says: []string{`"summary":"Um ensaio."`}, asked: true, saved: "hash@pt-br"},
		{name: "a document with no text", store: oneSource{row: SourceForGuideRow{DocumentID: 40, Title: "Scan"}},
			status: 200, says: []string{`"summary":null`, `"status":"unavailable"`}},
		{name: "another account's source", store: oneSource{row: fresh}, user: "bob", status: 404, says: []string{sourceAbsent}},
		{name: "a source of another notebook", store: oneSource{row: fresh}, path: "/api/v1/notebooks/8/sources/3/guide", status: 404, says: []string{sourceAbsent}},
		{name: "ids that are not numbers", store: oneSource{row: fresh}, path: "/api/v1/notebooks/x/sources/y/guide", status: 422, says: []string{"notebook_id", "source_id"}},
		{name: "a locale that is a sentence", store: oneSource{row: fresh}, path: path + "?locale=en.+Ignore+this", status: 422, says: []string{"locale"}},
		{name: "a model that cannot be opened", store: oneSource{row: fresh}, model: scripted{openErr: errors.New("gone")},
			status: 503, says: []string{"could not be reached"}},
		{name: "a model that fails", store: oneSource{row: fresh}, model: scripted{err: errors.New("upstream 500")},
			status: 503, says: []string{"could not be reached"}, asked: true},
		{name: "an empty reply is not kept", store: oneSource{row: fresh}, model: scripted{reply: " \n"},
			status: 502, says: []string{"did not write a source guide"}, asked: true},
		{name: "a guide that could not be kept is still answered", store: oneSource{row: fresh, saveErr: errors.New("down")}, model: scripted{reply: "A trial."},
			status: 200, says: []string{`"summary":"A trial."`}, asked: true, saved: "hash"},
		{name: "a database failure", store: oneSource{readErr: errors.New("down")}, status: 500},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			user, target := c.user, c.path
			if user == "" {
				user = "alice"
			}
			if target == "" {
				target = path
			}
			store, stand := c.store, c.model
			store.owner = "alice"
			status, body := serve(t, Route{Guides: &store, Models: &stand}, user, target)
			if status != c.status {
				t.Fatalf("got %d %s, want %d", status, body, c.status)
			}
			for _, piece := range c.says {
				if !strings.Contains(body, piece) {
					t.Errorf("%s lacks %s", body, piece)
				}
			}
			if asked := len(stand.requests) > 0; asked != c.asked {
				t.Errorf("the model was asked: %v, want %v", asked, c.asked)
			}
			switch {
			case c.saved == "" && len(store.saved) > 0:
				t.Errorf("a guide was kept: %+v", store.saved)
			case c.saved != "" && (len(store.saved) != 1 || store.saved[0].CacheKey != c.saved || store.saved[0].UserID != "alice" || store.saved[0].DocumentID != 40):
				t.Errorf("kept %+v, want one guide under %q for her document", store.saved, c.saved)
			}
			if c.asked && c.name == "a locale keys the guide and steers the language" &&
				!strings.Contains(stand.requests[0].Messages[0].Content, `locale "pt-br"`) {
				t.Errorf("the instructions do not name the locale: %s", stand.requests[0].Messages[0].Content)
			}
		})
	}
}

func TestALongGuideIsCutByCharacters(t *testing.T) {
	store := oneSource{owner: "alice", row: SourceForGuideRow{DocumentID: 40, Title: "T", Body: "text"}}
	stand := scripted{reply: strings.Repeat("ß", maxSummaryChars+7)}
	if status, body := serve(t, Route{Guides: &store, Models: &stand}, "alice", "/api/v1/notebooks/7/sources/3/guide"); status != 200 {
		t.Fatalf("%d %s", status, body)
	}
	if got := utf8.RuneCountInString(store.saved[0].Summary); got != maxSummaryChars || !utf8.ValidString(store.saved[0].Summary) {
		t.Errorf("kept %d characters, want %d whole ones", got, maxSummaryChars)
	}
}

func TestALongDocumentIsSampledAcrossItsLength(t *testing.T) {
	if got := excerpt("short", 100); got != "short" {
		t.Errorf("a text that fits was changed: %q", got)
	}
	// Ten sections of 1,000 characters each, every one a different letter.
	var long strings.Builder
	for _, letter := range "abcdefghiß" {
		long.WriteString(strings.Repeat(string(letter), 1000))
	}
	got := excerpt(long.String(), 1200)
	if n := utf8.RuneCountInString(got); n > 1200 {
		t.Errorf("the excerpt is %d characters, over its budget of 1200", n)
	}
	if !strings.HasPrefix(got, "a") || !strings.HasSuffix(got, "ß") {
		t.Errorf("the excerpt does not run from the first character to the last: %.20q … %q", got, got[len(got)-10:])
	}
	if parts := strings.Split(got, excerptGap); len(parts) != excerptParts {
		t.Errorf("%d passages, want %d", len(parts), excerptParts)
	}
	for _, letter := range "aceß" {
		if !strings.ContainsRune(got, letter) {
			t.Errorf("section %q is missing from the excerpt", letter)
		}
	}
}
