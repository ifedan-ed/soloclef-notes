package podcasts

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

const createPath = "/api/v1/podcasts"

// create sends a new podcast, built from the smallest valid body with the
// given fields set, replaced or (for a value of "") removed. Values are JSON.
func create(t *testing.T, f *fake, method principal.Method, fields map[string]string) answered {
	t.Helper()
	body := map[string]string{
		"title":          `"The harbour hour"`,
		"source_content": `"Tides are caused by the moon."`,
	}
	for name, value := range fields {
		if value == "" {
			delete(body, name)
		} else {
			body[name] = value
		}
	}
	parts := []string{}
	for name, value := range body {
		parts = append(parts, `"`+name+`": `+value)
	}
	return send(t, f, method, http.MethodPost, createPath, "{"+strings.Join(parts, ", ")+"}")
}

// speakers is the brief's speakers as "slot name role voice", in order.
func speakers(t *testing.T, got answered) []string {
	t.Helper()
	spec, _ := got.body["spec"].(map[string]any)
	list, _ := spec["speakers"].([]any)
	if list == nil {
		t.Fatalf("no speakers in %s", got.raw)
	}
	out := []string{}
	for _, entry := range list {
		speaker := entry.(map[string]any)
		if speaker["delivery"] != nil {
			t.Errorf("a proposed speaker has a delivery note: %v", speaker)
		}
		out = append(out, strings.Join([]string{
			strings.TrimSuffix(strings.TrimSuffix(jsonText(speaker["slot"]), ".0"), ""),
			speaker["name"].(string), speaker["role"].(string), speaker["voice_id"].(string),
		}, " "))
	}
	return out
}

func jsonText(value any) string {
	out, _ := json.Marshal(value)
	return string(out)
}

func TestANewPodcastArrivesAtTheGateWithABrief(t *testing.T) {
	f := newFake()
	got := create(t, f, principal.MethodSession, nil)
	if got.status != http.StatusCreated {
		t.Fatalf("status %d: %s", got.status, got.raw)
	}
	want := map[string]any{
		"id": 100.0, "title": "The harbour hour", "status": "awaiting_brief", "spec_version": 1.0,
		"spec": map[string]any{
			"language": "en", "style": "conversational",
			// The deployment's two configured hosts, in their order.
			"speakers": []any{
				map[string]any{"slot": 0.0, "name": "Host", "role": "host", "voice_id": "kokoro:af_heart", "delivery": nil},
				map[string]any{"slot": 1.0, "name": "Guest", "role": "guest", "voice_id": "kokoro:am_adam", "delivery": nil},
			},
			"duration": map[string]any{"min_seconds": 20.0, "max_seconds": 30.0},
			"focus":    nil,
		},
		"transcript": nil, "has_audio": false, "duration_seconds": nil, "error": nil,
		"created_at": "2026-09-08T07:06:05Z", "thread_id": nil, "artifact_id": nil,
	}
	if !reflect.DeepEqual(got.body, want) {
		t.Errorf("the new podcast is\n %v\nand should be\n %v", got.body, want)
	}
	if got.header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type %q", got.header.Get("Content-Type"))
	}
	// What was stored is what was answered, and the text to draft from.
	row := f.podcasts[100]
	if row.Status != statusAwaitingBrief || f.owners[100] != callerID || row.ThreadID != nil || f.sources[100] != "Tides are caused by the moon." {
		t.Errorf("stored %+v with source %q", row, f.sources[100])
	}
	if stored, readable := readSpec(mustDecode(t, row.Spec)); !readable || stored == nil {
		t.Errorf("the stored brief cannot be read back: %s", row.Spec)
	}
	// Nothing is drafted until the brief is approved.
	if len(f.enqueued) != 0 {
		t.Errorf("a new podcast queued %v", f.enqueued)
	}
}

