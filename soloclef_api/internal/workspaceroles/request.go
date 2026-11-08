package workspaceroles

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// The constraints RoleCreate declares (app/schemas/rbac_schemas.py:RoleBase).
const (
	nameMinLength        = 1
	nameMaxLength        = 100
	descriptionMaxLength = 500
)

// Requested is a validated RoleCreate: the four fields the Python route writes
// from role_data.model_dump(), with the defaults the model gives them.
type Requested struct {
	Name        string
	Description *string
	Permissions []string
	IsDefault   bool
}

// ReadRequest validates the body as RoleCreate. The fields are read in the
// model's declaration order because that is the order FastAPI reports their
// failures in, and every one is read even after one has failed, because a
// request with three bad fields is answered with all three.
//
// A field the body does not mention is not an error: pydantic ignores it, so an
// unknown key changes nothing and the request succeeds, which was checked
// against the Python tier rather than assumed.
func ReadRequest(contentType string, body []byte) (Requested, []errorenvelope.Failure, error) {
	object, failure, err := requestbody.Decode(contentType, body)
	if err != nil {
		return Requested{}, nil, err
	}
	if failure != nil {
		return Requested{}, []errorenvelope.Failure{*failure}, nil
	}

	// Read one field per statement so the order is the model's and is visible
	// as such, rather than resting on how a composite literal is evaluated.
	model := requestbody.NewModel(object)
	var requested Requested
	requested.Name = model.Text("name", nameMinLength, nameMaxLength)
	requested.Description = model.OptionalText("description", descriptionMaxLength)
	requested.Permissions = model.TextList("permissions")
	requested.IsDefault = model.Truth("is_default", false)
	if !model.Valid() {
		return Requested{}, model.Failures(), nil
	}
	return requested, nil, nil
}
