package instructionslint

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Route serves POST /api/v1/system-instructions/lint.
type Route struct {
	// QuickModel is the stored name of the model that does the account's
	// quick work (modelchoices.Choices.ModelFor with the quick purpose): a
	// proxy name, or a model of the account's own connection.
	QuickModel func(ctx context.Context, userID string) (string, error)
	// Open builds the client for that name, for that account (the chat's
	// Models.OpenFor, the one resolver); it does not call the model.
	Open func(ctx context.Context, userID, name string) (model.Model, error)
	// ModelTimeout bounds the model pass. The person is waiting on a
	// settings page, and the pattern answer is already complete, so the
	// model is given seconds and not a turn's minutes.
	ModelTimeout time.Duration
	Errors       errorenvelope.Writer
	Logger       *slog.Logger
}

type answer struct {
	Warnings     []Warning `json:"warnings"`
	ModelChecked bool      `json:"model_checked"`
}

// Register adds the operation.
func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "lint-system-instructions", Method: http.MethodPost, Path: Path,
		Summary: "Warnings about the caller's chat instructions, before they are saved",
		Tags:    []string{"chat instructions"},
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r Route) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		// Unreachable behind the gate; a 500 rather than a model call for
		// nobody if the wiring is ever wrong.
		r.Logger.LogAttrs(ctx.Context(), slog.LevelError, "the instructions check ran without an authenticated caller",
			slog.String("request_id", requestID))
		r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
		return
	}
	invalid := func(field, message string) {
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{{Location: []string{"body", field}, Message: message}})
	}

	// use_model is a pointer because leaving it out means yes: the page
	// asks for the full check, and a client that wants rules only says so.
	var body struct {
		Text     *string `json:"text"`
		UseModel *bool   `json:"use_model"`
	}
	if err := json.Unmarshal(requestbody.Buffered(ctx.Context()), &body); err != nil {
		invalid("text", "The body must be a JSON object with the text to check")
		return
	}
	if body.Text == nil {
		invalid("text", "text is required")
		return
	}
	cleaned := Clean(*body.Text)
	if utf8.RuneCountInString(cleaned) > MaxChars {
		invalid("text", "Instructions can be at most "+strconv.Itoa(MaxChars)+" characters")
		return
	}

	rules := ByRule(cleaned)
	checked := answer{Warnings: rules}
	if cleaned != "" && (body.UseModel == nil || *body.UseModel) {
		fromModel, err := r.byModel(ctx.Context(), caller.UserID, cleaned)
		if err != nil {
			// Said in the answer (model_checked stays false) and here.
			// The text itself is never logged: it is the person's.
			r.Logger.LogAttrs(ctx.Context(), slog.LevelInfo, "the instructions check had no model reading",
				slog.String("request_id", requestID), slog.String("error", err.Error()))
		} else {
			checked = answer{Warnings: Merge(rules, fromModel), ModelChecked: true}
		}
	}

	rendered, err := json.Marshal(checked)
	if err != nil {
		r.Logger.LogAttrs(ctx.Context(), slog.LevelError, "the instructions check could not be rendered",
			slog.String("request_id", requestID), slog.String("error", err.Error()))
		r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(rendered)
}

// byModel is the model pass with the account's quick model, bounded.
func (r Route) byModel(ctx context.Context, userID, cleaned string) ([]Warning, error) {
	ctx, cancel := context.WithTimeout(ctx, r.ModelTimeout)
	defer cancel()
	name, err := r.QuickModel(ctx, userID)
	if err != nil {
		return nil, err
	}
	reader, err := r.Open(ctx, userID, name)
	if err != nil {
		return nil, err
	}
	return ByModel(ctx, reader, cleaned)
}
