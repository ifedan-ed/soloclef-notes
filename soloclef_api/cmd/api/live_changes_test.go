package main

import (
	"net/http"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/livechanges"
)

// Every served write route says what it changes for the person's open tabs
// and app, even if that is nothing (internal/livechanges, Rules). A new write
// route fails here until it is placed, so a screen is never left stale by a
// route nobody thought about.
func TestEveryWriteRouteSaysWhatItChangesLive(t *testing.T) {
	for _, route := range servedRoutes() {
		method, _, _ := strings.Cut(route, " ")
		switch method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			continue
		}
		if _, found := livechanges.Classify(livechanges.Rules, route); !found {
			t.Errorf("%s is in no live-change rule (internal/livechanges/rules.go)", route)
		}
	}
}

// The stream is behind the credential gate like every route not listed as
// public: a stranger is refused before a stream is opened.
func TestTheLiveChangeStreamIsNotPublic(t *testing.T) {
	for _, route := range publicRoutes() {
		if strings.HasSuffix(route, " "+livechanges.Path) {
			t.Fatalf("%s is served outside the gate", route)
		}
	}
	if _, served := set(servedRoutes())[http.MethodGet+" "+livechanges.Path]; !served {
		t.Fatal("the live-change stream is not served")
	}
}
