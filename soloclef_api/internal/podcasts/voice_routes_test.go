package podcasts

import (
	"bytes"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/voices"
)

const (
	voicesPath    = "/api/v1/podcasts/voices"
	languagesPath = "/api/v1/podcasts/languages"
)

func voiceIDs(t *testing.T, got answered) []string {
	t.Helper()
	if got.status != http.StatusOK || got.list == nil {
		t.Fatalf("status %d and no list: %s", got.status, got.raw)
	}
	ids := []string{}
	for _, entry := range got.list {
		ids = append(ids, entry.(map[string]any)["voice_id"].(string))
	}
	return ids
}

func TestTheVoicesAreListedAsThePickerParsesThem(t *testing.T) {
	got := ask(t, newFake(), principal.MethodSession, http.MethodGet, voicesPath)
	if ids := voiceIDs(t, got); len(ids) != 23 || ids[0] != "kokoro:am_adam" {
		t.Fatalf("the voices are %v", ids)
	}
	// Exactly the four fields of the web app's `voiceOption`.
	want := map[string]any{"voice_id": "kokoro:am_adam", "display_name": "Adam (US)", "language": "en-US", "gender": "male"}
	if !reflect.DeepEqual(got.list[0], want) {
		t.Errorf("a voice is %v, want %v", got.list[0], want)
	}
	if got.header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type %q", got.header.Get("Content-Type"))
	}
}

func TestTheVoicesOfOneLanguage(t *testing.T) {
	for query, want := range map[string][]string{
		"?language=pt":    {"kokoro:pm_alex", "kokoro:pf_dora"},
		"?language=PT-br": {"kokoro:pm_alex", "kokoro:pf_dora"},
		"?language=fr":    {"kokoro:ff_siwis"},
		// A language is a language even when nothing speaks it.
		"?language=de": {},
	} {
		if ids := voiceIDs(t, ask(t, newFake(), principal.MethodSession, http.MethodGet, voicesPath+query)); !reflect.DeepEqual(ids, want) {
			t.Errorf("%s: %v, want %v", query, ids, want)
		}
	}
	// What is not a language is said to be none, rather than read as "all".
	for _, query := range []string{"?language=", "?language=english", "?language=e", "?language=en_US"} {
		got := ask(t, newFake(), principal.MethodSession, http.MethodGet, voicesPath+query)
		if got.status != http.StatusUnprocessableEntity || got.faults()["query.language"] != notALanguage {
			t.Errorf("%s: status %d, faults %v", query, got.status, got.faults())
		}
	}
}

func TestVoicesThatSpeakAnythingMatchEveryLanguage(t *testing.T) {
	f := newFake()
	f.catalogue = openAI(t, "coral", "ash")
	got := ask(t, f, principal.MethodSession, http.MethodGet, voicesPath+"?language=sw")
	if ids := voiceIDs(t, got); !reflect.DeepEqual(ids, []string{"openai:ash", "openai:coral"}) {
		t.Errorf("the Swahili voices are %v", ids)
	}
	// "*" is what the form reads as "speaks every language".
	if language := got.list[0].(map[string]any)["language"]; language != "*" {
		t.Errorf("the voice's language is %v", language)
	}
}

func TestTheLanguagesOnOffer(t *testing.T) {
	got := ask(t, newFake(), principal.MethodSession, http.MethodGet, languagesPath)
	want := map[string]any{
		"languages":     []any{"en-GB", "en-US", "es", "fr", "hi", "it", "ja", "pt-BR", "zh"},
		"allows_custom": false,
	}
	if got.status != http.StatusOK || !reflect.DeepEqual(got.body, want) {
		t.Errorf("Kokoro's languages: %d %v, want %v", got.status, got.body, want)
	}

	f := newFake()
	f.catalogue = openAI(t, "coral")
	got = ask(t, f, principal.MethodSession, http.MethodGet, languagesPath)
	languages, _ := got.body["languages"].([]any)
	if got.body["allows_custom"] != true || len(languages) != 21 || languages[0] != "ar" || languages[20] != "zh" {
		t.Errorf("an any-language roster offers %v", got.body)
	}
}

// The Python tier answered both lists with no credential at all. Here they
// are behind the tier's authentication like every other route; a request
// that somehow arrives without a caller is a wiring fault and is told
// nothing about the catalogue.
func TestNoRouteAnswersWithoutACaller(t *testing.T) {
	for _, route := range []struct{ verb, path, body string }{
		{http.MethodGet, voicesPath, ""},
		{http.MethodGet, languagesPath, ""},
		{http.MethodGet, voicesPath + "/kokoro:af_heart/preview", ""},
		{http.MethodPost, "/api/v1/podcasts", `{"title": "t", "source_content": "s"}`},
		{http.MethodPatch, "/api/v1/podcasts/41/spec", `{"spec": ` + goodBrief + `, "expected_version": 1}`},
		{http.MethodPost, "/api/v1/podcasts/41/cancel", ""},
	} {
		f := newFake(podcast(41, nil))
		got := send(t, f, nobody, route.verb, route.path, route.body)
		if got.status != http.StatusInternalServerError {
			t.Errorf("%s %s answered %d to nobody: %s", route.verb, route.path, got.status, got.raw)
		}
		if bytes.Contains(got.raw, []byte("kokoro")) || len(f.sampled) != 0 || len(f.podcasts) != 1 || f.moves != 0 {
			t.Errorf("%s %s did something for nobody: %s", route.verb, route.path, got.raw)
		}
	}
}

