"""What a new password must be."""

from __future__ import annotations

import pytest
from fastapi_users.exceptions import InvalidPasswordException

from app.auth.password_policy import validate_new_password

pytestmark = pytest.mark.unit


async def test_a_reasonable_password_passes():
    await validate_new_password("correct horse battery", "a@b.co")


@pytest.mark.parametrize(
    ("password", "reason"),
    [
        ("short", "at least 8"),
        ("x" * 257, "at most 256"),
        ("Me@Example.com", "not be your email"),
    ],
)
async def test_weak_passwords_are_refused_with_a_reason(password, reason):
    with pytest.raises(InvalidPasswordException) as exc:
        await validate_new_password(password, "me@example.com")
    assert reason in exc.value.reason
