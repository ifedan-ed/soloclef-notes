package notes

import (
	"net/http"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// TestARequestOfTheWrongShapeIsRefusedBeforeAnythingIsRead: the routes run
// with no database at all, so a request that reached one would panic.
func TestARequestOfTheWrongShapeIsRefusedBeforeAnythingIsRead(t *testing.T) {
	s := serve(t, nil).as(testpostgres.Alice)
	cases := []struct {
		verb, path, body string
		status           int
		says             string
	}{
		{http.MethodGet, "/api/v1/notes/abc", "", http.StatusUnprocessableEntity, "note_id"},
		{http.MethodGet, "/api/v1/notes?limit=0&sort=size", "", http.StatusUnprocessableEntity, "sort"},
		{http.MethodPost, "/api/v1/notes", `{"title":5}`, http.StatusUnprocessableEntity, "title"},
		{http.MethodPatch, "/api/v1/notes/1", `{"title":null}`, http.StatusUnprocessableEntity, "title"},
		{http.MethodPut, "/api/v1/notes/1/notebook", `{}`, http.StatusUnprocessableEntity, "notebook_id"},
		{http.MethodPut, "/api/v1/notes/1/sources/x", "", http.StatusUnprocessableEntity, "notebook_id"},
		// Too wide for a bigint: names nothing, and is not sent to Postgres.
		{http.MethodGet, "/api/v1/notes/99999999999999999999", "", http.StatusNotFound, noteAbsent},
		{http.MethodPost, "/api/v1/notes", `{"notebook_id":99999999999999999999}`, http.StatusNotFound, notebookAbsent},
	}
	for _, c := range cases {
		answer := s.send(c.verb, c.path, c.body)
		if answer.status != c.status || !strings.Contains(answer.raw, c.says) {
			t.Errorf("%s %s %s: %d %s", c.verb, c.path, c.body, answer.status, answer.raw)
		}
	}
}
