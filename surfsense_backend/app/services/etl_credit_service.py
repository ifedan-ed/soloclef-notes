"""
Page billing for ETL document processing, through ``app.billing``.

Page counts are estimated as before; each page is priced by the parser that
reads it (``cloud_page`` / ``local_page`` credits). The billing mode (off /
shadow / live) decides whether anything is recorded or enforced; the old
``ETL_CREDIT_BILLING_ENABLED`` flag no longer gates it.
"""

import logging
import os
from pathlib import Path, PurePosixPath

from sqlalchemy.ext.asyncio import AsyncSession

from app.config import config

logger = logging.getLogger(__name__)


class InsufficientCreditsError(Exception):
    """Raised when live billing refuses to process a document (plan or credit)."""

    def __init__(
        self,
        message: str = "Insufficient credits to process this document. "
        "Add more credits to continue.",
        balance_micros: int = 0,
        required_micros: int = 0,
        code: str = "INSUFFICIENT_CREDITS",
    ):
        self.balance_micros = balance_micros
        self.required_micros = required_micros
        self.code = code
        super().__init__(message)


def _parser_name(parser, multiplier: int | None = None) -> str:
    """``"local"`` or ``"cloud"``; ``None`` means the deployment's default."""
    value = getattr(parser, "value", parser)
    if value in ("local", "cloud"):
        return value
    if multiplier == 0:
        return "local"
    from app.etl_pipeline.parser_selection import default_parser

    return default_parser().value


def page_action(parser) -> str:
    return "cloud_page" if _parser_name(parser) == "cloud" else "local_page"


