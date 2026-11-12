// Package speech voices a line of text through the model proxy's speech
// route, and says what a brief's voice id is to the service that speaks it.
//
// The setting it reads is the one the API tier reads
// (SOLOCLEF_API_TTS_SERVICE, here SOLOCLEF_WORKER_TTS_SERVICE): a roster and
// the proxy's model name, `openai/gpt-4o-mini-tts`. The API accepts a
// brief's voices against that roster, so the two settings must be the same
// value; a brief whose voice belongs to another roster is refused here by
// name rather than spoken in some other voice.
package speech

import (
	"fmt"
	"strings"
)

// Roster is which list of voices a service draws on. It is also the prefix
// of every voice id in that list ("kokoro:af_heart").
type Roster string

const (
	Kokoro Roster = "kokoro"
	OpenAI Roster = "openai"
	Azure  Roster = "azure"
	Vertex Roster = "vertex_ai"
)

// Service is the speech setting, read.
type Service struct {
	// Roster is the list the brief's voices come from.
	Roster Roster
	// Model is what the proxy is sent as `model`.
	Model string
}

// ReadService reads `<roster>/<model>`, with the API tier's rules
// (internal/voices/settings.go): a hosted Kokoro speaks OpenAI's API, so it
// is known by its model name; every other roster by the first segment.
func ReadService(setting string) (Service, error) {
	name := strings.TrimSpace(setting)
	prefix, model, found := strings.Cut(name, "/")
	if !found || prefix == "" || model == "" {
		return Service{}, fmt.Errorf("speech: the service %q must be <roster>/<the proxy's model name>, for example openai/gpt-4o-mini-tts", name)
	}
	if strings.EqualFold(prefix, "local") {
		return Service{}, fmt.Errorf("speech: the service %q would run a model on this box; name one the proxy serves", name)
	}
	service := Service{Model: model}
	switch {
	case strings.Contains(strings.ToLower(model), "kokoro"):
		service.Roster = Kokoro
	case strings.EqualFold(prefix, string(OpenAI)):
		service.Roster = OpenAI
	case strings.EqualFold(prefix, string(Azure)):
		service.Roster = Azure
	case strings.EqualFold(prefix, string(Vertex)):
		service.Roster = Vertex
	default:
		return Service{}, fmt.Errorf("speech: no voices are known for %q in the service %q: the first segment must be openai, azure or vertex_ai", prefix, name)
	}
	return service, nil
}

// TakesInstructions reports whether the model takes a speaking-style
// instruction. gpt-4o-mini-tts does; tts-1 refuses a request that carries
// one, and Grok Voice ignores it.
func (s Service) TakesInstructions() bool {
	return strings.Contains(strings.ToLower(s.Model), "4o-mini-tts")
}

// vertexVoices is the one roster whose native reference is not the name in
// the id: Vertex names a voice by locale and name, and spells the British
// locale "en-UK" where the id and the language tag use "en-GB". It is the
// API tier's data_vertex.go, which offers exactly these six.
var vertexVoices = map[string]map[string]string{
	"en-US-Studio-O": {"languageCode": "en-US", "name": "en-US-Studio-O"},
	"en-US-Studio-M": {"languageCode": "en-US", "name": "en-US-Studio-M"},
	"en-GB-Studio-A": {"languageCode": "en-UK", "name": "en-UK-Studio-A"},
	"en-GB-Studio-B": {"languageCode": "en-UK", "name": "en-UK-Studio-B"},
	"en-AU-Studio-A": {"languageCode": "en-AU", "name": "en-AU-Studio-A"},
	"en-AU-Studio-B": {"languageCode": "en-AU", "name": "en-AU-Studio-B"},
}

// VoiceError is a brief's voice the configured service cannot speak. It is
// the one speech failure a person can act on (by choosing another voice),
// so it is a type of its own.
type VoiceError struct {
	VoiceID string
	Reason  string
}

func (e *VoiceError) Error() string { return "speech: " + e.Reason }

// Voice is what the service is sent as `voice` for a brief's voice id.
//
// The id must belong to the configured roster. A brief kept from before
// the service was changed names a voice the new one does not have; it is
// refused with both names, because speaking it in a voice nobody chose is
// what the Python worker used to do after a TTS_SERVICE change.
func (s Service) Voice(voiceID string) (any, error) {
	roster, name, found := strings.Cut(voiceID, ":")
	if !found || name == "" {
		return nil, &VoiceError{VoiceID: voiceID, Reason: fmt.Sprintf("%q is not a voice id (roster:name)", voiceID)}
	}
	if Roster(roster) != s.Roster {
		return nil, &VoiceError{VoiceID: voiceID, Reason: fmt.Sprintf("the voice %q belongs to %s, and this server speaks with %s (%s)", voiceID, roster, s.Roster, s.Model)}
	}
	if s.Roster == Vertex {
		native, known := vertexVoices[name]
		if !known {
			return nil, &VoiceError{VoiceID: voiceID, Reason: fmt.Sprintf("%q is not one of the Vertex voices this server offers", voiceID)}
		}
		return native, nil
	}
	return name, nil
}
