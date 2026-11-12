package accountprofile

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// ReadChanges validates the body as UserUpdate (app/schemas/users.py).
//
// Two declared fields, both `str | None = None` with no length constraint —
// the columns are unconstrained `varchar` — and `extra="forbid"`, which is the
// API's first model that refuses an undeclared key. That refusal is the point
// of the model: it declares neither `email` nor `password` deliberately, so a
// client that sends either is told 422 rather than quietly having nothing
// changed.
//
// The order is the order pydantic reports it in, asked of the model itself:
// the **declared** fields first in declaration order, then every extra in the
// order the request wrote them (internal/requestbody.RefuseExtras). So
// `{"nope": 1, "display_name": 5}` names display_name first.
//
// There is no `safe=True` to mirror. `create_update_dict()` also excludes
// `id`, `is_superuser`, `is_active`, `is_verified` and `oauth_accounts`, and
// this model declares none of them — with extras forbidden, naming one is a
// 422 before the exclusion could matter.
func ReadChanges(contentType string, body []byte) (Changes, []errorenvelope.Failure, error) {
	object, failure, err := requestbody.Decode(contentType, body)
	if err != nil {
		return Changes{}, nil, err
	}
	if failure != nil {
		return Changes{}, []errorenvelope.Failure{*failure}, nil
	}

	model := requestbody.NewModel(object)
	changes := Changes{
		DisplayName: model.MaybeText("display_name", 0, 0),
		AvatarURL:   model.MaybeText("avatar_url", 0, 0),
	}
	model.RefuseExtras(updatableFields...)
	if !model.Valid() {
		return Changes{}, model.Failures(), nil
	}
	return changes, nil, nil
}
