package podcasts

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

const briefPath = "/api/v1/podcasts/41/spec"

// editedBrief is what the web app's form sends back for goodBrief with the
// length and the second speaker's name changed. Like the form, it has no
// `delivery` key: the form does not know the field.
const editedBrief = `{"language": "en-GB", "style": "interview", "speakers": [` +
	`{"slot": 0, "name": "Mara", "role": "host", "voice_id": "kokoro:af_bella"}, ` +
	`{"slot": 1, "name": "Teodor", "role": "expert", "voice_id": "kokoro:am_adam"}], ` +
	`"duration": {"min_seconds": 120, "max_seconds": 180}, "focus": "  neap tides "}`

// edit sends a brief for podcast 41 against the version given.
func edit(t *testing.T, f *fake, method principal.Method, brief, version string) answered {
	t.Helper()
	return send(t, f, method, http.MethodPatch, briefPath, `{"spec": `+brief+`, "expected_version": `+version+`}`)
}

func TestAnEditedBriefReplacesTheStoredOneAndMovesTheVersionOn(t *testing.T) {
	f := newFake(podcast(41, nil))
	got := edit(t, f, principal.MethodSession, editedBrief, "1")
	if got.status != http.StatusOK {
		t.Fatalf("status %d: %s", got.status, got.raw)
	}
	want := map[string]any{
		"language": "en-GB", "style": "interview",
		"speakers": []any{
			map[string]any{"slot": 0.0, "name": "Mara", "role": "host", "voice_id": "kokoro:af_bella", "delivery": nil},
			// The delivery note casting wrote for this speaker is still
			// there, although the form sent none: same slot, same voice.
			map[string]any{"slot": 1.0, "name": "Teodor", "role": "expert", "voice_id": "kokoro:am_adam", "delivery": "dry"},
		},
		"duration": map[string]any{"min_seconds": 120.0, "max_seconds": 180.0},
		"focus":    "neap tides",
	}
	if !reflect.DeepEqual(got.body["spec"], want) || got.body["spec_version"] != 2.0 || got.body["status"] != "awaiting_brief" {
		t.Errorf("answered version %v, brief\n %v\nwant\n %v", got.body["spec_version"], got.body["spec"], want)
	}
	// What is stored is the whole brief in the contract's order, which is
	// what the worker reads.
	const stored = `{"language":"en-GB","style":"interview","speakers":[` +
		`{"slot":0,"name":"Mara","role":"host","voice_id":"kokoro:af_bella","delivery":null},` +
		`{"slot":1,"name":"Teodor","role":"expert","voice_id":"kokoro:am_adam","delivery":"dry"}],` +
		`"duration":{"min_seconds":120,"max_seconds":180},"focus":"neap tides"}`
	if row := f.podcasts[41]; string(row.Spec) != stored || row.SpecVersion != 2 {
		t.Errorf("stored version %d:\n %s\nwant\n %s", row.SpecVersion, row.Spec, stored)
	}
}

func TestADeliveryNoteFollowsItsSpeakerAndItsVoice(t *testing.T) {
	speaker := func(rest string) string {
		return `{"language": "en", "speakers": [` +
			`{"slot": 0, "name": "Mara", "role": "host", "voice_id": "kokoro:af_bella"}, ` +
			`{"slot": 1, "name": "Teo", "role": "guest"` + rest + `}], ` +
			`"duration": {"min_seconds": 20, "max_seconds": 30}}`
	}
	for name, c := range map[string]struct {
		second string
		want   any
	}{
		"not mentioned, same voice: kept":      {`, "voice_id": "kokoro:am_adam"`, "dry"},
		"named as null: cleared":               {`, "voice_id": "kokoro:am_adam", "delivery": null`, nil},
		"named: replaced":                      {`, "voice_id": "kokoro:am_adam", "delivery": "brisk"`, "brisk"},
		"another voice: the note goes":         {`, "voice_id": "kokoro:am_michael"`, nil},
		"another voice with a note of its own": {`, "voice_id": "kokoro:am_michael", "delivery": "slow"`, "slow"},
	} {
		f := newFake(podcast(41, nil))
		got := edit(t, f, principal.MethodSession, speaker(c.second), "1")
		if got.status != http.StatusOK {
			t.Fatalf("%s: status %d: %s", name, got.status, got.raw)
		}
		second := got.body["spec"].(map[string]any)["speakers"].([]any)[1].(map[string]any)
		if second["delivery"] != c.want {
			t.Errorf("%s: the note is %v, want %v", name, second["delivery"], c.want)
		}
	}
}

