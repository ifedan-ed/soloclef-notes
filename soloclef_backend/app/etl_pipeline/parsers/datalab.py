"""Datalab (Marker API) document -> markdown: the Cloud parser when
``ETL_CLOUD_SERVICE=DATALAB``.

``POST /api/v1/convert`` with the file, then poll the ``request_check_url``
the reply names until the conversion is ``complete`` or ``failed``. The
markdown comes back paginated: each page starts with a ``{N}----...`` line,
``N`` counting from 0, which is how chunks learn their page.

Modes and list prices (datalab.to/pricing, checked 2026-09-27): fast and
balanced $4 per 1,000 pages, accurate $10. Cloud always uses balanced;
tests on clinical PDFs found it matched accurate. ``total_cost`` in the reply is
in cents and is logged beside the page count.

Errors follow the LlamaCloud path: network failures, 429 and 5xx replies
and an empty result are retried with exponential backoff; a 4xx (bad key,
file refused), a conversion Datalab reports as failed, or one that outlives
the job timeout is not, since a resubmission is billed again.
The reference for the request and poll logic is the Datalab reader in the
clinical-assist indexer.
"""

from __future__ import annotations

import asyncio
import logging
import mimetypes
import os
import random
import re
import time
from pathlib import PurePosixPath

import httpx

from app.config import config as app_config
from app.etl_pipeline.constants import (
    DATALAB_BASE_DELAY,
    DATALAB_MAX_DELAY,
    DATALAB_MAX_RETRIES,
    DATALAB_POLL_INTERVAL,
    DATALAB_RETRYABLE_EXCEPTIONS,
    calculate_job_timeout,
    calculate_upload_timeout,
)
from app.source_pages.paged_markdown import PagedMarkdown, pages_by_number

logger = logging.getLogger(__name__)

DATALAB_MODES: tuple[str, ...] = ("fast", "balanced", "accurate")
DEFAULT_DATALAB_MODE = "balanced"
#: US dollars per 1,000 pages, Datalab's list price per mode.
DATALAB_PRICE_PER_1000_PAGES: dict[str, float] = {
    "fast": 4.0,
    "balanced": 4.0,
    "accurate": 10.0,
}

_PAGE_MARKER = re.compile(r"(?m)^[ \t]*\{(\d+)\}-{8,}[ \t]*\n?")


class DatalabError(RuntimeError):
    """Datalab refused or failed a conversion. ``retryable`` says if trying again may help."""

    def __init__(self, message: str, *, retryable: bool = False) -> None:
        super().__init__(message)
        self.retryable = retryable


def datalab_mode(value: str | None = None) -> str:
    """``value`` if it names a Datalab mode, else balanced."""
    mode = (value or "").strip().lower()
    return mode if mode in DATALAB_MODES else DEFAULT_DATALAB_MODE


def split_pages(markdown: str) -> tuple[str, list[str] | None]:
    """The markdown without Datalab's page markers, and each page's markdown.

    Text before the first marker belongs to the first page. No markers: the
    text as-is and no page list.
    """
    markers = list(_PAGE_MARKER.finditer(markdown or ""))
    if not markers:
        return markdown or "", None
    numbered: list[tuple[int, str]] = []
    lead = markdown[: markers[0].start()]
    for i, marker in enumerate(markers):
        end = markers[i + 1].start() if i + 1 < len(markers) else len(markdown)
        body = markdown[marker.end() : end]
        if i == 0 and lead.strip():
            body = lead + body
        numbered.append((int(marker.group(1)) + 1, body.strip("\n")))
    pages = pages_by_number(numbered)
    text = "\n\n".join(page for page in pages if page.strip())
    return text, pages


def _mime_type(filename: str) -> str:
    guessed, _ = mimetypes.guess_type(filename)
    return guessed or "application/octet-stream"


def _reply_json(response: httpx.Response, what: str) -> dict:
    if response.status_code == 429 or response.status_code >= 500:
        raise DatalabError(
            f"{what} ({response.status_code}): {response.text[:300]}", retryable=True
        )
    if response.status_code >= 400:
        raise DatalabError(f"{what} ({response.status_code}): {response.text[:300]}")
    try:
        data = response.json()
    except ValueError as exc:
        raise DatalabError(f"{what}: the reply was not JSON", retryable=True) from exc
    if not isinstance(data, dict):
        raise DatalabError(f"{what}: unexpected reply", retryable=True)
    return data


