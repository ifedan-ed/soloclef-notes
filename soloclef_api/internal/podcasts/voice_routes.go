package podcasts

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/voices"
)

// VoicesRoute serves GET /api/v1/podcasts/voices: the voices the brief form's
// picker offers, all of them or those that speak one language.
//
// It answers a signed-in caller and asks nothing more of them. The list is
// the same for everyone and is nobody's in particular; what it must not be is open
// to the internet, which on the Python tier it was.
type VoicesRoute struct {
	Voices Catalogue
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r VoicesRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-podcast-voices",
		Method:      http.MethodGet,
		Path:        VoicesPath,
		Summary:     "The voices a podcast's speakers can be given",
		Tags:        []string{"podcasts"},
	}, func(ctx context.Context, _ *struct {
		Language string `query:"language" doc:"Only the voices that speak this language (a tag such as en or pt-BR)"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r VoicesRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	answer := answerer{
		ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestcontext.ID(ctx.Context()),
		what:      "the podcast voices could not be listed",
	}
	if _, signedIn := answer.signedIn(); !signedIn {
		return
	}

	// `language` is optional. Named, it has to be a language: the form sends
	// the brief's own, and an empty or garbled one answering "every voice"
	// would fill a picker with voices that cannot speak the brief.
	address := ctx.URL()
	query := address.Query()
	language, filtered := "", query.Has("language")
	if filtered {
		tag, isLanguage := readLanguage(query.Get("language"))
		if !isLanguage {
			answer.errors.WriteValidation(response, answer.requestID, []errorenvelope.Failure{
				{Location: []string{"query", "language"}, Message: notALanguage}})
			return
		}
		language = tag
	}

	roster, err := r.Voices.Roster(ctx.Context())
	if err != nil {
		answer.refuse(err, "could not read the voice catalogue")
		return
	}
	offered := roster.Voices()
	if filtered {
		offered = roster.ForLanguage(language)
	}

	// VoiceOption, as the web app's `voiceOption` schema parses it. A voice
	// that speaks anything answers "*" for its language, which the form
	// reads as "matches every language".
	options := make([]any, 0, len(offered))
	for _, voice := range offered {
		option := pythonjson.NewObject()
		option.Set("voice_id", voice.ID)
		option.Set("display_name", voice.DisplayName)
		option.Set("language", voice.Language)
		option.Set("gender", string(voice.Gender))
		options = append(options, option)
	}
	body, err := pythonjson.Marshal(options)
	if err != nil {
		answer.fail("the voices could not be rendered", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

// LanguagesRoute serves GET /api/v1/podcasts/languages: the languages the
// brief form and the Studio's dialog offer, and whether any other may be
// typed. Like the voices, it answers any signed-in caller.
type LanguagesRoute struct {
	Voices Catalogue
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r LanguagesRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-podcast-languages",
		Method:      http.MethodGet,
		Path:        LanguagesPath,
		Summary:     "The languages a podcast can be made in",
		Tags:        []string{"podcasts"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r LanguagesRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	answer := answerer{
		ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestcontext.ID(ctx.Context()),
		what:      "the podcast languages could not be listed",
	}
	if _, signedIn := answer.signedIn(); !signedIn {
		return
	}
	roster, err := r.Voices.Roster(ctx.Context())
	if err != nil {
		answer.refuse(err, "could not read the voice catalogue")
		return
	}
	offering := roster.Languages()
	languages := make([]any, 0, len(offering.Languages))
	for _, tag := range offering.Languages {
		languages = append(languages, tag)
	}
	// LanguageOptions, as the web and mobile apps parse it.
	options := pythonjson.NewObject()
	options.Set("languages", languages)
	options.Set("allows_custom", offering.AllowsCustom)
	body, err := pythonjson.Marshal(options)
	if err != nil {
		answer.fail("the languages could not be rendered", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

// PreviewRoute serves GET /api/v1/podcasts/voices/{voice_id}/preview: a few
// seconds of a voice, so that it is chosen by ear.
//
// # Who may ask
//
// Anyone signed in: every account can make a podcast, so every account has
// a use for the sample. Whether an API key or an agent may act for the
// account was decided before the route ran (internal/principal).
//
// # Why that is enough
//
// This is the one read in the package that can cost money, and what bounds
// the cost is not who asks but how much there is to ask for
// (soloclef_assistant/voices.Samples). The caller picks a voice out of the roster and
// nothing else -- not the words, not the model -- and each voice is
// synthesised once for the whole deployment and kept. So the most this route
// can ever spend is one sentence per voice, however many people press play
// and however often, and no rate limit is needed to make that true.
type PreviewRoute struct {
	Voices  Catalogue
	Samples Sampler
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
}

// sampleMaxAge is how long a browser may keep a sample without asking again.
// A voice's sample does not change, and it is the same for every caller who
// may hear it; `private` keeps it out of shared caches all the same, because
// the route is behind a sign-in.
const sampleMaxAge = "private, max-age=86400"

func (r PreviewRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "preview-podcast-voice",
		Method:      http.MethodGet,
		Path:        PreviewPath,
		Summary:     "A short audio sample of a voice",
		Tags:        []string{"podcasts"},
	}, func(ctx context.Context, _ *struct {
		VoiceID string `path:"voice_id" doc:"The voice's id, as the voice list answers it"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r PreviewRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	answer := answerer{
		ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestcontext.ID(ctx.Context()),
		what:      "the voice sample could not be served",
	}
	if _, signedIn := answer.signedIn(); !signedIn {
		return
	}

	roster, err := r.Voices.Roster(ctx.Context())
	if err != nil {
		answer.refuse(err, "could not read the voice catalogue")
		return
	}
	// A voice of another roster is as unknown as one that never existed:
	// the service configured today cannot speak it.
	voice, found := roster.Voice(ctx.Param("voice_id"))
	if !found {
		answer.errors.Write(response, http.StatusNotFound, voiceAbsent, answer.requestID)
		return
	}

	sample, err := r.Samples.Of(ctx.Context(), voice)
	if err != nil {
		// The store being down, the provider failing, an answer that is not
		// audio: the listener can do nothing about any of them but try
		// again, so they are one sentence. The log has which.
		answer.unavailable(sampleUnavailable, err)
		return
	}
	header := response.Header()
	header.Set("Content-Type", sample.ContentType)
	header.Set("Content-Length", strconv.Itoa(len(sample.Audio)))
	header.Set("Cache-Control", sampleMaxAge)
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(sample.Audio)
}

// The compiler's check that the real catalogue and the real samples are what
// these routes take.
var (
	_ Catalogue = (*voices.Catalogue)(nil)
	_ Sampler   = (*voices.Samples)(nil)
)
