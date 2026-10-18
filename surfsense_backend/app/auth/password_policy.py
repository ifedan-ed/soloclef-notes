"""What a new password must be: registration, password change and reset alike."""

from __future__ import annotations

from fastapi_users.exceptions import InvalidPasswordException

MIN_LENGTH = 8
# bcrypt/argon2 inputs this long are a denial-of-service lever, not a password.
MAX_LENGTH = 256


async def validate_new_password(password: str, email: str | None = None) -> None:
    """Raise ``InvalidPasswordException`` with a reason people can act on."""
    if len(password) < MIN_LENGTH:
        raise InvalidPasswordException(
            reason=f"Password should be at least {MIN_LENGTH} characters."
        )
    if len(password) > MAX_LENGTH:
        raise InvalidPasswordException(
            reason=f"Password should be at most {MAX_LENGTH} characters."
        )
    if email and password.strip().lower() == email.strip().lower():
        raise InvalidPasswordException(reason="Password should not be your email.")
