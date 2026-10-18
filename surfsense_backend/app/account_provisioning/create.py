"""Create an account exactly as registration would.

Registration is closed at the edge, so an operator creates accounts here.
The account goes through the same ``UserManager.create`` as a sign-up (the
password is hashed by it, and ``on_after_register`` gives it its workspace,
prompts and welcome credit), but is marked verified. Without an explicit
password it gets a random one that is never shown or stored anywhere, and the
person sets their own with "Forgot password?" on the sign-in page. An
operator bootstrapping a server without email can pass one instead.
"""

from __future__ import annotations

import secrets
from dataclasses import dataclass

from fastapi_users.db import SQLAlchemyUserDatabase
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app import db
from app.db import User, Workspace, async_session_maker
from app.schemas import UserCreate
from app.users import UserManager


class AccountExistsError(RuntimeError):
    pass


@dataclass(frozen=True)
class CreatedAccount:
    user_id: str
    email: str
    workspace_ids: tuple[int, ...]


def _user_db(session: AsyncSession) -> SQLAlchemyUserDatabase:
    # The OAuth account table only exists when Google sign-in is configured.
    oauth_account = getattr(db, "OAuthAccount", None)
    if oauth_account is None:
        return SQLAlchemyUserDatabase(session, User)
    return SQLAlchemyUserDatabase(session, User, oauth_account)


def unguessable_password() -> str:
    """64 random URL-safe characters (384 bits); discarded after hashing."""
    return secrets.token_urlsafe(48)


async def create_verified_account(
    email: str,
    password: str | None = None,
    session_maker: async_sessionmaker[AsyncSession] = async_session_maker,
) -> CreatedAccount:
    email = email.strip().lower()
    async with session_maker() as session:
        manager = UserManager(_user_db(session))
        existing = await session.scalar(select(User.id).where(User.email == email))
        if existing is not None:
            raise AccountExistsError(email)
        user = await manager.create(
            UserCreate(
                email=email,
                password=password or unguessable_password(),
                is_active=True,
                is_verified=True,
                is_superuser=False,
            ),
            safe=False,
        )
        user_id = user.id
    async with session_maker() as session:
        workspace_ids = tuple(
            (
                await session.scalars(
                    select(Workspace.id)
                    .where(Workspace.user_id == user_id)
                    .order_by(Workspace.id)
                )
            ).all()
        )
    return CreatedAccount(
        user_id=str(user_id), email=email, workspace_ids=workspace_ids
    )
