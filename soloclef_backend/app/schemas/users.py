import uuid

from fastapi_users import schemas
from pydantic import ConfigDict


class UserRead(schemas.BaseUser[uuid.UUID]):
    credit_micros_balance: int
    display_name: str | None = None
    avatar_url: str | None = None


class UserCreate(schemas.BaseUserCreate):
    pass


class UserUpdate(schemas.CreateUpdateDictModel):
    """What PATCH /users/me may change: the profile, nothing that signs in.

    Not fastapi-users' ``BaseUserUpdate``: that takes ``email`` and
    ``password`` too, and changed them on a session alone, with no check of
    the new address and no current password. A changed email is what single
    sign-on and agent sign-in link a first-time identity to, so anyone with a
    session (or someone who planted an account) could claim another person's
    address. A password changes through "Forgot password?" (a mailed token);
    an email only by the operator. Unknown fields are refused (422), so a
    client that sends either learns it was not changed.
    """

    model_config = ConfigDict(extra="forbid")

    display_name: str | None = None
    avatar_url: str | None = None