func TestABriefIsEditedOnlyAtTheGateAndOnlyAtTheVersionSeen(t *testing.T) {
	for name, c := range map[string]struct {
		row     func(*PodcastOfOwnerRow)
		version string
		message string
	}{
		"already drafting":       {func(row *PodcastOfOwnerRow) { row.Status = statusDrafting }, "1", notAtTheGate + "drafting"},
		"a finished episode":     {func(row *PodcastOfOwnerRow) { row.Status = statusReady }, "1", notAtTheGate + "ready"},
		"cancelled":              {func(row *PodcastOfOwnerRow) { row.Status = statusCancelled }, "1", notAtTheGate + "cancelled"},
		"an older version":       {func(row *PodcastOfOwnerRow) { row.SpecVersion = 3 }, "1", "spec version conflict: expected 1, current is 3"},
		"a version from nowhere": {nil, "8", "spec version conflict: expected 8, current is 1"},
	} {
		f := newFake(podcast(41, c.row))
		before := f.podcasts[41]
		got := edit(t, f, principal.MethodSession, editedBrief, c.version)
		// 409 is what makes the web app reload the brief.
		if got.status != http.StatusConflict || got.message() != c.message {
			t.Errorf("%s: %d %q, want 409 %q", name, got.status, got.message(), c.message)
		}
		if after := f.podcasts[41]; string(after.Spec) != string(before.Spec) || after.SpecVersion != before.SpecVersion {
			t.Errorf("%s: the brief was written anyway", name)
		}
	}
}

// Two people save the same brief at once. Both read version 1; one write
// lands first. The Python route let the second overwrite the first.
func TestAnEditThatLosesARaceIsAConflictAndOverwritesNothing(t *testing.T) {
	theirs := `{"language":"es"}`
	for name, meanwhile := range map[string]func(*fake){
		"another edit landed": func(f *fake) {
			row := f.podcasts[41]
			row.Spec, row.SpecVersion = text(theirs), 2
			f.podcasts[41] = row
		},
		"the brief was approved": func(f *fake) {
			row := f.podcasts[41]
			row.Status = statusDrafting
			f.podcasts[41] = row
		},
		"the podcast was deleted": func(f *fake) { delete(f.podcasts, 41) },
	} {
		f := newFake(podcast(41, nil))
		f.meanwhile = meanwhile
		got := edit(t, f, principal.MethodSession, editedBrief, "1")
		if got.status != http.StatusConflict || got.message() != changedMeanwhile {
			t.Errorf("%s: %d %q", name, got.status, got.message())
		}
		if row, still := f.podcasts[41]; still && strings.Contains(string(row.Spec), "Teodor") {
			t.Errorf("%s: the losing edit was written: %s", name, row.Spec)
		}
	}
}

