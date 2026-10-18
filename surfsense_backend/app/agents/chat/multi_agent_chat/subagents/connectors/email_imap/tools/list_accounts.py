import logging
from typing import Any

from langchain_core.tools import tool
from sqlalchemy.ext.asyncio import AsyncSession

from app.connectors.email_imap.service import list_accounts

logger = logging.getLogger(__name__)


def create_list_email_accounts_tool(
    db_session: AsyncSession | None = None,
    workspace_id: int | None = None,
    user_id: str | None = None,
):
    @tool
    async def list_email_accounts() -> dict[str, Any]:
        """List the user's connected email (IMAP) accounts.

        Returns each account's label, host, and username — never a password.
        Call this first if you don't already know which accounts the user has
        connected, or if they ask "which email accounts do you have".

        Returns:
            {"status": "success", "accounts": [{"id", "label", "host", "username", ...}]}
            or {"status": "error", "message": str} if misconfigured.
        """
        if db_session is None or workspace_id is None or user_id is None:
            return {
                "status": "error",
                "message": "Email connector not properly configured.",
            }
        try:
            accounts = await list_accounts(
                db_session,
                workspace_id=workspace_id,
                user_id=user_id,
                for_workspace_use=True,
            )
            return {"status": "success", "accounts": accounts}
        except Exception as exc:
            logger.error("list_email_accounts failed: %s", exc, exc_info=True)
            return {"status": "error", "message": "Could not list email accounts."}

    return list_email_accounts
