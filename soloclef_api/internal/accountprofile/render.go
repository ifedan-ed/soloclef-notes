package accountprofile

import "git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"

// Build renders one account as UserRead (app/schemas/users.py).
//
// The key order is the model's field order, which is fastapi-users' BaseUser
// first and this deployment's three additions after it: id, email, is_active,
// is_superuser, is_verified, display_name, avatar_url.
// Redeclaring `email` in the subclass does **not** move it — pydantic keeps an
// inherited field's position — which was asked of the model rather than
// assumed, because that redeclaration is this commit's own change.
//
// `id` is the uuid's canonical text, which is what `str(uuid.UUID)` and the
// column's own text form both are. There is no datetime here, so neither
// datetime spelling is in play.
func Build(profile Profile) *pythonjson.Object {
	rendered := pythonjson.NewObject()
	rendered.Set("id", profile.ID)
	rendered.Set("email", profile.Email)
	rendered.Set("is_active", profile.Active)
	rendered.Set("is_superuser", profile.Superuser)
	rendered.Set("is_verified", profile.Verified)
	rendered.Set("display_name", textOrNil(profile.DisplayName))
	rendered.Set("avatar_url", textOrNil(profile.AvatarURL))
	return rendered
}

func textOrNil(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
