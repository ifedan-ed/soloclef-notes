"""Who may send announcements: the site's administrators.

The same people who administer billing: superusers and ``BILLING_ADMIN_EMAILS``.
"""

from __future__ import annotations

from fastapi import Depends, HTTPException, status

from app.auth.context import AuthContext
from app.billing.ledger import is_config_admin
from app.users import require_session_context


async def require_site_admin(
    auth: AuthContext = Depends(require_session_context),
) -> AuthContext:
    if not is_config_admin(auth.user):
        raise HTTPException(
            status_code=status.HTTP_403_FORBIDDEN,
            detail="Site administrators only.",
        )
    return auth
