import logging
from typing import Any

from langchain_core.tools import tool
from sqlalchemy.ext.asyncio import AsyncSession

from app.connectors.email_imap.client import ImapConnectionError
from app.connectors.email_imap.service import (
    EmailAccountNotFoundError,
    read_attachment_text as _read_attachment,
    read_one_message as _read_message,
)

logger = logging.getLogger(__name__)


def create_read_email_tool(
    db_session: AsyncSession | None = None,
    workspace_id: int | None = None,
    user_id: str | None = None,
):
    @tool
    async def read_email(
        account_id: str,
        folder: str,
        uid: str,
        attachment_filename: str | None = None,
    ) -> dict[str, Any]:
        """Read one email's headers, plain-text body, and attachment list.

        Call `search_emails` first to find the `account_id`/`folder`/`uid` of
        the message you want. This never marks the message as read on the
        server and can never send, delete, move, or flag mail.

        Args:
            account_id: Account id (from `search_emails`/`list_email_accounts`).
            folder: Folder the message is in (from the `search_emails` result).
            uid: The message's uid (from `search_emails`).
            attachment_filename: If set, also extract and return the named
                attachment's content (best-effort). Readable types: PDF
                (including scanned/image-only PDFs, via OCR), plain text,
                Word (.docx), PowerPoint (.pptx), Excel (.xlsx/.xls), CSV,
                and images (.png/.jpg/.webp/.heic/.heif — described and
                OCR'd by the workspace's vision model, metered like any
                other vision call). Spreadsheets are capped to the first
                200 rows per sheet. Legacy binary .doc is NOT supported
                (no converter installed) and returns a note saying so.
                Attachments over 25 MB are skipped with a note instead of
                being read.

        Returns:
            {"status": "success", "message": {"from","to","subject","date",
             "body", "attachments": [{"filename","content_type","size"}],
             "attachment_text": str | null}}
        """
        if db_session is None or workspace_id is None or user_id is None:
            return {"status": "error", "message": "Email connector not properly configured."}
        try:
            summary = await _read_message(
                db_session,
                workspace_id=workspace_id,
                user_id=user_id,
                account_id=account_id,
                folder=folder,
                uid=uid,
                for_workspace_use=True,
            )
            attachment_text = None
            if attachment_filename:
                attachment_text = await _read_attachment(
                    db_session,
                    workspace_id=workspace_id,
                    user_id=user_id,
                    account_id=account_id,
                    folder=folder,
                    uid=uid,
                    filename=attachment_filename,
                    for_workspace_use=True,
                )
            summary["attachment_text"] = attachment_text
            return {"status": "success", "message": summary}
        except EmailAccountNotFoundError:
            return {"status": "not_found", "message": "That email account was not found."}
        except ImapConnectionError as exc:
            return {"status": "error", "message": f"Could not read message: {exc}"}
        except Exception as exc:
            logger.error("read_email failed: %s", exc, exc_info=True)
            return {"status": "error", "message": "Could not read that email."}

    return read_email
