"""An operator-created account: verified, hashed, unknown password, a workspace
on the shared defaults, and no second account for the same address."""

from __future__ import annotations

import uuid

import pytest
from fastapi_users.password import PasswordHelper
from sqlalchemy import delete, select

from app.account_provisioning.create import (
    AccountExistsError,
    create_verified_account,
    unguessable_password,
)
from app.config import config
from app.db import User, Workspace, async_session_maker

pytestmark = pytest.mark.integration

SHARED = [
    {
        "id": -4,
        "model_id": "google-gemini-3.7-flash",
        "enabled": True,
        "supports_chat": True,
        "supports_image_input": True,
    },
    {
        "id": -2001,
        "model_id": "openai-gpt-image-2",
        "enabled": True,
        "supports_image_generation": True,
    },
]


@pytest.fixture
async def email(monkeypatch):
    monkeypatch.setattr(config, "GLOBAL_MODELS", SHARED)
    address = f"Admin-{uuid.uuid4().hex[:8]}@Example.com"
    yield address
    # This path commits for real (as the script does), so clean up after it.
    async with async_session_maker() as session:
        user_id = await session.scalar(
            select(User.id).where(User.email == address.lower())
        )
        if user_id is not None:
            await session.execute(delete(Workspace).where(Workspace.user_id == user_id))
            await session.execute(delete(User).where(User.id == user_id))
            await session.commit()


async def test_the_account_is_verified_active_and_not_a_superuser(email):
    account = await create_verified_account(email)
    assert account.email == email.lower()
    async with async_session_maker() as session:
        user = await session.scalar(
            select(User).where(User.id == uuid.UUID(account.user_id))
        )
    assert (user.is_verified, user.is_active, user.is_superuser) == (True, True, False)
    assert user.hashed_password and not user.hashed_password.startswith("x")
    # Nobody knows the password: not even a plausible guess verifies.
    ok, _ = PasswordHelper().verify_and_update("password123", user.hashed_password)
    assert not ok


async def test_an_operator_can_set_the_password(email):
    account = await create_verified_account(email, password="bootstrap-password-1")
    async with async_session_maker() as session:
        user = await session.scalar(
            select(User).where(User.id == uuid.UUID(account.user_id))
        )
    ok, _ = PasswordHelper().verify_and_update(
        "bootstrap-password-1", user.hashed_password
    )
    assert ok


async def test_the_account_gets_a_workspace_on_the_shared_defaults(email):
    account = await create_verified_account(email)
    assert len(account.workspace_ids) == 1
    async with async_session_maker() as session:
        workspace = await session.get(Workspace, account.workspace_ids[0])
    assert (
        workspace.chat_model_id,
        workspace.vision_model_id,
        workspace.image_gen_model_id,
    ) == (
        0,
        -4,
        -2001,
    )


async def test_an_existing_address_is_left_alone(email):
    await create_verified_account(email)
    with pytest.raises(AccountExistsError):
        await create_verified_account(email.upper())


def test_generated_passwords_are_long_and_never_repeat():
    passwords = {unguessable_password() for _ in range(50)}
    assert len(passwords) == 50
    assert all(len(p) >= 64 for p in passwords)
