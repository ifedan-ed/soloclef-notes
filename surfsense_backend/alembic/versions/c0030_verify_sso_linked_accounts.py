"""Accounts already signed in to through single sign-on count as verified.

Since the 2026-10-11 security fixes, SSO links a first-time identity to the
account with the same email, agent sign-in (MCP) acts as it, and
``BILLING_ADMIN_EMAILS`` makes it an administrator, only when that
account's own address is verified (``user.is_verified``). An account made
by an SSO sign-in is made verified, but accounts linked by email before
that rule may not be: the owner's own (``you@example.com``) was
unverified with an SSO link, so his agent sign-in, which matches by email
when its subject was never linked, would have been refused.

This marks verified every active account that has an SSO link
(``oauth_account`` with ``oauth_name`` = the configured provider,
``OIDC_PROVIDER_NAME``, ``authentik`` when unset) whose address is the one
the provider last vouched for (``account_email``, rewritten at every SSO
sign-in; compared without case). An account whose own address was changed
after it was linked (``PATCH /users/me`` allowed that until the same fixes)
is left as it is: the provider never vouched for that address. A database
without the table (no SSO ever) is left alone.

``downgrade`` changes nothing: which accounts this verified is not recorded
in the database. The deploy steps keep the list (custom/README.md,
"Security fixes of 2026-10-11", follow-up), and the undo there sets those
accounts back by id if it is ever wanted.

Local revision, namespaced as custom/README.md explains.

Revision ID: c0030_verify_sso_linked_accounts
Revises: c0029_zero_config_thread_access
"""

from __future__ import annotations

import logging
from collections.abc import Sequence

from sqlalchemy import text

from alembic import op
from app.auth_oidc.settings import load_oidc_settings

revision: str = "c0030_verify_sso_linked_accounts"
down_revision: str | None = "c0029_zero_config_thread_access"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None

logger = logging.getLogger("alembic.runtime.migration")

VERIFY_SSO_LINKED = text(
    """
    UPDATE "user" AS u
       SET is_verified = TRUE
     WHERE u.is_active
       AND NOT u.is_verified
       AND EXISTS (
            SELECT 1
              FROM oauth_account AS o
             WHERE o.user_id = u.id
               AND o.oauth_name = :provider
               AND lower(o.account_email) = lower(u.email)
       )
    """
)


def upgrade() -> None:
    bind = op.get_bind()
    if bind.execute(text("SELECT to_regclass('oauth_account')")).scalar() is None:
        return
    provider = load_oidc_settings().provider_name
    changed = bind.execute(VERIFY_SSO_LINKED, {"provider": provider}).rowcount
    # A count, never who: the deploy steps list the accounts beforehand.
    logger.info("c0030: marked %s SSO-linked account(s) verified", changed)


def downgrade() -> None:
    # Nothing to undo in the schema; see the module docstring.
    pass