func mustDecode(t *testing.T, column []byte) any {
	t.Helper()
	value, err := decodeColumn(column)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestWhatTheRequestAsksForIsInTheBrief(t *testing.T) {
	f := newFake()
	got := create(t, f, principal.MethodSession, map[string]string{
		"title": `"  Six voices  "`, "speaker_count": `6`, "min_seconds": `600`, "max_seconds": `900`,
		"focus": `"  the spring tides "`, "thread_id": `12`,
	})
	if got.status != http.StatusCreated {
		t.Fatalf("status %d: %s", got.status, got.raw)
	}
	want := []string{
		"0 Host host kokoro:af_heart",
		"1 Guest guest kokoro:am_adam",
		// Past the configured hosts: a voice nobody has yet, and while one
		// gender has been heard less, that one.
		"2 Expert expert kokoro:am_michael",
		"3 Co-Host cohost kokoro:af_bella",
		"4 Narrator narrator kokoro:af_nicole",
		"5 Guest 5 guest kokoro:af_sarah",
	}
	if cast := speakers(t, got); !reflect.DeepEqual(cast, want) {
		t.Errorf("the cast is\n %v\nwant\n %v", cast, want)
	}
	spec := got.body["spec"].(map[string]any)
	if !reflect.DeepEqual(spec["duration"], map[string]any{"min_seconds": 600.0, "max_seconds": 900.0}) || spec["focus"] != "the spring tides" {
		t.Errorf("the brief is %v", spec)
	}
	if got.body["title"] != "Six voices" || got.body["thread_id"] != 12.0 {
		t.Errorf("title %q, thread %v", got.body["title"], got.body["thread_id"])
	}
}

// earlier is a podcast made before the one under test, holding a brief.
func earlier(id int64, brief string, at time.Time) PodcastOfOwnerRow {
	return podcast(id, func(row *PodcastOfOwnerRow) {
		row.Spec, row.CreatedAt, row.Status = text(brief), at, statusReady
	})
}

func briefIn(language string, voiceIDs ...string) string {
	speakers := []string{}
	for slot, voice := range voiceIDs {
		speakers = append(speakers, `{"slot": `+string(rune('0'+slot))+`, "name": "S", "role": "host", "voice_id": "`+voice+`"}`)
	}
	return `{"language": "` + language + `", "speakers": [` + strings.Join(speakers, ",") + `], "duration": {"min_seconds": 20, "max_seconds": 30}}`
}

func TestANewPodcastStartsFromTheCallersLastChoices(t *testing.T) {
	spring := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	for name, c := range map[string]struct {
		before []PodcastOfOwnerRow
		// others is which of those rows are another account's.
		others   []int64
		count    string
		language string
		cast     []string
	}{
		"the last language and voice, and a second voice that differs": {
			[]PodcastOfOwnerRow{earlier(1, briefIn("es", "kokoro:ef_dora"), spring)}, nil, "2",
			"es", []string{"0 Host host kokoro:ef_dora", "1 Guest guest kokoro:em_alex"},
		},
		"the newest brief, not the oldest": {
			[]PodcastOfOwnerRow{
				earlier(1, briefIn("es", "kokoro:ef_dora"), spring),
				earlier(2, briefIn("it", "kokoro:im_nicola", "kokoro:if_sara"), spring.Add(time.Hour)),
			}, nil, "2",
			"it", []string{"0 Host host kokoro:im_nicola", "1 Guest guest kokoro:if_sara"},
		},
		// English again, so the configured hosts speak it: last time's voice
		// for the first speaker, the first host not already used after it.
		"last time's voices before the configured hosts": {
			[]PodcastOfOwnerRow{earlier(1, briefIn("en-GB", "kokoro:bm_george"), spring)}, nil, "3",
			"en-GB", []string{"0 Host host kokoro:bm_george", "1 Guest guest kokoro:af_heart", "2 Expert expert kokoro:am_adam"},
		},
		// The service was changed since: the old brief's language has no
		// voice on this roster, so it is English and the hosts.
		"a language the roster no longer speaks": {
			[]PodcastOfOwnerRow{earlier(1, briefIn("de", "openai:coral", "openai:ash"), spring)}, nil, "2",
			"en", []string{"0 Host host kokoro:af_heart", "1 Guest guest kokoro:am_adam"},
		},
		// Kokoro has one French voice. Both speakers get it, and the user
		// sees that at the gate.
		"more speakers than the language has voices": {
			[]PodcastOfOwnerRow{earlier(1, briefIn("fr", "kokoro:ff_siwis"), spring)}, nil, "2",
			"fr", []string{"0 Host host kokoro:ff_siwis", "1 Guest guest kokoro:ff_siwis"},
		},
		// It was a 500 on the Python tier.
		"a last brief that cannot be read costs the defaults, not the podcast": {
			[]PodcastOfOwnerRow{earlier(1, `{"language": "es"}`, spring)}, nil, "2",
			"en", []string{"0 Host host kokoro:af_heart", "1 Guest guest kokoro:am_adam"},
		},
		// Isolation: what somebody else chose is not read, even when it is
		// the newest brief there is.
		"another account's choices are not the caller's": {
			[]PodcastOfOwnerRow{earlier(1, briefIn("es", "kokoro:ef_dora"), spring)}, []int64{1}, "1",
			"en", []string{"0 Host host kokoro:af_heart"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake(c.before...)
			for _, id := range c.others {
				f.owners[id] = strangerID
			}
			got := create(t, f, principal.MethodSession, map[string]string{"speaker_count": c.count})
			if got.status != http.StatusCreated {
				t.Fatalf("status %d: %s", got.status, got.raw)
			}
			if language := got.body["spec"].(map[string]any)["language"]; language != c.language {
				t.Errorf("the language is %v, want %s", language, c.language)
			}
			if cast := speakers(t, got); !reflect.DeepEqual(cast, c.cast) {
				t.Errorf("the cast is %v, want %v", cast, c.cast)
			}
		})
	}
}

func TestWithNoHostsConfiguredTheRostersOrderDecides(t *testing.T) {
	f := newFake()
	f.catalogue = kokoro(t)
	got := create(t, f, principal.MethodSession, nil)
	// The first voice, then the first of the other gender.
	want := []string{"0 Host host kokoro:am_adam", "1 Guest guest kokoro:af_bella"}
	if cast := speakers(t, got); !reflect.DeepEqual(cast, want) {
		t.Errorf("the cast is %v, want %v", cast, want)
	}
}

func TestABodyThatIsNotANewPodcastIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	long := func(length int) string { return `"` + strings.Repeat("x", length) + `"` }
	for name, c := range map[string]struct {
		fields map[string]string
		at     string
		says   string
	}{
		"no title":                 {map[string]string{"title": ""}, "body.title", "Field required"},
		"a blank title":            {map[string]string{"title": `"   "`}, "body.title", blankText},
		"a title that is a number": {map[string]string{"title": `7`}, "body.title", "Input should be a valid string"},
		"a title too long":         {map[string]string{"title": long(501)}, "body.title", "String should have at most 500 characters"},
		"a title holding a NUL":    {map[string]string{"title": `"a\u0000b"`}, "body.title", nulRefused},
		// The body does not say whose the podcast is. A client that still
		// sends a workspace is told, not silently obeyed or ignored.
		"a workspace id":             {map[string]string{"workspace_id": `7`}, "body.workspace_id", "Extra inputs are not permitted"},
		"nothing to draft from":      {map[string]string{"source_content": ""}, "body.source_content", "Field required"},
		"only spaces to draft from":  {map[string]string{"source_content": `" \n "`}, "body.source_content", blankText},
		"a thread that is a word":    {map[string]string{"thread_id": `"lounge"`}, "body.thread_id", notAWholeNumber},
		"no speakers":                {map[string]string{"speaker_count": `0`}, "body.speaker_count", "Input should be greater than or equal to 1"},
		"seven speakers":             {map[string]string{"speaker_count": `7`}, "body.speaker_count", "Input should be less than or equal to 6"},
		"a speaker count in quotes":  {map[string]string{"speaker_count": `"2"`}, "body.speaker_count", notAWholeNumber},
		"a speaker count of null":    {map[string]string{"speaker_count": `null`}, "body.speaker_count", notAWholeNumber},
		"too short":                  {map[string]string{"min_seconds": `14`}, "body.min_seconds", "Input should be greater than or equal to 15"},
		"longer than a day":          {map[string]string{"max_seconds": `86401`}, "body.max_seconds", "Input should be less than or equal to 86400"},
		"a minimum past the maximum": {map[string]string{"min_seconds": `60`}, "body.max_seconds", durationReversed},
		"a focus too long":           {map[string]string{"focus": long(2001)}, "body.focus", "String should have at most 2000 characters"},
		"a focus that is a list":     {map[string]string{"focus": `["tides"]`}, "body.focus", "Input should be a valid string"},
		// A misspelling used to be ignored, and the podcast made with two.
		"a key nobody declared": {map[string]string{"speakers_count": `4`}, "body.speakers_count", extraKey},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake()
			got := create(t, f, principal.MethodSession, c.fields)
			if got.status != http.StatusUnprocessableEntity {
				t.Fatalf("status %d: %s", got.status, got.raw)
			}
			if faults := got.faults(); len(faults) != 1 || faults[c.at] != c.says {
				t.Errorf("the faults are %v, want %q at %s", faults, c.says, c.at)
			}
			if len(f.podcasts) != 0 {
				t.Errorf("a podcast was made: %v", f.podcasts)
			}
		})
	}

	// Every fault at once, so a form can mark them all.
	got := create(t, newFake(), principal.MethodSession, map[string]string{"title": "", "speaker_count": `9`, "focus": `1`})
	if faults := got.faults(); len(faults) != 3 {
		t.Errorf("three faults were sent and %v came back", faults)
	}

	// And a body that is not an object at all.
	for _, body := range []string{`[]`, `"a podcast"`, `7`} {
		got := send(t, newFake(), principal.MethodSession, http.MethodPost, createPath, body)
		if got.status != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d", body, got.status)
		}
	}
}

