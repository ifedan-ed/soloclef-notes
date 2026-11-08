"""Which Notes account a verified agent sign-in acts as.

The same rule as the web's single sign-on (``app.auth_oidc.account_link``),
read-only:

1. An identity the web sign-in linked before acts as that account, found by
   the provider's subject id, whatever its email says today.
2. Otherwise the account with the same email, but only when Authentik says it
   verified that email (an email scope mapping asserts it) and that account's
   own address is verified (``is_verified``), as the web's rule requires:
   an account someone signed up with another person's address acts for
   nobody.
3. Otherwise nobody: an agent sign-in never creates an account and never
   records a link, so alice@example.com acts as alice's account and
   admin@example.com as admin's (an administrator through
   ``BILLING_ADMIN_EMAILS``, by email, exactly as on the web).
"""

from __future__ import annotations

import uuid

from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.agent_sign_in.access_token import AgentIdentity
from app.auth_oidc.account_link import identity_links
from app.db import User


class NoAccountForAgentError(Exception):
    """A genuine sign-in for which Notes has no usable account."""


def choose_account(
    *,
    linked_user_id: uuid.UUID | None,
    email_user_id: uuid.UUID | None,
    email_verified: bool,
    email_user_verified: bool = False,
) -> uuid.UUID:
    """The rule in the module docstring, with no database in the way.

    ``email_user_verified``: whether the account with the same email has its
    own address verified.
    """
    if linked_user_id is not None:
        return linked_user_id
    if email_user_id is not None and email_verified and email_user_verified:
        return email_user_id
    raise NoAccountForAgentError("no Notes account matches this sign-in")


async def account_for_agent(
    identity: AgentIdentity, *, provider_name: str, session: AsyncSession
) -> User:
    linked_user_id = await session.scalar(
        select(identity_links.c.user_id).where(
            identity_links.c.oauth_name == provider_name,
            identity_links.c.account_id == identity.subject,
        )
    )
    email_user_id = None
    email_user = None
    if linked_user_id is None and identity.email:
        email_user_id = await session.scalar(
            select(User.id).where(func.lower(User.email) == identity.email)
        )
        if email_user_id is not None:
            email_user = await session.get(User, email_user_id)
    user_id = choose_account(
        linked_user_id=linked_user_id,
        email_user_id=email_user_id,
        email_verified=identity.email_verified,
        email_user_verified=bool(email_user is not None and email_user.is_verified),
    )
    user = email_user if email_user is not None else await session.get(User, user_id)
    if user is None or not user.is_active:
        raise NoAccountForAgentError("the Notes account is deactivated")
    return user
