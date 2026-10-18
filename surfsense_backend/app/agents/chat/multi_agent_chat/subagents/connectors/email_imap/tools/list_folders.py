import logging
from typing import Any

from langchain_core.tools import tool
from sqlalchemy.ext.asyncio import AsyncSession

from app.connectors.email_imap.client import ImapConnectionError
from app.connectors.email_imap.service import (
    EmailAccountNotFoundError,
    list_folders_for_account as _list_folders,
)

logger = logging.getLogger(__name__)


def create_list_email_folders_tool(
    db_session: AsyncSession | None = None,
    workspace_id: int | None = None,
    user_id: str | None = None,
):
    @tool
    async def list_email_folders(account_id: str) -> dict[str, Any]:
        """List the mail folders of one connected email account.

        Args:
            account_id: An account id from `list_email_accounts`.

        Returns:
            {"status": "success", "folders": [str, ...]}
            or {"status": "error"/"not_found", "message": str}
        """
        if db_session is None or workspace_id is None or user_id is None:
            return {"status": "error", "message": "Email connector not properly configured."}
        try:
            folders = await _list_folders(
                db_session,
                workspace_id=workspace_id,
                user_id=user_id,
                account_id=account_id,
                for_workspace_use=True,
            )
            return {"status": "success", "folders": folders}
        except EmailAccountNotFoundError:
            return {"status": "not_found", "message": "That email account was not found."}
        except ImapConnectionError as exc:
            return {"status": "error", "message": f"Could not connect: {exc}"}
        except Exception as exc:
            logger.error("list_email_folders failed: %s", exc, exc_info=True)
            return {"status": "error", "message": "Could not list folders."}

    return list_email_folders
