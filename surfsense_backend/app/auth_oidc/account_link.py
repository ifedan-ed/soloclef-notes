"""Which Notes account a verified provider identity signs in to.

1. An identity seen before signs in to the account it was linked to, found
   by the provider's subject id, whatever its email says today.
2. A first-time identity is linked to the Notes account with the same email,
   but only when the provider says it verified that email (Authentik's
   "Example email profile" mapping asserts it for every user) and that
   account's own address was verified too (``is_verified``: made by the
   operator, by an earlier SSO sign-in, or verified since). Otherwise anyone
   who signed up with, or once switched an account to, another person's
   address would have that person's first SSO sign-in land in an account
   they still hold the password of; that person is refused instead
   (``ACCOUNT_NOT_VERIFIED``) and the operator settles it.
3. Otherwise a new account is made when the address may have one
   (``account_creation_rule``: its domain is in ``OIDC_AUTOCREATE_DOMAINS``,
   or ``OIDC_ALLOW_SIGNUP=TRUE``). It is made exactly as a sign-up makes
   one: ``UserManager.create`` then ``on_after_register`` (signup credit,
   "My Workspace" with its default roles and the Owner membership, default
   prompts), verified: the provider vouched for the address. Any other
   address is refused; the operator makes its account with
   ``scripts/create_account.py`` and SSO then links to it by email. When
   the address got an account between the lookup and the creation (a
   second tab), that account is linked on the terms of rule 2, so one made
   meanwhile with the password sign-up form is refused too.

Accounts linked to SSO before rule 2 checked ``is_verified`` (the owner's
own, 2026-10-11) were marked verified by migration
``c0030_verify_sso_linked_accounts``; agent sign-in, which matches by email
when its subject was never linked, needs that.

Links live in the ``oauth_account`` table fastapi-users made for Google
sign-in (it exists on this database). It is reached here as a plain table
rather than through ``app.db.OAuthAccount``: that model and the
``User.oauth_accounts`` relationship are only mapped under AUTH_TYPE=GOOGLE,
and mapping them would add a joined load to every query that reads a user.
"""

from __future__ import annotations

import uuid
from dataclasses import dataclass
from enum import Enum

from fastapi_users.exceptions import UserAlreadyExists
from pydantic import ValidationError
from sqlalchemy import (
    Column,
    Integer,
    MetaData,
    String,
    Table,
    func,
    select,
    update,
)
from sqlalchemy.dialects.postgresql import UUID
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession

from app.account_provisioning.create import unguessable_password
from app.auth_oidc.account_creation_rule import may_create_account
from app.auth_oidc.errors import (
    ACCOUNT_NOT_VERIFIED,
    EMAIL_NOT_VERIFIED,
    NO_ACCOUNT,
    USER_INACTIVE,
    OidcLoginError,
)
from app.auth_oidc.id_token import VerifiedIdentity
from app.db import User
from app.schemas import UserCreate

# Kept on its own MetaData so it never collides with the ORM model of the
# same table under AUTH_TYPE=GOOGLE. Columns match fastapi-users' table.
_metadata = MetaData()
identity_links = Table(
    "oauth_account",
    _metadata,
    Column("id", UUID(as_uuid=True), primary_key=True),
    # References "user".id ON DELETE CASCADE in the database; not declared
    # here because "user" lives on app.db's MetaData, not this one.
    Column("user_id", UUID(as_uuid=True), nullable=False),
    Column("oauth_name", String(100), nullable=False, index=True),
    # Never the provider's token: nothing here calls the provider later.
    Column("access_token", String(1024), nullable=False),
    Column("expires_at", Integer, nullable=True),
    Column("refresh_token", String(1024), nullable=True),
    Column("account_id", String(320), nullable=False, index=True),
    Column("account_email", String(320), nullable=False),
)


class Match(Enum):
    LINKED = "linked"  # this identity signed in before
    BY_EMAIL = "by_email"  # first time; same verified email as an account
    NEW_ACCOUNT = "new_account"


def decide(
    *,
    linked_user_id: uuid.UUID | None,
    email_user_id: uuid.UUID | None,
    email: str,
    email_verified: bool,
    allow_signup: bool,
    autocreate_domains: frozenset[str] = frozenset(),
    email_user_verified: bool = False,
) -> Match:
    """The rule in the module docstring, with no database in the way.

    ``email_user_verified``: whether the account with the same email
    (``email_user_id``) has its own address verified; nothing links to one
    that has not.
    """
    if linked_user_id is not None:
        return Match.LINKED
    if not email_verified:
        raise OidcLoginError(EMAIL_NOT_VERIFIED, "provider has not verified the email")
    if email_user_id is not None:
        if not email_user_verified:
            raise OidcLoginError(
                ACCOUNT_NOT_VERIFIED,
                "the Notes account with this email never verified it",
            )
        return Match.BY_EMAIL
    if may_create_account(
        email, allow_signup=allow_signup, autocreate_domains=autocreate_domains
    ):
        return Match.NEW_ACCOUNT
    raise OidcLoginError(NO_ACCOUNT, "no Notes account has this email")


