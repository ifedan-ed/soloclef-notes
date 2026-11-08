package workspaceroles

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// Changes is a validated RoleUpdate: a partial update, so each field carries
// whether the request mentioned it at all.
//
// The distinction is the route's whole behaviour. update_role acts on
// role_update.model_dump(exclude_unset=True): a field the body did not mention
// is not written, is not checked against the system-role restrictions and does
// not provoke the name conflict — while an explicit null *is* a value, and
// writing it into one of the three NOT NULL columns is a 500 on both tiers.
type Changes struct {
	Name        requestbody.MaybeText
	Description requestbody.MaybeText
	Permissions requestbody.MaybeTextList
	IsDefault   requestbody.MaybeTruth
}

// ReadChanges validates the body as RoleUpdate (app/schemas/rbac_schemas.py).
// The fields are read in the model's declaration order because that is the
// order FastAPI reports their failures in, and every one is read even after one
// has failed.
func ReadChanges(contentType string, body []byte) (Changes, []errorenvelope.Failure, error) {
	object, failure, err := requestbody.Decode(contentType, body)
	if err != nil {
		return Changes{}, nil, err
	}
	if failure != nil {
		return Changes{}, []errorenvelope.Failure{*failure}, nil
	}

	model := requestbody.NewModel(object)
	var changes Changes
	changes.Name = model.MaybeText("name", nameMinLength, nameMaxLength)
	changes.Description = model.MaybeText("description", 0, descriptionMaxLength)
	changes.Permissions = model.MaybeTextList("permissions")
	changes.IsDefault = model.MaybeTruth("is_default")
	if !model.Valid() {
		return Changes{}, model.Failures(), nil
	}
	return changes, nil, nil
}

// RequestedPermissions is `role_update.permissions or []`, which is what the
// ownership rule is asked about before the role is even loaded: an absent list
// and an explicit null are both no permissions at all.
func (c Changes) RequestedPermissions() []string {
	if !c.Permissions.Present || c.Permissions.Value == nil {
		return nil
	}
	return c.Permissions.Value
}

// TouchesSystemRoleFields reports whether the body mentions one of the three
// fields a system role refuses: name, description or is_default. Mentioning it
// is enough — the Python check is `field in update_data`, so naming a field
// with the value it already has is refused too.
func (c Changes) TouchesSystemRoleFields() bool {
	return c.Name.Present || c.Description.Present || c.IsDefault.Present
}

// AsksToBeDefault is `update_data.get("is_default")`, which is a truth test
// rather than a presence test: an explicit null and an explicit false both
// leave the workspace's existing default alone.
func (c Changes) AsksToBeDefault() bool {
	return c.IsDefault.Present && c.IsDefault.Value != nil && *c.IsDefault.Value
}
