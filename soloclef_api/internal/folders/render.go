package folders

import (
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// errNotAMapping is a `metadata` column holding something other than an
// object. FolderRead types it `dict[str, Any] | None`, so the Python route's
// response model rejects it and FastAPI answers 500 — from *outside* the CORS
// middleware, because that happens after the route returned. The seeded
// workspace in the comparison carries such a row for exactly this case.
var errNotAMapping = errors.New("folders: the metadata column is not an object")

// Build renders one folder as FolderRead.
//
// The key order is pydantic's field order, which for a plain BaseModel is the
// declaration order in app/schemas/folders.py, and `metadata` is last because
// it is declared last — its `validation_alias` renames what it reads from the
// ORM object, not where it appears. Read off the running tier's own response
// rather than reasoned about.
//
// The datetimes are pydantic's own form rather than isoformat's, because the
// routes declare a response_model: six fractional digits or none at all.
func Build(folder Folder) (*pythonjson.Object, error) {
	metadata, err := readMetadata(folder.Metadata)
	if err != nil {
		return nil, err
	}

	rendered := pythonjson.NewObject()
	rendered.Set("id", folder.ID)
	rendered.Set("name", folder.Name)
	rendered.Set("position", folder.Position)
	rendered.Set("parent_id", numberOrNil(folder.ParentID))
	rendered.Set("workspace_id", folder.WorkspaceID)
	rendered.Set("created_by_id", textOrNil(folder.CreatedByID))
	rendered.Set("created_at", pythonjson.PydanticDateTime(folder.CreatedAt))
	rendered.Set("updated_at", pythonjson.PydanticDateTime(folder.UpdatedAt))
	rendered.Set("metadata", metadata)
	return rendered, nil
}

// BuildBreadcrumb renders the ancestor chain as list[FolderBreadcrumb].
func BuildBreadcrumb(chain []Breadcrumb) []any {
	rendered := make([]any, 0, len(chain))
	for _, link := range chain {
		one := pythonjson.NewObject()
		one.Set("id", link.ID)
		one.Set("name", link.Name)
		rendered = append(rendered, one)
	}
	return rendered
}

// readMetadata reads the jsonb column as pydantic reads it into
// `dict[str, Any] | None`. A SQL NULL is None; anything that is not an object
// fails the response model, which is the Python route's 500 and so is an error
// here rather than a guess.
func readMetadata(stored []byte) (any, error) {
	if len(stored) == 0 {
		return nil, nil
	}
	value, err := pythonjson.Decode(stored)
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, nil
	}
	object, isObject := value.(*pythonjson.Object)
	if !isObject {
		return nil, errNotAMapping
	}
	// These routes declare a response_model, so the whole body — the metadata
	// included — is serialised by pydantic-core rather than by json.dumps, and
	// a float inside the column is written its way and not repr's
	// (internal/pythonjson.PydanticFloat).
	return pythonjson.PydanticValues(object), nil
}

func numberOrNil(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func textOrNil(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

// marshal is the one place this slice renders a body to bytes, so the routes
// and the tests cannot drift into two renderings.
func marshal(body any) ([]byte, error) {
	return pythonjson.Marshal(body)
}

// sendFolder is the answer the create and the three PUT routes share: one
// FolderRead, or the 500 a column no response model can read earns.
func sendFolder(ctx huma.Context, response http.ResponseWriter,
	requestID string, fail failures, folder Folder) {
	rendered, err := Build(folder)
	if err != nil {
		fail.failWhileRendering(ctx, response, requestID, "could not render the folder", err)
		return
	}
	body, err := marshal(rendered)
	if err != nil {
		fail.failWhileRendering(ctx, response, requestID, "could not render the folder", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}