func TestOptionalFieldsMayBeNullOrBlank(t *testing.T) {
	f := newFake()
	got := create(t, f, principal.MethodSession, map[string]string{"thread_id": `null`, "focus": `"   "`})
	if got.status != http.StatusCreated || got.body["thread_id"] != nil || got.body["spec"].(map[string]any)["focus"] != nil {
		t.Errorf("status %d: %s", got.status, got.raw)
	}
	// A whole number written as 2.0 is a whole number: JSON has one kind.
	got = create(t, newFake(), principal.MethodSession, map[string]string{"speaker_count": `1.0`})
	if got.status != http.StatusCreated || len(speakers(t, got)) != 1 {
		t.Errorf("status %d: %s", got.status, got.raw)
	}
}

// Anyone signed in may make a podcast, and it is theirs: the owner is the
// caller, whatever the body says and however they signed in.
func TestANewPodcastIsItsMakers(t *testing.T) {
	for _, c := range []struct {
		name   string
		asker  string
		method principal.Method
	}{
		{"a person", callerID, principal.MethodSession},
		{"an API key", callerID, principal.MethodPersonalAccessToken},
		{"an agent", callerID, principal.MethodAgent},
		{"another account", strangerID, principal.MethodSession},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFake()
			f.asker = c.asker
			got := create(t, f, c.method, nil)
			if got.status != http.StatusCreated {
				t.Fatalf("status %d: %s", got.status, got.raw)
			}
			if len(f.podcasts) != 1 || f.owners[100] != c.asker {
				t.Errorf("podcasts afterwards: %v, owned by %v", f.podcasts, f.owners)
			}
		})
	}
}