// A brief's voices are the roster's question, and it is asked before the
// brief is stored: afterwards the next thing to find out is the render.
func TestAnEditedBriefMustBeSpeakable(t *testing.T) {
	with := func(language, first, second string) string {
		return `{"language": "` + language + `", "speakers": [` +
			`{"slot": 0, "name": "Mara", "role": "host", "voice_id": "` + first + `"}, ` +
			`{"slot": 4, "name": "Teo", "role": "guest", "voice_id": "` + second + `"}], ` +
			`"duration": {"min_seconds": 20, "max_seconds": 30}}`
	}
	for name, c := range map[string]struct {
		brief  string
		faults map[string]string
	}{
		"a voice nobody has": {with("en", "kokoro:af_bella", "kokoro:am_nobody"),
			map[string]string{"body.spec.speakers.1.voice_id": voiceAbsent}},
		"a voice of another service": {with("en", "openai:coral", "kokoro:am_adam"),
			map[string]string{"body.spec.speakers.0.voice_id": voiceAbsent}},
		"a voice that does not speak the language": {with("es", "kokoro:ef_dora", "kokoro:am_adam"),
			map[string]string{"body.spec.speakers.1.voice_id": "This voice does not speak es"}},
		"both speakers wrong": {with("it", "kokoro:ef_dora", "kokoro:xx"),
			map[string]string{"body.spec.speakers.0.voice_id": "This voice does not speak it", "body.spec.speakers.1.voice_id": voiceAbsent}},
		"a language the service has no voice for": {with("de", "kokoro:af_bella", "kokoro:am_adam"),
			map[string]string{"body.spec.language": "The speech service has no voice for de"}},
	} {
		f := newFake(podcast(41, nil))
		got := edit(t, f, principal.MethodSession, c.brief, "1")
		if got.status != http.StatusUnprocessableEntity || !reflect.DeepEqual(got.faults(), c.faults) {
			t.Errorf("%s: %d %v, want 422 %v", name, got.status, got.faults(), c.faults)
		}
		if f.podcasts[41].SpecVersion != 1 {
			t.Errorf("%s: the brief was stored", name)
		}
	}

	// A British voice for an American brief is an accent, not a mistake;
	// and one voice may play two speakers.
	f := newFake(podcast(41, nil))
	if got := edit(t, f, principal.MethodSession, with("en-US", "kokoro:bf_emma", "kokoro:bf_emma"), "1"); got.status != http.StatusOK {
		t.Errorf("status %d: %s", got.status, got.raw)
	}
	// A roster that speaks anything takes any language.
	f = newFake(podcast(41, nil))
	f.catalogue = openAI(t, "coral", "ash")
	if got := edit(t, f, principal.MethodSession, with("sw", "openai:coral", "openai:ash"), "1"); got.status != http.StatusOK {
		t.Errorf("status %d: %s", got.status, got.raw)
	}
}

func TestABodyThatIsNotABriefEditIsRefused(t *testing.T) {
	change := func(old, new string) string { return strings.Replace(editedBrief, old, new, 1) }
	for name, c := range map[string]struct {
		body   string
		faults map[string]string
	}{
		"no brief":   {`{"expected_version": 1}`, map[string]string{"body.spec": "Field required"}},
		"no version": {`{"spec": ` + editedBrief + `}`, map[string]string{"body.expected_version": "Field required"}},
		"a version of zero": {`{"spec": ` + editedBrief + `, "expected_version": 0}`,
			map[string]string{"body.expected_version": "Input should be greater than or equal to 1"}},
		"a version in quotes": {`{"spec": ` + editedBrief + `, "expected_version": "1"}`,
			map[string]string{"body.expected_version": notAWholeNumber}},
		"a key beside the two": {`{"spec": ` + editedBrief + `, "expected_version": 1, "force": true}`,
			map[string]string{"body.force": extraKey}},
		"a brief that is a list": {`{"spec": [], "expected_version": 1}`, map[string]string{"body.spec": notAnObject}},
		"a key the brief does not have": {`{"spec": ` + change(`"style"`, `"mood": "bright", "style"`) + `, "expected_version": 1}`,
			map[string]string{"body.spec.mood": extraKey}},
		"a style nobody defined": {`{"spec": ` + change(`"interview"`, `"rap battle"`) + `, "expected_version": 1}`,
			map[string]string{"body.spec.style": "Input should be 'conversational', 'interview', 'debate', 'monologue' or 'narrative'"}},
		"a language that is not one": {`{"spec": ` + change(`"en-GB"`, `"british"`) + `, "expected_version": 1}`,
			map[string]string{"body.spec.language": notALanguage}},
		"a monologue for two": {`{"spec": ` + change(`"interview"`, `"monologue"`) + `, "expected_version": 1}`,
			map[string]string{"body.spec.speakers": monologueOfMany}},
		"two speakers in one slot": {`{"spec": ` + change(`"slot": 1`, `"slot": 0`) + `, "expected_version": 1}`,
			map[string]string{"body.spec.speakers.1.slot": slotTaken}},
		"a blank name and a role nobody defined": {`{"spec": ` + change(`"name": "Teodor", "role": "expert"`, `"name": " ", "role": "villain"`) + `, "expected_version": 1}`,
			map[string]string{
				"body.spec.speakers.1.name": blankText,
				"body.spec.speakers.1.role": "Input should be 'host', 'cohost', 'guest', 'expert' or 'narrator'",
			}},
		"a length out of order": {`{"spec": ` + change(`"max_seconds": 180`, `"max_seconds": 60`) + `, "expected_version": 1}`,
			map[string]string{"body.spec.duration.max_seconds": durationReversed}},
		// Minutes are how old rows are stored, not something to send.
		"a length in minutes": {`{"spec": ` + change(`{"min_seconds": 120, "max_seconds": 180}`, `{"min_minutes": 2, "max_minutes": 3}`) + `, "expected_version": 1}`,
			map[string]string{
				"body.spec.duration.min_minutes": extraKey, "body.spec.duration.max_minutes": extraKey,
				"body.spec.duration.min_seconds": "Field required", "body.spec.duration.max_seconds": "Field required",
			}},
		"no speakers": {`{"spec": {"language": "en", "speakers": [], "duration": {"min_seconds": 20, "max_seconds": 30}}, "expected_version": 1}`,
			map[string]string{"body.spec.speakers": "List should have at least 1 item after validation, not 0"}},
	} {
		f := newFake(podcast(41, nil))
		got := send(t, f, principal.MethodSession, http.MethodPatch, briefPath, c.body)
		if got.status != http.StatusUnprocessableEntity || !reflect.DeepEqual(got.faults(), c.faults) {
			t.Errorf("%s: %d\n got  %v\n want %v", name, got.status, got.faults(), c.faults)
		}
		if f.podcasts[41].SpecVersion != 1 {
			t.Errorf("%s: the brief was stored", name)
		}
	}
}

