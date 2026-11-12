// Package voices is the catalogue of voices a podcast can be spoken in: which
// voices the configured speech service has, which languages those cover, which
// of them the deployment chose as its hosts, and a short audible sample of
// each.
//
// # Why it is in this module
//
// Two things read the roster: the API tier's podcast routes, and the
// artifact specialist when it opens a podcast and proposes its brief
// (deliverables). The specialist runs in every tier that runs the agent,
// so the roster it reads has to be importable by all of them, and one copy
// is one idea of which voices exist.
//
// # Where a voice comes from
//
// One setting names the speech service (Settings.Service, the Python tier's
// TTS_SERVICE): `openai/openrouter-kokoro-82m-tts`, `openai/openai-tts-1-hd`.
// What follows the first slash is the model name the model proxy is sent. The
// service also decides the roster -- the list of voices on offer -- and there
// is exactly one rule per roster (settings.go):
//
//   - Kokoro, Azure and Vertex have a fixed roster, written down here as data
//     (data_*.go). Nothing is asked of anybody.
//   - An OpenAI-style model has whichever of a known set of voices the proxy
//     says that model speaks: gpt-4o-mini-tts has thirteen, tts-1 nine, Grok
//     Voice five of its own. That is asked of the proxy (`GET /model/info`),
//     because it is the proxy that knows which model a name is pointed at
//     today.
//
// # What this does better than the Python catalogue it replaces
//
//   - No guessing. When the proxy could not be asked, the Python catalogue
//     guessed the roster from the model's name ("tts-1" in it means nine
//     voices) and kept the guess for the life of the process. Here an answer
//     that cannot be had is an error the caller is told about, and the next
//     request asks again.
//   - A wrong setting stops the process. A service with no known roster, a
//     service that would run a model on this box, a missing proxy address, a
//     host voice the roster does not have: each is refused by New, naming
//     the setting, instead of surfacing as a 500 on the first podcast.
//   - The proxy's answer is refreshed. It was read once per process, so a
//     model re-pointed in the proxy needed every API process restarted.
//   - The proxy is asked only when its answer is used. The Python catalogue
//     asked it about any `openai/…` service, a Kokoro one included, and then
//     did not use the answer.
//   - A sample is kept where every replica can find it (samples.go), so a
//     voice is synthesised once for the deployment rather than once per
//     container and again after each restart.
//
// No model is ever loaded into this process, and no provider is ever called
// directly: the catalogue question and the sample both go to the proxy.
package voices

import "strings"

// AnyLanguage marks a voice that speaks whatever language its text is in, as
// the OpenAI-style voices do. It matches every language asked for.
const AnyLanguage = "*"

// Gender is how a voice is heard. It exists so that two speakers can be given
// voices a listener will tell apart.
type Gender string

const (
	Male    Gender = "male"
	Female  Gender = "female"
	Neutral Gender = "neutral"
)

// Voice is one voice a speaker can be given.
type Voice struct {
	// ID is what a brief stores on a speaker: the roster's name, a colon and
	// the voice's own name ("kokoro:af_heart"). It is stable; briefs written
	// months ago hold it.
	ID string
	// Language is a BCP-47 tag ("en-US", "pt-BR", "zh"), or AnyLanguage.
	Language string
	// DisplayName is what the voice picker shows.
	DisplayName string
	Gender      Gender
	// Native is what the speech service is sent as `voice`: the bare name for
	// most, and for Vertex an object naming a locale and a voice. It is kept
	// apart from ID so that the id can stay put when a provider renames.
	Native any
}

// Speaks reports whether the voice can be given text in the language. Only
// the primary subtag is compared: a "pt-BR" voice speaks "pt", and an "en-GB"
// voice is offered for "en-US", because a different accent is a choice the
// user can make and no voice at all is not.
func (v Voice) Speaks(language string) bool {
	if v.Language == AnyLanguage {
		return true
	}
	return primary(v.Language) == primary(language)
}

// primary is a tag's first subtag, lowered: "en" for "EN-us".
func primary(language string) string {
	first, _, _ := strings.Cut(language, "-")
	return strings.ToLower(strings.TrimSpace(first))
}