async def _convert_once(
    client: httpx.AsyncClient,
    *,
    file_path: str,
    filename: str,
    mode: str,
    api_key: str,
    base_url: str,
    poll_interval: float,
    max_wait_seconds: float,
) -> dict:
    headers = {"X-Api-Key": api_key}
    with open(file_path, "rb") as handle:
        content = handle.read()
    response = await client.post(
        base_url,
        headers=headers,
        files={"file": (filename, content, _mime_type(filename))},
        data={
            "output_format": "markdown",
            "mode": mode,
            "paginate": "true",
            "disable_image_extraction": "true",
        },
    )
    submitted = _reply_json(response, "Datalab refused the request")
    if submitted.get("success") is False:
        raise DatalabError(
            f"Datalab refused the request: {submitted.get('error') or submitted}"
        )
    check_url = submitted.get("request_check_url")
    if not check_url:
        raise DatalabError("Datalab did not return a request_check_url")

    started = time.monotonic()
    while True:
        await asyncio.sleep(poll_interval)
        if time.monotonic() - started > max_wait_seconds:
            # Not retried: a resubmission would be billed again.
            raise DatalabError(
                f"Datalab conversion did not finish within {max_wait_seconds:.0f}s"
            )
        try:
            polled = await client.get(check_url, headers=headers)
            result = _reply_json(polled, "Datalab status check failed")
        except (*DATALAB_RETRYABLE_EXCEPTIONS, DatalabError) as exc:
            # A blip while polling does not lose the job; keep asking
            # until the wait runs out. A refusal (4xx) is final.
            if isinstance(exc, DatalabError) and not exc.retryable:
                raise
            logger.debug("Datalab poll hiccup: %s", exc)
            continue
        status = str(result.get("status") or "").lower()
        if status == "complete":
            break
        if status in ("failed", "error"):
            raise DatalabError(
                f"Datalab conversion failed: {result.get('error') or status}"
            )

    if result.get("success") is False or result.get("error"):
        raise DatalabError(f"Datalab conversion failed: {result.get('error')}")
    return result


def _cost_cents(result: dict) -> float | None:
    cents = result.get("total_cost")
    if cents is None:
        cents = (result.get("cost_breakdown") or {}).get("final_cost_cents")
    try:
        return float(cents) if cents is not None else None
    except (TypeError, ValueError):
        return None


async def parse_with_datalab(
    file_path: str,
    filename: str,
    estimated_pages: int,
    *,
    api_key: str | None = None,
    mode: str | None = None,
    base_url: str | None = None,
    transport: httpx.AsyncBaseTransport | None = None,
    poll_interval: float = DATALAB_POLL_INTERVAL,
    max_retries: int = DATALAB_MAX_RETRIES,
    base_delay: float = DATALAB_BASE_DELAY,
) -> str:
    """Convert ``file_path`` with Datalab; markdown carrying its page texts."""
    key = api_key if api_key is not None else app_config.DATALAB_API_KEY
    if not key:
        raise DatalabError("DATALAB_API_KEY is not configured")
    chosen_mode = datalab_mode(mode)
    url = base_url or app_config.DATALAB_BASE_URL

    file_size = os.path.getsize(file_path)
    upload_timeout = calculate_upload_timeout(file_size)
    max_wait = calculate_job_timeout(estimated_pages, file_size)
    timeout = httpx.Timeout(
        connect=60.0, read=upload_timeout, write=upload_timeout, pool=60.0
    )
    name = PurePosixPath(filename).name or "document"

    logger.info(
        "Datalab parse: mode=%s size=%.1fMB pages~%s max_wait=%.0fs",
        chosen_mode,
        file_size / (1024 * 1024),
        estimated_pages,
        max_wait,
    )

    errors: list[str] = []
    last: Exception | None = None
    for attempt in range(1, max_retries + 1):
        try:
            async with httpx.AsyncClient(
                timeout=timeout, transport=transport
            ) as client:
                result = await _convert_once(
                    client,
                    file_path=file_path,
                    filename=name,
                    mode=chosen_mode,
                    api_key=key,
                    base_url=url,
                    poll_interval=poll_interval,
                    max_wait_seconds=max_wait,
                )
            text, pages = split_pages(str(result.get("markdown") or ""))
            if not text.strip():
                raise DatalabError(
                    "Datalab returned empty/whitespace-only content", retryable=True
                )
            logger.info(
                "Datalab parse done: mode=%s pages=%s cost_cents=%s attempt=%d",
                chosen_mode,
                result.get("page_count"),
                _cost_cents(result),
                attempt,
            )
            return PagedMarkdown(text, pages) if pages else text
        except (*DATALAB_RETRYABLE_EXCEPTIONS, DatalabError) as exc:
            if isinstance(exc, DatalabError) and not exc.retryable:
                raise
            last = exc
            errors.append(f"Attempt {attempt}: {type(exc).__name__} - {str(exc)[:200]}")
            if attempt < max_retries:
                delay = min(base_delay * (2 ** (attempt - 1)), DATALAB_MAX_DELAY)
                delay += delay * 0.25 * (2 * random.random() - 1)
                logger.warning(
                    "Datalab parse failed (attempt %d/%d): %s. Retrying in %.0fs",
                    attempt,
                    max_retries,
                    type(exc).__name__,
                    delay,
                )
                await asyncio.sleep(delay)
            else:
                logger.error(
                    "Datalab parse failed after %d attempts: %s",
                    max_retries,
                    "; ".join(errors),
                )
    raise last or DatalabError(f"Datalab parsing failed after {max_retries} attempts")