class EtlCreditService:
    """Checks and charges ETL page processing through ``app.billing``.

    Pages are priced per parser (``cloud_page`` / ``local_page`` credits,
    admin-editable). ``check_credits`` runs before parsing: in live billing
    mode it refuses a parser the plan does not include, an upload over the
    plan's page cap, or a balance that cannot cover the estimate.
    ``charge_credits`` records the actual pages after success and never
    refuses. Shadow mode records what the pages would have cost; ``off`` does
    nothing.

    ``multiplier`` is the old per-parser page multiplier; callers now pass the
    parser instead, and a multiplier of 0 still means the free local parser.
    """

    def __init__(self, session: AsyncSession):
        self.session = session

    @staticmethod
    def billing_enabled() -> bool:
        return config.ETL_CREDIT_BILLING_ENABLED

    @staticmethod
    def pages_to_micros(pages: int, multiplier: int = 1) -> int:
        """Legacy conversion kept for display code: pages x multiplier x rate."""
        return int(pages) * int(multiplier) * config.MICROS_PER_PAGE

    def _factory(self):
        from app.billing.meter import sibling_sessions

        return sibling_sessions(self.session)

    async def get_available_micros(self, user_id: str) -> int | None:
        """What the person can still parse, for connector batch planning.

        Expressed in :meth:`pages_to_micros` units (pages x
        ``MICROS_PER_PAGE``) so the indexers' "does this batch fit" arithmetic
        holds: it is the number of pages their balance buys at the default
        parser's price. ``None`` means unlimited: billing not live, an exempt
        account, or free pages.
        """
        from app.billing import ledger
        from app.billing.meter import action_price
        from app.billing.settings_store import load_settings

        try:
            async with self._factory()() as session:
                if (await load_settings(session)).mode != "live":
                    return None
                account, user = await ledger.lock_account(session, user_id)
                exempt = account.exempt or ledger.is_config_admin(user)
                spendable = ledger.balances(account, user).spendable_micros
                per_page = await action_price(session, page_action(None))
                await session.commit()
            if exempt or per_page <= 0:
                return None
            return max(0, spendable) // per_page * config.MICROS_PER_PAGE
        except Exception:
            # A read for batch planning; the per-file check still enforces.
            logger.warning("billing balance read failed for %s", user_id, exc_info=True)
            return None

    async def check_credits(
        self,
        user_id: str,
        estimated_pages: int = 1,
        multiplier: int = 1,
        *,
        parser=None,
        workspace_id: int | None = None,
    ) -> None:
        """Refuse (live billing only) before any parsing happens.

        With ``workspace_id``, checked against that workspace's owner (the
        plan and balance that ``charge_credits`` will actually bill), not
        the uploader's own — same payer ``meter.begin``/``record`` resolve.
        """
        from app.billing import meter
        from app.billing.entitlements import Requirement
        from app.billing.errors import BillingDenied

        name = _parser_name(parser, multiplier)
        try:
            await meter.check(
                user_id,
                page_action(name),
                units=estimated_pages,
                need=Requirement(parser=name, pages=estimated_pages),
                workspace_id=workspace_id,
                session_factory=self._factory(),
            )
        except BillingDenied as exc:
            raise InsufficientCreditsError(
                exc.message,
                balance_micros=exc.available_micros,
                required_micros=exc.required_micros,
                code=exc.code,
            ) from exc
        except Exception:
            logger.warning(
                "billing check failed for %s; not refusing", user_id, exc_info=True
            )

    async def charge_credits(
        self,
        user_id: str,
        pages: int,
        multiplier: int = 1,
        *,
        parser=None,
        workspace_id: int | None = None,
        notebook_id: int | None = None,
        reference: str | None = None,
    ) -> int | None:
        """Record the pages actually parsed. Returns micros charged (would-be
        in shadow mode), never refuses."""
        from app.billing import meter
        from app.billing.entitlements import Requirement

        name = _parser_name(parser, multiplier)
        try:
            return await meter.record(
                user_id,
                page_action(name),
                units=pages,
                need=Requirement(parser=name, pages=pages),
                workspace_id=workspace_id,
                notebook_id=notebook_id,
                reference=reference,
                details={"parser": name},
                session_factory=self._factory(),
            )
        except Exception:
            # The document is already processed; a billing hiccup must not
            # fail it. The usage is simply not recorded.
            logger.exception("recording %s pages for %s failed", pages, user_id)
            return None

    def estimate_pages_from_elements(self, elements: list) -> int:
        """
        Estimate page count from document elements (for Unstructured).

        Args:
            elements: List of document elements

        Returns:
            Estimated number of pages
        """
        # For Unstructured, we can count unique page numbers in metadata
        # or estimate based on content length
        page_numbers = set()

        for element in elements:
            # Try to get page number from metadata
            if hasattr(element, "metadata") and element.metadata:
                page_num = element.metadata.get("page_number")
                if page_num is not None:
                    page_numbers.add(page_num)

        # If we found page numbers in metadata, use that count
        if page_numbers:
            return len(page_numbers)

        # Otherwise, estimate: assume ~2000 chars per page
        total_content_length = sum(
            len(element.page_content) if hasattr(element, "page_content") else 0
            for element in elements
        )
        estimated_pages = max(1, total_content_length // 2000)

        return estimated_pages

    def estimate_pages_from_markdown(self, markdown_documents: list) -> int:
        """
        Estimate page count from markdown documents (for LlamaCloud).

        Args:
            markdown_documents: List of markdown document objects

        Returns:
            Estimated number of pages
        """
        # For LlamaCloud, if split_by_page=True was used, each doc is a page
        # Otherwise, estimate based on content length
        if not markdown_documents:
            return 1

        # Check if documents have page metadata
        total_pages = 0
        for doc in markdown_documents:
            if hasattr(doc, "metadata") and doc.metadata:
                # If metadata contains page info, use it
                page_num = doc.metadata.get("page", doc.metadata.get("page_number"))
                if page_num is not None:
                    total_pages += 1
                    continue

            # Otherwise estimate from content length
            content_length = len(doc.text) if hasattr(doc, "text") else 0
            estimated = max(1, content_length // 2000)
            total_pages += estimated

        return max(1, total_pages)

    def estimate_pages_from_content_length(self, content_length: int) -> int:
        """
        Estimate page count from content length (for local parsers).

        Args:
            content_length: Length of the document content

        Returns:
            Estimated number of pages
        """
        # Estimate ~2000 characters per page
        return max(1, content_length // 2000)

    @staticmethod
    def estimate_pages_from_metadata(
        file_name_or_ext: str, file_size: int | str | None = None
    ) -> int:
        """Size-based page estimation from file name/extension and byte size.

        Pure function — no file I/O, no database access.  Used by cloud
        connectors (which only have API metadata) and as the internal
        fallback for :meth:`estimate_pages_before_processing`.

        ``file_name_or_ext`` can be a full filename (``"report.pdf"``) or
        a bare extension (``".pdf"``).  ``file_size`` may be an int, a
        stringified int from a cloud API, or *None*.
        """
        if file_size is not None:
            try:
                file_size = int(file_size)
            except (ValueError, TypeError):
                file_size = 0
        else:
            file_size = 0

        if file_size <= 0:
            return 1

        ext = PurePosixPath(file_name_or_ext).suffix.lower() if file_name_or_ext else ""
        if not ext and file_name_or_ext.startswith("."):
            ext = file_name_or_ext.lower()
        file_ext = ext

        if file_ext == ".pdf":
            return max(1, file_size // (100 * 1024))

        if file_ext in {
            ".doc",
            ".docx",
            ".docm",
            ".dot",
            ".dotm",
            ".odt",
            ".ott",
            ".sxw",
            ".stw",
            ".uot",
            ".rtf",
            ".pages",
            ".wpd",
            ".wps",
            ".abw",
            ".zabw",
            ".cwk",
            ".hwp",
            ".lwp",
            ".mcw",
            ".mw",
            ".sdw",
            ".vor",
        }:
            return max(1, file_size // (50 * 1024))

        if file_ext in {
            ".ppt",
            ".pptx",
            ".pptm",
            ".pot",
            ".potx",
            ".odp",
            ".otp",
            ".sxi",
            ".sti",
            ".uop",
            ".key",
            ".sda",
            ".sdd",
            ".sdp",
        }:
            return max(1, file_size // (200 * 1024))

        if file_ext in {
            ".xls",
            ".xlsx",
            ".xlsm",
            ".xlsb",
            ".xlw",
            ".xlr",
            ".ods",
            ".ots",
            ".fods",
            ".numbers",
            ".123",
            ".wk1",
            ".wk2",
            ".wk3",
            ".wk4",
            ".wks",
            ".wb1",
            ".wb2",
            ".wb3",
            ".wq1",
            ".wq2",
            ".csv",
            ".tsv",
            ".slk",
            ".sylk",
            ".dif",
            ".dbf",
            ".prn",
            ".qpw",
            ".602",
            ".et",
            ".eth",
        }:
            return max(1, file_size // (100 * 1024))

        if file_ext in {".epub"}:
            return max(1, file_size // (50 * 1024))

        if file_ext in {".txt", ".log", ".md", ".markdown", ".htm", ".html", ".xml"}:
            return max(1, file_size // 3000)

        if file_ext in {
            ".jpg",
            ".jpeg",
            ".png",
            ".gif",
            ".bmp",
            ".tiff",
            ".webp",
            ".svg",
            ".cgm",
            ".odg",
            ".pbd",
        }:
            return 1

        if file_ext in {".mp3", ".m4a", ".wav", ".mpga"}:
            return max(1, file_size // (1024 * 1024))

        if file_ext in {".mp4", ".mpeg", ".webm"}:
            return max(1, file_size // (5 * 1024 * 1024))

        return max(1, file_size // (80 * 1024))

    def estimate_pages_before_processing(self, file_path: str) -> int:
        """
        Estimate page count from a local file before processing.

        For PDFs, attempts to read the actual page count via pypdf.
        For everything else, delegates to :meth:`estimate_pages_from_metadata`.

        Args:
            file_path: Path to the file

        Returns:
            Estimated number of pages
        """
        if not os.path.exists(file_path):
            raise ValueError(f"File not found: {file_path}")

        file_ext = Path(file_path).suffix.lower()
        file_size = os.path.getsize(file_path)

        if file_ext == ".pdf":
            try:
                import pypdf

                with open(file_path, "rb") as f:
                    pdf_reader = pypdf.PdfReader(f)
                    return len(pdf_reader.pages)
            except Exception:
                pass  # fall through to size-based estimation

        return self.estimate_pages_from_metadata(file_ext, file_size)
