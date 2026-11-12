package voices

import (
	"fmt"
	"strings"
)

// The environment variables cmd/api reads Settings from. They are named here
// so that an error can name the thing an operator has to edit.
//
// Each holds what the Python tier's setting of the same name without the
// prefix holds (TTS_SERVICE, …), and must: the worker that voices a podcast
// reads those, and a brief this tier accepts has to be one that worker can
// render.
const (
	ServiceVariable       = "SOLOCLEF_API_TTS_SERVICE"
	APIBaseVariable       = "SOLOCLEF_API_TTS_SERVICE_API_BASE"
	APIKeyVariable        = "SOLOCLEF_API_TTS_SERVICE_API_KEY"
	PodcastVoicesVariable = "SOLOCLEF_API_TTS_PODCAST_VOICES"
)

// Settings is everything the catalogue is configured with. There is one
// setting per question and no setting stands in for another.
type Settings struct {
	// Service names the speech service: `<roster>/<model>`, for example
	// `openai/openrouter-kokoro-82m-tts`. Empty means the deployment has no
	// speech service, which is a valid deployment: the podcast routes that
	// need one answer that there is none.
	Service string
	// APIBase is the model proxy's OpenAI-style base address, for example
	// `http://litellm:4000/v1`. Required with a service, because every call
	// goes through the proxy and none goes to a provider.
	APIBase string
	// APIKey is the proxy's key for this application. Empty sends no
	// Authorization header, which is right for a proxy that asks for none.
	APIKey string
	// PodcastVoices is the voices a new podcast's speakers are given, in
	// speaker order, in the service's own voice names ("af_heart",
	// "am_adam"). Empty leaves the choice to the roster's order. A name the
	// roster does not have is a wrong setting, not a hint.
	PodcastVoices []string
}

// RosterName is which list of voices a service draws on. It is also the
// prefix of every voice id in that list.
type RosterName string

const (
	Kokoro RosterName = "kokoro"
	OpenAI RosterName = "openai"
	Azure  RosterName = "azure"
	Vertex RosterName = "vertex_ai"
)

// Service is Settings.Service, read.
type Service struct {
	// Name is the setting as written.
	Name string
	// Roster is the list its voices come from.
	Roster RosterName
	// Model is the name the proxy knows the model by: the setting without
	// its first segment.
	Model string
}

// kokoroMarker is how a Kokoro model is known. The setting's first segment
// names the API the proxy speaks, and a hosted Kokoro speaks OpenAI's, so
// `openai/openrouter-kokoro-82m-tts` would otherwise be offered coral and
// ash. Kokoro is the one model family whose roster does not follow from that
// segment, and its name is how the worker that renders the audio tells too
// (app/podcasts/voices/provider.py) -- the two must agree, or this tier
// would accept a brief the worker cannot voice.
const kokoroMarker = "kokoro"

// localPrefix is a service that would run a model inside this process. There
// is no such thing here.
const localPrefix = "local/"

// MisconfiguredError is a setting that is wrong. It names the setting, so
// that whoever reads the log knows what to edit.
type MisconfiguredError struct {
	Setting string
	Reason  string
}

func (e *MisconfiguredError) Error() string {
	return e.Setting + " " + e.Reason
}

func misconfigured(setting, format string, arguments ...any) *MisconfiguredError {
	return &MisconfiguredError{Setting: setting, Reason: fmt.Sprintf(format, arguments...)}
}

// readService reads Settings.Service. Each kind of service is recognised by
// exactly one rule, and a value none of them recognises is refused: there is
// no roster to fall back on, because a voice from the wrong roster is a
// podcast that fails after its transcript has been paid for.
func readService(written string) (Service, error) {
	name := strings.TrimSpace(written)
	lowered := strings.ToLower(name)
	if strings.HasPrefix(lowered, localPrefix) {
		return Service{}, misconfigured(ServiceVariable,
			"is %q, which names a speech model on this box. Nothing here runs a model: "+
				"name one the proxy serves, for example openai/openrouter-kokoro-82m-tts", name)
	}
	prefix, model, found := strings.Cut(name, "/")
	if !found || prefix == "" || model == "" {
		return Service{}, misconfigured(ServiceVariable,
			"is %q, and must be <roster>/<the proxy's model name>, for example openai/openai-tts-1-hd", name)
	}
	service := Service{Name: name, Model: model}
	switch {
	case strings.Contains(strings.ToLower(model), kokoroMarker):
		service.Roster = Kokoro
	case strings.EqualFold(prefix, string(OpenAI)):
		service.Roster = OpenAI
	case strings.EqualFold(prefix, string(Azure)):
		service.Roster = Azure
	case strings.EqualFold(prefix, string(Vertex)):
		service.Roster = Vertex
	default:
		return Service{}, misconfigured(ServiceVariable,
			"is %q, and no voices are known for %q: the first segment must be openai, azure or vertex_ai", name, prefix)
	}
	return service, nil
}

// validate is everything about the settings that can be checked without
// asking the proxy anything.
func (s Settings) validate() (Service, bool, error) {
	if strings.TrimSpace(s.Service) == "" {
		// No speech service. Then nothing else may be set either: a list of
		// host voices for no service is a deployment that thinks it has one.
		if len(s.PodcastVoices) > 0 {
			return Service{}, false, misconfigured(PodcastVoicesVariable,
				"is set and %s is not: there is no service for those voices to belong to", ServiceVariable)
		}
		return Service{}, false, nil
	}
	service, err := readService(s.Service)
	if err != nil {
		return Service{}, false, err
	}
	if strings.TrimSpace(s.APIBase) == "" {
		return Service{}, false, misconfigured(APIBaseVariable,
			"is not set. %s names a service, and every speech call goes through the model proxy at this address", ServiceVariable)
	}
	return service, true, nil
}
