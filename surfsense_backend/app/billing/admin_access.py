"""Who may administer billing: superusers and ``BILLING_ADMIN_EMAILS``."""

from __future__ import annotations

from fastapi import Depends, HTTPException, status

from app.auth.context import AuthContext
from app.users import require_session_context

from .ledger import is_config_admin


async def require_billing_admin(
    auth: AuthContext = Depends(require_session_context),
) -> AuthContext:
    if not is_config_admin(auth.user):
        raise HTTPException(
            status_code=status.HTTP_403_FORBIDDEN, detail="Billing administrators only."
        )
    return auth