// The three ways the catalogue does not answer, on every route that asks it.
// Each is a 503 with its own sentence, and none of them tells the caller
// which setting or which address is at fault -- that is in the log.
func TestACatalogueThatCannotAnswerIsA503(t *testing.T) {
	failures := map[string]struct {
		err  error
		says string
	}{
		"no speech service": {voices.ErrNotConfigured, noSpeechService},
		"the proxy is down": {&voices.UnreachableError{Cause: errors.New("dial tcp 10.0.0.9:4000: connection refused")}, voicesUnavailable},
		"a wrong setting":   {&voices.MisconfiguredError{Setting: voices.PodcastVoicesVariable, Reason: "names coral"}, voicesMisconfigured},
	}
	routes := []struct{ verb, path, body string }{
		{http.MethodGet, voicesPath, ""},
		{http.MethodGet, languagesPath, ""},
		{http.MethodGet, voicesPath + "/kokoro:af_heart/preview", ""},
		{http.MethodPost, "/api/v1/podcasts", `{"title": "t", "source_content": "s"}`},
		{http.MethodPatch, "/api/v1/podcasts/41/spec", `{"spec": ` + goodBrief + `, "expected_version": 1}`},
	}
	for name, failure := range failures {
		for _, route := range routes {
			f := newFake(podcast(41, nil))
			f.catalogueErr = failure.err
			got := send(t, f, principal.MethodSession, route.verb, route.path, route.body)
			if got.status != http.StatusServiceUnavailable || got.message() != failure.says {
				t.Errorf("%s, %s %s: %d %q, want 503 %q", name, route.verb, route.path, got.status, got.message(), failure.says)
			}
			for _, secret := range []string{"10.0.0.9", "SOLOCLEF_API", "coral"} {
				if bytes.Contains(got.raw, []byte(secret)) {
					t.Errorf("%s, %s %s: the answer says %q: %s", name, route.verb, route.path, secret, got.raw)
				}
			}
			// And nothing was written or synthesised on the way.
			if len(f.podcasts) != 1 || f.podcasts[41].SpecVersion != 1 || len(f.sampled) != 0 {
				t.Errorf("%s, %s %s changed something", name, route.verb, route.path)
			}
		}
	}
}

func TestAVoiceSampleIsServedAsAudio(t *testing.T) {
	f := newFake()
	// The web app sends the id as a path segment, colon escaped.
	got := ask(t, f, principal.MethodSession, http.MethodGet, voicesPath+"/kokoro%3Aaf_heart/preview")
	if got.status != http.StatusOK || !bytes.Equal(got.raw, sampleAudio) {
		t.Fatalf("status %d, %d bytes: %s", got.status, len(got.raw), got.raw)
	}
	for header, want := range map[string]string{
		"Content-Type":   "audio/mpeg",
		"Content-Length": strconv.Itoa(len(sampleAudio)),
		// A sample never changes, so the browser keeps it; it is behind a
		// sign-in, so nothing shared does.
		"Cache-Control":               "private, max-age=86400",
		"Access-Control-Allow-Origin": origin,
	} {
		if got.header.Get(header) != want {
			t.Errorf("%s is %q, want %q", header, got.header.Get(header), want)
		}
	}
	if !reflect.DeepEqual(f.sampled, []string{"kokoro:af_heart"}) {
		t.Errorf("sampled %v", f.sampled)
	}
}

// Every account can make a podcast, so every account may hear a voice
// before choosing it, however it signed in. What keeps the route from being
// a speech service is the booth, not the door (soloclef_assistant/voices.Samples).
func TestAnyoneSignedInMayHearAVoiceSample(t *testing.T) {
	for _, c := range []struct {
		asker  string
		method principal.Method
	}{
		{callerID, principal.MethodSession},
		{callerID, principal.MethodPersonalAccessToken},
		{callerID, principal.MethodAgent},
		// An account that has never made a podcast.
		{strangerID, principal.MethodSession},
	} {
		f := newFake()
		f.asker = c.asker
		got := ask(t, f, c.method, http.MethodGet, voicesPath+"/kokoro:af_heart/preview")
		if got.status != http.StatusOK || len(f.sampled) != 1 {
			t.Errorf("%s by %s: status %d, sampled %v: %s", c.asker, c.method, got.status, f.sampled, got.raw)
		}
	}
}

func TestOnlyAVoiceOfTheRosterHasASample(t *testing.T) {
	for _, id := range []string{
		"kokoro:nobody",
		// A real voice, of a roster this deployment does not use.
		"openai:coral",
		"af_heart",
		"kokoro%3Aaf_heart%2Fextra",
	} {
		f := newFake()
		got := ask(t, f, principal.MethodSession, http.MethodGet, voicesPath+"/"+id+"/preview")
		if got.status != http.StatusNotFound || got.message() != voiceAbsent {
			t.Errorf("%s: %d %q", id, got.status, got.message())
		}
		if len(f.sampled) != 0 {
			t.Errorf("%s: the booth was asked for %v", id, f.sampled)
		}
	}
}

func TestASampleThatCannotBeHadIsA503ThatSaysNothingOfWhy(t *testing.T) {
	for name, failure := range map[string]error{
		"the store is down":    voices.ErrStoreUnavailable,
		"the provider refused": errors.New("POST /audio/speech answered 401: invalid key sk-live-1234"),
	} {
		f := newFake()
		f.sampleErr = failure
		got := ask(t, f, principal.MethodSession, http.MethodGet, voicesPath+"/kokoro:af_heart/preview")
		if got.status != http.StatusServiceUnavailable || got.message() != sampleUnavailable {
			t.Errorf("%s: %d %q", name, got.status, got.message())
		}
		if strings.Contains(string(got.raw), "sk-live") {
			t.Errorf("%s: the provider's words reached the caller: %s", name, got.raw)
		}
	}
}
