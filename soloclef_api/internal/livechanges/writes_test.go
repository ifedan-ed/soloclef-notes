package livechanges

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_notify/livechange"
	"git.danvics.com/danvics/soloclef/soloclef_notify/livechange/livechangetest"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

func TestTheLongestPrefixDecides(t *testing.T) {
	cases := map[string][]livechange.Kind{
		"POST /api/v1/notebooks":                                            {livechange.Notebooks},
		"PATCH /api/v1/notebooks/{notebook_id}":                             {livechange.Notebooks},
		"POST /api/v1/notebooks/{notebook_id}/studio/generate":              {livechange.Outputs},
		"DELETE /api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}": {livechange.Outputs},
		"POST /api/v1/notebooks/{notebook_id}/research/search":              nil,
		"POST /api/v1/notebooks/{notebook_id}/research/import":              {livechange.Notebooks, livechange.Documents},
		"PUT /api/v1/search-source-connectors/{connector_id}":               {livechange.Connectors},
		"DELETE /api/v1/search-source-connectors/{connector_id}":            {livechange.Connectors, livechange.Documents},
		"POST /api/v1/new_chat":                                             {livechange.Chats, livechange.Notes, livechange.Documents, livechange.Folders, livechange.Outputs},
		"POST /api/v1/threads/{thread_id}/snapshots":                        nil,
		"POST /auth/jwt/login":                                              nil,
	}
	for pattern, want := range cases {
		rule, found := Classify(Rules, pattern)
		if !found {
			t.Errorf("%s: no rule", pattern)
			continue
		}
		var got []livechange.Kind
		for _, change := range rule.Changes {
			got = append(got, change.Kind)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v, want %v", pattern, got, want)
		}
	}
	// A segment boundary, not a string prefix: /api/v1/notebooks-x is not
	// under /api/v1/notebooks.
	if _, found := Classify([]Rule{{Prefix: "/api/v1/notebooks"}}, "POST /api/v1/notebooks-x"); found {
		t.Error("matched across a segment")
	}
}

func TestEveryRuleNamesAKnownKindAndUsesItsOwnParameters(t *testing.T) {
	for _, rule := range Rules {
		for _, change := range rule.Changes {
			if !livechange.Known(change.Kind) {
				t.Errorf("%s: unknown kind %q", rule.Prefix, change.Kind)
			}
			for _, param := range []string{change.IDFrom, change.NotebookIDFrom} {
				if param != "" && !containsParam(rule.Prefix, param) {
					t.Errorf("%s: names {%s}, which its prefix does not have", rule.Prefix, param)
				}
			}
		}
	}
}

func containsParam(prefix, name string) bool {
	for i := 0; i+len(name)+2 <= len(prefix); i++ {
		if prefix[i:i+len(name)+2] == "{"+name+"}" {
			return true
		}
	}
	return false
}

func writesAround(status int, recorder *livechangetest.Recorder) http.Handler {
	routes := []string{
		"PATCH /api/v1/notebooks/{notebook_id}",
		"POST /api/v1/notebooks/{notebook_id}/studio/generate",
		"GET /api/v1/notebooks/{notebook_id}",
		"POST /api/v1/notebooks/{notebook_id}/research/search",
	}
	route := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		// A write's recorder must keep the writer underneath reachable (the
		// chat's turn flushes through it); a read is not wrapped at all.
		_, wrapped := w.(interface{ Unwrap() http.ResponseWriter })
		if wrapped != isWrite(r.Method) {
			panic("a write's writer is hidden, or a read's is wrapped")
		}
	})
	return Writes{Publisher: recorder, Routes: routes, Rules: Rules}.Wrap(route)
}

func send(handler http.Handler, method, path string, who *principal.Principal) {
	request := httptest.NewRequest(method, path, nil)
	if who != nil {
		request = request.WithContext(principal.WithPrincipal(request.Context(), *who))
	}
	handler.ServeHTTP(httptest.NewRecorder(), request)
}

func TestASucceededWritePublishesItsChangesWithTheIdsInItsPath(t *testing.T) {
	recorder := &livechangetest.Recorder{}
	alice := &principal.Principal{UserID: "alice", Method: principal.MethodSession}
	send(writesAround(http.StatusOK, recorder), http.MethodPatch, "/api/v1/notebooks/42", alice)
	send(writesAround(http.StatusAccepted, recorder), http.MethodPost, "/api/v1/notebooks/42/studio/generate", alice)
	want := []livechangetest.Published{
		{UserID: "alice", Changes: []livechange.Change{{Kind: livechange.Notebooks, ID: 42}}},
		{UserID: "alice", Changes: []livechange.Change{{Kind: livechange.Outputs, NotebookID: 42}}},
	}
	if got := recorder.Calls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("published %+v", got)
	}
}

func TestNothingIsPublishedForAFailureARead(t *testing.T) {
	recorder := &livechangetest.Recorder{}
	alice := &principal.Principal{UserID: "alice", Method: principal.MethodSession}
	send(writesAround(http.StatusNotFound, recorder), http.MethodPatch, "/api/v1/notebooks/42", alice)
	send(writesAround(http.StatusOK, recorder), http.MethodGet, "/api/v1/notebooks/42", alice)
	send(writesAround(http.StatusOK, recorder), http.MethodPost, "/api/v1/notebooks/42/research/search", alice)
	send(writesAround(http.StatusOK, recorder), http.MethodPatch, "/api/v1/notebooks/42", nil)
	send(writesAround(http.StatusOK, recorder), http.MethodPatch, "/api/v1/unknown", alice)
	if got := recorder.Calls(); len(got) != 0 {
		t.Fatalf("published %+v", got)
	}
}

func TestAnAPIKeysWriteIsPublishedToo(t *testing.T) {
	// A script or an agent changing a notebook changes what the person's
	// open tab shows just the same.
	recorder := &livechangetest.Recorder{}
	key := &principal.Principal{UserID: "alice", Method: principal.MethodPersonalAccessToken}
	send(writesAround(http.StatusOK, recorder), http.MethodPatch, "/api/v1/notebooks/7", key)
	if got := recorder.Kinds("alice"); !reflect.DeepEqual(got, []livechange.Kind{livechange.Notebooks}) {
		t.Fatalf("published %v", got)
	}
}
