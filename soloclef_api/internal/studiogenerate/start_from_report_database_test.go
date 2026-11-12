package studiogenerate

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestAnOutputStartedFromAReportReadsTheReportsOwnSources(t *testing.T) {
	h := newHarness(t, false)
	ctx := context.Background()
	her := Caller{UserID: alice, RequestID: "req-1"}

	// The report was made from the Sun paper (in a folder source that is
	// not ticked today), a document being deleted and, by a stray id, a
	// document of another account. Only the first is read.
	id, err := h.routes.StartFromReport(ctx, her, 10, FromReport{
		Kind: "audio", Title: "Talk", Focus: "The sun", Language: "en", RequestID: "report1g1s1a0",
		DocumentIDs: []int64{101, 102, 200},
	})
	if err != nil || id == 0 {
		t.Fatalf("start: %d, %v", id, err)
	}
	if len(h.focuses) != 1 || !strings.Contains(h.focuses[0], "The sun pulls less.") {
		t.Fatalf("the podcast was opened with %q", h.focuses)
	}
	for _, leaked := range []string{"The moon pulls the sea.", "Being deleted.", "BOBS SECRET TEXT"} {
		if strings.Contains(h.focuses[0], leaked) {
			t.Errorf("the material holds %q", leaked)
		}
	}
	if h.count("notebook_outputs WHERE id = "+strconv.FormatInt(id, 10)+" AND notebook_id = 10 AND kind = 'audio' AND source_document_ids = '[101]'") != 1 {
		t.Error("the output does not record the one source it was made from")
	}

	// None of the report's sources is left in the notebook: the ticked
	// sources are read, as for any other output.
	if _, err := h.routes.StartFromReport(ctx, her, 10, FromReport{
		Kind: "audio", Title: "Talk", RequestID: "report1g1s2a0", DocumentIDs: []int64{200, 424242},
	}); err != nil {
		t.Fatal(err)
	}
	if len(h.focuses) != 2 || !strings.Contains(h.focuses[1], "The moon pulls the sea.") || strings.Contains(h.focuses[1], "BOBS SECRET TEXT") {
		t.Errorf("with none of its own left, the material was %q", h.focuses)
	}

	// An artifact kind is one output however often the same attempt is sent.
	first, err := h.routes.StartFromReport(ctx, her, 10, FromReport{Kind: "mindmap", Title: "Map", RequestID: "report1g1s3a0", DocumentIDs: []int64{101}})
	if err != nil {
		t.Fatal(err)
	}
	again, err := h.routes.StartFromReport(ctx, her, 10, FromReport{Kind: "mindmap", Title: "Map", RequestID: "report1g1s3a0", DocumentIDs: []int64{101}})
	if err != nil || again != first {
		t.Errorf("the same attempt twice: %d then %d, %v", first, again, err)
	}

	// Refusals are the dialog's own.
	for name, c := range map[string]struct {
		who    Caller
		asked  FromReport
		status int
	}{
		"another account's notebook": {Caller{UserID: bob}, FromReport{Kind: "audio", Title: "x", RequestID: "r1"}, http.StatusNotFound},
		"a kind outside the list":    {her, FromReport{Kind: "nonsense", Title: "x", RequestID: "r2"}, http.StatusUnprocessableEntity},
		"a request id with a space":  {her, FromReport{Kind: "quiz", Title: "x", RequestID: "r 3"}, http.StatusUnprocessableEntity},
		"a kind built by code, here": {her, FromReport{Kind: "slides", Title: "x", RequestID: "r4"}, http.StatusServiceUnavailable},
	} {
		_, err := h.routes.StartFromReport(ctx, c.who, 10, c.asked)
		if status, message, refused := Refused(err); !refused || status != c.status || message == "" {
			t.Errorf("%s: %d %q %v (%v)", name, status, message, refused, err)
		}
	}
	if _, _, refused := Refused(context.Canceled); refused {
		t.Error("an ordinary error read as a refusal")
	}
	if h.routes.CanMake("slides") || !h.routes.CanMake("quiz") || !h.routes.CanMake("audio") || h.routes.CanMake("nonsense") {
		t.Error("without a sandbox: a quiz and audio can be made, slides and an unknown kind cannot")
	}
}
