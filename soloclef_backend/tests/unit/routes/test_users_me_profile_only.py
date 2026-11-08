"""PATCH /users/me changes the profile only, never the email or the password.

fastapi-users' update schema took both and changed them on a session alone:
no check of the new address, no current password. A changed address is what
single sign-on links a first-time identity to, so the route now takes the
profile fields alone and refuses anything else (422).
"""

from __future__ import annotations

from types import SimpleNamespace
from uuid import uuid4

import pytest
from fastapi import FastAPI
from starlette.testclient import TestClient

from app.auth.context import AuthContext
from app.routes import users_routes
from app.users import get_user_manager, require_session_context

pytestmark = pytest.mark.unit


class _Manager:
    def __init__(self, user) -> None:
        self.user = user
        self.updates: list[dict] = []

    async def update(self, update, user, safe=False, request=None):
        changes = update.create_update_dict() if safe else {}
        self.updates.append(changes)
        for field, value in changes.items():
            setattr(user, field, value)
        return user


@pytest.fixture
def world():
    user = SimpleNamespace(
        id=uuid4(),
        email="probe@example.org",
        is_active=True,
        is_superuser=False,
        is_verified=False,
        credit_micros_balance=0,
        display_name=None,
        avatar_url=None,
    )
    manager = _Manager(user)
    app = FastAPI()
    app.include_router(users_routes.router)
    app.dependency_overrides[require_session_context] = lambda: AuthContext.session(
        user
    )
    app.dependency_overrides[get_user_manager] = lambda: manager
    return SimpleNamespace(client=TestClient(app), manager=manager, user=user)


def test_the_display_name_and_avatar_still_change(world) -> None:
    res = world.client.patch(
        "/users/me",
        json={"display_name": "Probe", "avatar_url": "https://a.example/p.png"},
    )
    assert res.status_code == 200, res.text
    assert world.manager.updates == [
        {"display_name": "Probe", "avatar_url": "https://a.example/p.png"}
    ]
    assert res.json()["display_name"] == "Probe"


@pytest.mark.parametrize(
    "body",
    [
        {"email": "someone-else@example.org"},
        {"password": "a-new-password-of-length"},
        {"display_name": "Probe", "email": "someone-else@example.org"},
        {"is_verified": True},
        {"is_superuser": True},
    ],
)
def test_nothing_that_signs_in_is_taken(world, body) -> None:
    res = world.client.patch("/users/me", json=body)
    assert res.status_code == 422
    assert world.manager.updates == []
    assert world.user.email == "probe@example.org"
