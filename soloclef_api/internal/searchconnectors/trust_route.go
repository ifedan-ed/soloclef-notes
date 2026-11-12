package searchconnectors

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// TrustRoute serves both edits of an MCP server's trusted-tool list:
// trust_mcp_tool, which adds a name, and untrust_mcp_tool, which removes one.
// A trusted tool runs without asking the person first.
//
// The chat reads the trusted list from the row at every decision, so an
// untrusted tool asks again at the very next call; and the routes move the
// account's MCP counter after a change commits (Tools, tool_changes.go).
//
// What the two routes are easy to misread about:
//
//   - Whose server it is, is the statement's own predicate: an MCP server
//     whose owner is the caller. Both routes also refuse anything but an
//     interactive session before a row is read, because "run this without
//     asking me" is a decision for the person and not for a key or an agent
//     acting for them.
//   - The add refuses a name the config column cannot give back
//     (public_config.go, cannotBeStored), before it asks whether the tool is
//     one that always asks. The remove refuses no name, so a tool can always
//     be made to ask again.
//   - The list is read leniently and written strictly: anything in it that
//     is not text is ignored on the way in and is gone on the way out, and
//     a `trusted_tools` that is not a list at all is replaced by one.
//   - Only the one key is written (queries.sql, StoreTrustedTools), with the
//     row locked from the read to the write, so two edits made at once both
//     land.
type TrustRoute struct {
	// Remove is false for trust-tool and true for untrust-tool.
	Remove  bool
	Changes Changes
	// Tools is told once a change to the list is committed.
	Tools  ToolChanges
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

// Register adds the operation.
func (r TrustRoute) Register(api huma.API) {
	operation := huma.Operation{
		OperationID: "trust-mcp-tool",
		Method:      http.MethodPost,
		Path:        TrustPath,
		Summary:     "Let an MCP tool run without asking",
		Description: "Adds the tool to the server's trusted list: it then runs " +
			"without asking the person first.",
		Tags: []string{"connectors"},
	}
	if r.Remove {
		operation.OperationID = "untrust-mcp-tool"
		operation.Path = UntrustPath
		operation.Summary = "Make an MCP tool ask again"
		operation.Description = "Removes the tool from the server's trusted list: " +
			"it asks the person again from the next call."
	}
	huma.Register(api, operation, func(ctx context.Context, _ *struct {
		ConnectorID string `path:"connector_id" doc:"The MCP server's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r TrustRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, message: "the trusted-tool list could not be changed"}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		failed.caught(noCaller, nil)
		return
	}

	// The whole request is validated first: the path's failure, then the
	// body's.
	problems := make([]errorenvelope.Failure, 0, 2)
	connector := readNumber(ctx.Param("connector_id"), &problems, "path", "connector_id")
	tool, bodyProblems, decodeErr := readToolName(
		ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if decodeErr != nil {
		failed.caught("a body reached the route that was never buffered", decodeErr)
		return
	}
	if problems = append(problems, bodyProblems...); len(problems) > 0 {
		r.Errors.WriteValidation(response, requestID, problems)
		return
	}

	// A session only: an API key and an agent are refused before any row is
	// read.
	if !caller.IsSession() {
		r.Errors.Write(response, http.StatusForbidden, sessionRequired, requestID)
		return
	}

	// The add's two refusals of a name, before any statement: so each is the
	// answer even for an id that names nothing.
	if !r.Remove && cannotBeStored(tool) {
		r.Errors.Write(response, http.StatusUnprocessableEntity, toolUnstorable, requestID)
		return
	}
	if !r.Remove && alwaysAsks(tool) {
		r.Errors.Write(response, http.StatusBadRequest, toolAlwaysAsk, requestID)
		return
	}

	var (
		trusted       []string
		stored, found bool
	)
	if !connector.tooWide {
		var err error
		trusted, stored, found, err = r.Changes.EditTrustedTools(ctx.Context(), caller.UserID, connector.value,
			func(held []string) ([]string, bool) { return editTrusted(held, tool, r.Remove) })
		if err != nil {
			failed.caught("could not change the trusted-tool list", err)
			return
		}
	}
	if !found {
		r.Errors.Write(response, http.StatusNotFound, serverAbsent, requestID)
		return
	}
	if stored {
		// After the commit, and only for an edit that changed the list.
		toolsChanged(ctx.Context(), r.Tools, r.Logger, requestID, caller.UserID)
	}
	names := make([]any, 0, len(trusted))
	for _, name := range trusted {
		names = append(names, name)
	}
	body := pythonjson.NewObject()
	body.Set("status", "ok")
	body.Set("trusted_tools", names)
	rendered, err := pythonjson.Marshal(body)
	if err != nil {
		failed.uncaught(bodyUnwritten, err)
		return
	}
	pythonjson.Send(response, http.StatusOK, rendered)
}

// editTrusted is the decision of one edit: the list as it should then be, and
// whether that differs from what is stored. Adding a name already there, and
// removing one that is not, change nothing and write nothing.
func editTrusted(held []string, tool string, remove bool) ([]string, bool) {
	present := false
	for _, name := range held {
		if name == tool {
			present = true
			break
		}
	}
	switch {
	case !remove && !present:
		return append(held, tool), true
	case remove && present:
		kept := make([]string, 0, len(held))
		for _, name := range held {
			if name != tool {
				kept = append(kept, name)
			}
		}
		return kept, true
	}
	return held, false
}

// readToolName validates the body as MCPTrustToolRequest: one required
// `tool_name: str`, with no bounds, and extra fields ignored.
//
// The first test is one internal/requestbody.Decode makes second. FastAPI
// reads the bytes before it looks at the content type, and an empty body is
// "no body" whatever the header says -- so `POST` with nothing at all is
// "Field required" there. Decode asks about the content type first and would
// answer "Input should be a valid dictionary" for that request. The order is
// put right here, for these two routes, until it is put right there.
func readToolName(contentType string, body []byte) (string, []errorenvelope.Failure, error) {
	if len(body) == 0 {
		return "", []errorenvelope.Failure{{
			Location: []string{requestbody.BodyRoot},
			Message:  requestbody.FieldRequired,
		}}, nil
	}
	object, failure, err := requestbody.Decode(contentType, body)
	if err != nil {
		return "", nil, err
	}
	if failure != nil {
		return "", []errorenvelope.Failure{*failure}, nil
	}
	model := requestbody.NewModel(object)
	tool := model.Text("tool_name", 0, 0)
	if !model.Valid() {
		return "", model.Failures(), nil
	}
	return tool, nil, nil
}