// A deployment with no speech service cannot propose a brief, and says so
// before anything is written.
func TestWithNoVoicesNothingIsCreated(t *testing.T) {
	f := newFake()
	f.catalogueErr = errNoDefaultVoice
	got := create(t, f, principal.MethodSession, nil)
	if got.status < 500 || len(f.podcasts) != 0 {
		t.Errorf("answered %d with %d podcasts made: %s", got.status, len(f.podcasts), got.raw)
	}
}

// The thread is where the podcast's player appears. It was never checked:
// any id was stored, somebody else's included. Another account's thread is
// told what a thread that does not exist is told.
func TestAPodcastsThreadMustBeTheCallersOwn(t *testing.T) {
	for name, c := range map[string]struct {
		thread string
		status int
	}{
		"one of the caller's threads":     {"12", 201},
		"another account's thread":        {"13", 404},
		"a thread that does not exist":    {"999", 404},
		"a number no thread can have":     {"99999999999", 404},
		"a negative number":               {"-1", 404},
		"a whole number written as 12.0":  {"12.0", 201},
		"a thread in quotes is not an id": {`"12"`, 422},
	} {
		f := newFake()
		got := create(t, f, principal.MethodSession, map[string]string{"thread_id": c.thread})
		if got.status != c.status {
			t.Errorf("%s: status %d, want %d: %s", name, got.status, c.status, got.raw)
		}
		if c.status == http.StatusNotFound && (got.message() != threadAbsent || len(f.podcasts) != 0) {
			t.Errorf("%s: told %q, podcasts %v", name, got.message(), f.podcasts)
		}
	}
}
