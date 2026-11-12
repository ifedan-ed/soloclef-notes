package podcasts

import (
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// rendered decodes a column, reads it, and gives back the JSON it would be
// answered as: "null" for none, and "unreadable" for one that is refused.
func rendered(t *testing.T, column string, read func(any) (*pythonjson.Object, bool)) string {
	t.Helper()
	value, err := decodeColumn([]byte(column))
	if err != nil {
		t.Fatalf("%s: %v", column, err)
	}
	object, readable := read(value)
	if !readable {
		return "unreadable"
	}
	if object == nil {
		return "null"
	}
	out, err := pythonjson.Marshal(object)
	if err != nil {
		t.Fatalf("%s: %v", column, err)
	}
	return string(out)
}

func brief(change string) string {
	base := map[string]string{
		"language": `"en"`, "style": `"interview"`,
		"speakers": `[{"slot": 0, "name": "Mara", "role": "host", "voice_id": "v1", "delivery": null}]`,
		"duration": `{"min_seconds": 20, "max_seconds": 30}`, "focus": `null`,
	}
	if name, value, found := strings.Cut(change, "="); found {
		if value == "" {
			delete(base, name)
		} else {
			base[name] = value
		}
	}
	parts := []string{}
	for _, name := range []string{"focus", "duration", "speakers", "style", "language", "mood"} {
		if value, present := base[name]; present {
			parts = append(parts, `"`+name+`": `+value)
		}
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func TestABriefIsRenderedWholeOrNotAtAll(t *testing.T) {
	const whole = `{"language":"en","style":"interview","speakers":[{"slot":0,"name":"Mara","role":"host","voice_id":"v1","delivery":null}],"duration":{"min_seconds":20,"max_seconds":30},"focus":null}`
	speaker := func(change string) string {
		return `speakers=[{"slot": 0, "name": "Mara", "role": "host", "voice_id": "v1"` + change + `}]`
	}
	for name, c := range map[string]struct{ column, want string }{
		// Whatever order jsonb kept the keys in, the answer's is the contract's.
		"a whole brief":            {brief(""), whole},
		"no brief":                 {`null`, "null"},
		"no style named":           {brief("style="), strings.Replace(whole, "interview", "conversational", 1)},
		"language in capitals":     {brief(`language=" PT-BR "`), strings.Replace(whole, `"en"`, `"pt-BR"`, 1)},
		"a focus":                  {brief(`focus="  the tides "`), strings.Replace(whole, `"focus":null`, `"focus":"the tides"`, 1)},
		"a focus of spaces":        {brief(`focus="   "`), whole},
		"a whole number as 300.0":  {brief(`duration={"min_seconds": 20.0, "max_seconds": 30}`), whole},
		"a brief sized in minutes": {brief(`duration={"min_minutes": 5, "max_minutes": 10}`), strings.Replace(whole, `"min_seconds":20,"max_seconds":30`, `"min_seconds":300,"max_seconds":600`, 1)},
		"a padded name":            {brief(`speakers=[{"slot": 0, "name": " Mara ", "role": "host", "voice_id": " v1 "}]`), whole},
		"a delivery note":          {brief(speaker(`, "delivery": "dry"`)), strings.Replace(whole, `"delivery":null`, `"delivery":"dry"`, 1)},

		"an empty object":            {`{}`, "unreadable"},
		"a list":                     {`[1]`, "unreadable"},
		"no language":                {brief("language="), "unreadable"},
		"a language that is not one": {brief(`language="english"`), "unreadable"},
		"a style nobody defined":     {brief(`style="rap battle"`), "unreadable"},
		"no speakers":                {brief("speakers=[]"), "unreadable"},
		"seven speakers":             {brief(`speakers=[` + strings.Repeat(`{"slot": 0, "name": "M", "role": "host", "voice_id": "v"},`, 6) + `{"slot": 0, "name": "M", "role": "host", "voice_id": "v"}]`), "unreadable"},
		"two speakers in one slot":   {brief(`speakers=[{"slot": 1, "name": "A", "role": "host", "voice_id": "v"}, {"slot": 1.0, "name": "B", "role": "guest", "voice_id": "w"}]`), "unreadable"},
		"a monologue for two":        {strings.Replace(brief(`speakers=[{"slot": 0, "name": "A", "role": "host", "voice_id": "v"}, {"slot": 1, "name": "B", "role": "guest", "voice_id": "w"}]`), "interview", "monologue", 1), "unreadable"},
		"a blank name":               {brief(`speakers=[{"slot": 0, "name": "  ", "role": "host", "voice_id": "v1"}]`), "unreadable"},
		"a role nobody defined":      {brief(`speakers=[{"slot": 0, "name": "Mara", "role": "villain", "voice_id": "v1"}]`), "unreadable"},
		"a slot that is text":        {brief(`speakers=[{"slot": "0", "name": "Mara", "role": "host", "voice_id": "v1"}]`), "unreadable"},
		"a negative slot":            {brief(`speakers=[{"slot": -1, "name": "Mara", "role": "host", "voice_id": "v1"}]`), "unreadable"},
		"a key nobody declared":      {brief(`mood="bright"`), "unreadable"},
		"no duration":                {brief("duration="), "unreadable"},
		"a duration out of order":    {brief(`duration={"min_seconds": 30, "max_seconds": 20}`), "unreadable"},
		"a duration too short":       {brief(`duration={"min_seconds": 14, "max_seconds": 30}`), "unreadable"},
		"a duration past a day":      {brief(`duration={"min_seconds": 20, "max_seconds": 86401}`), "unreadable"},
		"minutes past a day":         {brief(`duration={"min_minutes": 5, "max_minutes": 1441}`), "unreadable"},
		"a fraction of a second":     {brief(`duration={"min_seconds": 20.5, "max_seconds": 30}`), "unreadable"},
	} {
		if got := rendered(t, c.column, readSpec); got != c.want {
			t.Errorf("%s:\n got  %s\n want %s", name, got, c.want)
		}
	}
}

func TestATranscriptInEitherOfItsTwoShapes(t *testing.T) {
	const one = `{"turns":[{"speaker":0,"text":"Welcome back."}]}`
	for name, c := range map[string]struct{ column, want string }{
		"the current shape":           {`{"turns": [{"speaker": 0, "text": " Welcome back. "}]}`, one},
		"a turn a model decorated":    {`{"turns": [{"speaker": 0, "text": "Welcome back.", "emotion": "warm"}]}`, one},
		"the shape before the rework": {`[{"speaker_id": 0, "dialog": "Welcome back."}]`, one},
		"none":                        {`null`, "null"},
		"an old row with no turns":    {`[]`, "null"},
		"no turns":                    {`{"turns": []}`, "unreadable"},
		"a blank line":                {`{"turns": [{"speaker": 0, "text": "  "}]}`, "unreadable"},
		"a turn with no speaker":      {`{"turns": [{"text": "Welcome back."}]}`, "unreadable"},
		"an old turn under new names": {`[{"speaker": 0, "text": "Welcome back."}]`, "unreadable"},
		"a key beside the turns":      {`{"turns": [{"speaker": 0, "text": "x"}], "title": "y"}`, "unreadable"},
		"text":                        {`"a transcript"`, "unreadable"},
	} {
		if got := rendered(t, c.column, readTranscript); got != c.want {
			t.Errorf("%s:\n got  %s\n want %s", name, got, c.want)
		}
	}
}