@dataclass(frozen=True)
class SignedInAccount:
    user: User
    match: Match


async def _create_account(
    email: str, session: AsyncSession, user_manager
) -> User | None:
    """A verified account for ``email``, made the way sign-up makes one.

    The password is random and never shown (as ``scripts/create_account.py``
    does); the person can set one with "Forgot password?" to sign in with a
    password or in the mobile app. ``None`` when the address got an account
    between the lookup and now.
    """
    try:
        new_account = UserCreate(
            email=email,
            password=unguessable_password(),
            is_active=True,
            is_verified=True,
            is_superuser=False,
        )
    except ValidationError as exc:
        raise OidcLoginError(NO_ACCOUNT, "address not usable for an account") from exc
    try:
        user = await user_manager.create(new_account, safe=False)
    except UserAlreadyExists:
        return None
    except IntegrityError:
        # The same race, lost at the unique index rather than the check.
        await session.rollback()
        taken = await session.scalar(
            select(User.id).where(func.lower(User.email) == email)
        )
        if taken is None:
            raise
        return None
    # on_after_register wrote the workspace and any signup credit through its
    # own session and nudged this copy's balance; read the row back so the
    # commit below cannot write a stale balance over the ledger's.
    await session.refresh(user)
    return user


async def account_for(
    identity: VerifiedIdentity,
    *,
    provider_name: str,
    allow_signup: bool,
    autocreate_domains: frozenset[str] = frozenset(),
    session: AsyncSession,
    user_manager,
) -> SignedInAccount:
    linked_user_id = await session.scalar(
        select(identity_links.c.user_id).where(
            identity_links.c.oauth_name == provider_name,
            identity_links.c.account_id == identity.subject,
        )
    )
    email_user_id = None
    email_user_verified = False
    if linked_user_id is None and identity.email:
        same_email = (
            await session.execute(
                select(User.id, User.is_verified).where(
                    func.lower(User.email) == identity.email
                )
            )
        ).first()
        if same_email is not None:
            email_user_id, email_user_verified = same_email[0], bool(same_email[1])

    match = decide(
        linked_user_id=linked_user_id,
        email_user_id=email_user_id,
        email=identity.email,
        email_verified=identity.email_verified,
        allow_signup=allow_signup,
        autocreate_domains=autocreate_domains,
        email_user_verified=email_user_verified,
    )

    if match is Match.NEW_ACCOUNT:
        user = await _create_account(identity.email, session, user_manager)
        if user is None:
            # Another first sign-in with this address (a second tab, a double
            # click) made the account a moment ago: link to that one instead,
            # on the terms of any account found by email. A password sign-up
            # with this address in that moment made an unverified account,
            # and linking to it would hand this person's SSO to whoever holds
            # its password: refused as at the lookup (ACCOUNT_NOT_VERIFIED).
            user = await session.scalar(
                select(User).where(func.lower(User.email) == identity.email)
            )
            match = decide(
                linked_user_id=None,
                email_user_id=user.id if user is not None else None,
                email=identity.email,
                email_verified=identity.email_verified,
                allow_signup=allow_signup,
                autocreate_domains=autocreate_domains,
                email_user_verified=bool(user is not None and user.is_verified),
            )
    else:
        user = await session.get(User, linked_user_id or email_user_id)

    if user is None or not user.is_active:
        raise OidcLoginError(USER_INACTIVE, "the Notes account is deactivated")

    if match is Match.LINKED:
        if identity.email:
            await session.execute(
                update(identity_links)
                .where(
                    identity_links.c.oauth_name == provider_name,
                    identity_links.c.account_id == identity.subject,
                )
                .values(account_email=identity.email)
            )
    else:
        await session.execute(
            identity_links.insert().values(
                id=uuid.uuid4(),
                user_id=user.id,
                oauth_name=provider_name,
                access_token="",
                expires_at=None,
                refresh_token=None,
                account_id=identity.subject,
                account_email=identity.email,
            )
        )
    if identity.name and not user.display_name:
        user.display_name = identity.name[:255]
    await session.commit()
    return SignedInAccount(user=user, match=match)