// A brief is edited by the podcast's owner and by nobody else: another
// account is told the podcast does not exist, and the brief is not touched.
func TestOnlyItsOwnerEditsABrief(t *testing.T) {
	for _, c := range []struct {
		name    string
		asker   string
		method  principal.Method
		status  int
		message string
	}{
		{"the owner", callerID, principal.MethodSession, 200, ""},
		{"the owner by an API key", callerID, principal.MethodPersonalAccessToken, 200, ""},
		{"another account", strangerID, principal.MethodSession, 404, podcastAbsent},
		{"another account by an agent", strangerID, principal.MethodAgent, 404, podcastAbsent},
	} {
		f := newFake(podcast(41, nil))
		f.asker = c.asker
		got := edit(t, f, c.method, editedBrief, "1")
		if got.status != c.status || (c.message != "" && got.message() != c.message) {
			t.Errorf("%s: %d %q, want %d %q", c.name, got.status, got.message(), c.status, c.message)
		}
		if written := f.podcasts[41].SpecVersion == 2; written != (c.status == http.StatusOK) {
			t.Errorf("%s: version %d afterwards", c.name, f.podcasts[41].SpecVersion)
		}
	}
	// A podcast that is not there, and an id that is not a number.
	if got := send(t, newFake(), principal.MethodSession, http.MethodPatch, briefPath, `{"spec": `+editedBrief+`, "expected_version": 1}`); got.status != http.StatusNotFound {
		t.Errorf("no such podcast: %d", got.status)
	}
	if got := send(t, newFake(), principal.MethodSession, http.MethodPatch, "/api/v1/podcasts/latest/spec", `{}`); got.status != http.StatusUnprocessableEntity {
		t.Errorf("an id that is a word: %d", got.status)
	}
}

// A brief nobody can read is exactly the brief that needs replacing.
func TestABriefThatCannotBeReadCanBeReplaced(t *testing.T) {
	f := newFake(podcast(41, func(row *PodcastOfOwnerRow) { row.Spec = text(brokenBrief) }))
	got := edit(t, f, principal.MethodSession, editedBrief, "1")
	if got.status != http.StatusOK || got.body["spec"] == nil {
		t.Errorf("status %d: %s", got.status, got.raw)
	}
}
