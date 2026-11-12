package parserchoice

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/pictures"
)

// CapabilitiesPath is the route every parser picker reads: the upload
// dialog, a notebook's "add sources", the Nextcloud picker and the
// connector form.
const CapabilitiesPath = "/api/v1/etl/capabilities"

// Limits are the upload limits, told to the client so it can refuse a file
// before sending it. They are the upload route's own numbers (cmd/api gives
// both the same values), never a second copy that could drift.
type Limits struct {
	// MaxFileBytes is the largest single file an upload may carry.
	MaxFileBytes int64
	// MaxFiles is the most files one upload request may carry.
	MaxFiles int
}

// CapabilitiesRoute serves GET /api/v1/etl/capabilities.
//
// It answers for any signed-in caller. What it says is the deployment's
// parser configuration, which is the same for everyone and which every
// upload dialog has to know, and one thing about the caller: whether they
// may switch the vision model on (they, or the deployment, have one). That
// is the one row read; everything else is said from memory.
//
// # Better than the route it replaces
//
// The Python route offered two choices, "local" and "cloud", where "cloud"
// meant whichever single service one setting named. This one lists all four
// parsers, each with whether it is configured and what it reads, and the
// default by name. The registry is built once at start-up, so the body is
// encoded once in each of its two forms (vision or not) and each request is
// one read of the caller's vision choice and a write.
type CapabilitiesRoute struct {
	Parsers Registry
	Limits  Limits
	// Vision says whether the caller may switch use_vision_llm on
	// (uploadvision.Vision), the rule the upload routes apply.
	Vision interface {
		Available(ctx context.Context, userID string) (bool, error)
	}
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

// capabilities is the response body. The field names are the ones the web
// and mobile pickers already read (`default_parser`, `parsers[].parser`,
// `available`, `unavailable_reason`, `document_extensions`,
// `always_handled`).
type capabilities struct {
	DefaultParser string       `json:"default_parser"`
	Parsers       []capability `json:"parsers"`
	AlwaysHandled alwaysRead   `json:"always_handled"`
	// VisionAvailable is whether the caller may switch use_vision_llm on:
	// a client offers the switch only when the upload route would take it.
	VisionAvailable bool `json:"vision_available"`
	// PictureExtensions are the files an upload with vision on accepts
	// whatever parser was chosen: the vision model reads them.
	PictureExtensions []string `json:"picture_extensions"`
	MaxFileBytes      int64    `json:"max_file_size_bytes"`
	MaxFiles          int      `json:"max_files_per_upload"`
}

type capability struct {
	Parser    string `json:"parser"`
	Available bool   `json:"available"`
	// UnavailableReason is a stable token ("not_configured") the client
	// translates, and null for a parser that can be picked.
	UnavailableReason  *string  `json:"unavailable_reason"`
	DocumentExtensions []string `json:"document_extensions"`
}

// alwaysRead is the files Go reads itself, whatever parser is chosen.
type alwaysRead struct {
	Plaintext     []string `json:"plaintext"`
	DirectConvert []string `json:"direct_convert"`
}

// Describe is the body for a registry and its limits. It is exported for
// the tests and for any caller that needs the same facts without HTTP.
func Describe(registry Registry, limits Limits, visionAvailable bool) ([]byte, error) {
	body := capabilities{
		DefaultParser:     registry.Default().String(),
		MaxFileBytes:      limits.MaxFileBytes,
		MaxFiles:          limits.MaxFiles,
		VisionAvailable:   visionAvailable,
		PictureExtensions: pictures.Extensions(),
	}
	for _, option := range registry.Options() {
		// A copy, sorted: the registry's slice is shared with the parsers,
		// and a picker shows the list in a stable order.
		extensions := append([]string{}, option.Extensions...)
		sort.Strings(extensions)
		entry := capability{Parser: string(option.Parser), Available: option.Available, DocumentExtensions: extensions}
		if !option.Available {
			reason := option.UnavailableReason
			entry.UnavailableReason = &reason
		}
		body.Parsers = append(body.Parsers, entry)
	}
	body.AlwaysHandled.Plaintext, body.AlwaysHandled.DirectConvert = ReadDirectly()
	return json.Marshal(body)
}

// Register mounts the route. The two bodies are built here, once: nothing
// in them can change while the process runs.
func (r CapabilitiesRoute) Register(api huma.API) error {
	if r.Vision == nil {
		return errors.New("parserchoice: the capabilities route needs the vision check")
	}
	withoutVision, err := Describe(r.Parsers, r.Limits, false)
	if err != nil {
		return err
	}
	withVision, err := Describe(r.Parsers, r.Limits, true)
	if err != nil {
		return err
	}
	huma.Register(api, huma.Operation{
		OperationID: "read-parser-capabilities",
		Method:      http.MethodGet,
		Path:        CapabilitiesPath,
		Summary:     "The parsers a source may choose, and what each reads",
		Tags:        []string{"documents"},
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) {
			response := humawriter.New(ctx)
			requestID := requestcontext.ID(ctx.Context())
			caller, signedIn := principal.FromContext(ctx.Context())
			if !signedIn {
				// Unreachable behind the gate; a 500 rather than an
				// unauthenticated answer if the wiring is ever wrong.
				r.Logger.LogAttrs(ctx.Context(), slog.LevelError, "the parser capabilities ran without an authenticated caller",
					slog.String("request_id", requestID))
				r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
				return
			}
			available, err := r.Vision.Available(ctx.Context(), caller.UserID)
			if err != nil {
				// Not answered as "no vision": the switch would vanish for
				// an account that has a model, on the day its row could
				// not be read.
				r.Logger.LogAttrs(ctx.Context(), slog.LevelError, "the caller's vision model could not be read",
					slog.String("request_id", requestID), slog.String("error", err.Error()))
				r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
				return
			}
			body := withoutVision
			if available {
				body = withVision
			}
			header := response.Header()
			header.Set("Content-Type", "application/json")
			header.Set(requestcontext.Header, requestID)
			response.WriteHeader(http.StatusOK)
			_, _ = response.Write(body)
		}}, nil
	})
	return nil
}
