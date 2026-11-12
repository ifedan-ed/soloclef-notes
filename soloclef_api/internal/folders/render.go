package folders

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// Build renders one folder as the web and phone apps read it: these keys,
// in this order.
//
// A folder's answer names no owner. The caller is the owner of every folder
// they can be sent, so the field would say nothing, and leaving it out
// means no answer here ever carries an account id.
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
	rendered.Set("created_at", pythonjson.PydanticDateTime(folder.CreatedAt))
	rendered.Set("updated_at", pythonjson.PydanticDateTime(folder.UpdatedAt))
	rendered.Set("metadata", metadata)
	return rendered, nil
}

// BuildBreadcrumb renders an ancestor chain, root first.
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

// readMetadata reads the jsonb column as the clients type it: an object or
// null. The column has no constraint, so it can hold something else; that
// is answered as null, because one odd row must not take the whole folder
// list down with it.
func readMetadata(stored []byte) (any, error) {
	if len(stored) == 0 {
		return nil, nil
	}
	value, err := pythonjson.Decode(stored)
	if err != nil {
		return nil, err
	}
	object, isObject := value.(*pythonjson.Object)
	if !isObject {
		return nil, nil
	}
	return pythonjson.PydanticValues(object), nil
}

func numberOrNil(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

// sendFolder answers with one folder.
func sendFolder(ctx huma.Context, response http.ResponseWriter, requestID string, fail failures, folder Folder) {
	rendered, err := Build(folder)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not render the folder", err)
		return
	}
	send(ctx, response, requestID, fail, rendered)
}

// send is the one place a body becomes bytes.
func send(ctx huma.Context, response http.ResponseWriter, requestID string, fail failures, body any) {
	encoded, err := pythonjson.Marshal(body)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not render the answer", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, encoded)
}

// sendMessage answers with one sentence, as `{"message": ...}`.
func sendMessage(ctx huma.Context, response http.ResponseWriter, requestID string, fail failures, message string) {
	answer := pythonjson.NewObject()
	answer.Set("message", message)
	send(ctx, response, requestID, fail, answer)
}
