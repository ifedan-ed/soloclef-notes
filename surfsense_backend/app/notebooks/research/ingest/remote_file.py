"""Download a linked document file and parse it like an upload with no parser chosen.

Uploads that name no parser use the server's default (``resolve_upload_parser``),
and are billed ETL page credits for that parser; a linked file gets the same.
"""

from __future__ import annotations

import contextlib
import logging
import os
import tempfile
from urllib.parse import urljoin
from uuid import UUID

from curl_cffi.requests import AsyncSession as CurlSession
from sqlalchemy.ext.asyncio import AsyncSession

from app.utils.crawl import is_publicly_routable

from ..curl_http import HEADERS
from .fetched import FetchedContent
from .kind import file_name

logger = logging.getLogger(__name__)

MAX_FILE_BYTES = 50 * 1024 * 1024
_MAX_REDIRECTS = 5
_TIMEOUT_S = 60.0
# libcurl, not httpx: arXiv cuts python-httpx downloads off after 1 MB and
# answers its API requests 406, while serving curl normally (see curl_http).
_HEADERS = HEADERS


class RemoteFileError(Exception):
    """The file could not be downloaded safely."""


async def download(url: str, destination: str) -> None:
    """Stream ``url`` to ``destination``, refusing private hosts at every redirect hop."""
    async with CurlSession() as client:
        for _ in range(_MAX_REDIRECTS + 1):
            if not is_publicly_routable(url):
                raise RemoteFileError("refusing a non-public address")
            response = await client.get(
                url,
                headers=_HEADERS,
                timeout=_TIMEOUT_S,
                allow_redirects=False,
                stream=True,
            )
            try:
                if 300 <= response.status_code < 400 and response.headers.get(
                    "location"
                ):
                    url = urljoin(url, response.headers["location"])
                    continue
                if response.status_code >= 400:
                    raise RemoteFileError(f"answered {response.status_code}")
                size = 0
                with open(destination, "wb") as handle:
                    async for chunk in response.aiter_content():
                        size += len(chunk)
                        if size > MAX_FILE_BYTES:
                            raise RemoteFileError("file too large")
                        handle.write(chunk)
                return
            finally:
                await response.aclose()
    raise RemoteFileError("too many redirects")


async def fetch_remote_file(
    session: AsyncSession,
    url: str,
    user_id: UUID | str,
    *,
    name: str | None = None,
) -> FetchedContent | None:
    """Parse the file at ``url``; ``name`` stands in when the URL has no file name
    (``arxiv.org/pdf/2401.00001`` is a PDF without saying so)."""
    from app.billing.parsers import parser_within_plan
    from app.etl_pipeline.cache import extract_with_cache
    from app.etl_pipeline.etl_document import EtlRequest
    from app.etl_pipeline.parser_selection import resolve_upload_parser
    from app.services.etl_credit_service import EtlCreditService

    name = name or file_name(url) or "download"
    suffix = os.path.splitext(name)[1]
    handle, path = tempfile.mkstemp(suffix=suffix)
    os.close(handle)
    try:
        await download(url, path)
        # Cloud parsing when the plan includes it, else the local parser.
        parser = await parser_within_plan(user_id, resolve_upload_parser(None))
        credits = EtlCreditService(session)
        pages = credits.estimate_pages_before_processing(path)
        await credits.check_credits(str(user_id), pages, parser=parser)
        result = await extract_with_cache(
            EtlRequest(
                file_path=path, filename=name, estimated_pages=pages, parser=parser
            )
        )
        try:
            await credits.charge_credits(str(user_id), pages, parser=parser)
        except Exception:
            logger.exception("charging ETL pages for %s failed", url)
        if not result.markdown_content.strip():
            return None
        return FetchedContent(
            markdown=result.markdown_content,
            title=os.path.splitext(name)[0] or None,
            metadata={"FILE_NAME": name, "ETL_SERVICE": result.etl_service},
        )
    finally:
        with contextlib.suppress(OSError):
            os.unlink(path)
