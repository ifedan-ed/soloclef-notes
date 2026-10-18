"""LlamaCloud (LlamaParse) document -> markdown: the Cloud parser when
``ETL_CLOUD_SERVICE=LLAMACLOUD``, and Datalab's fallback when a LlamaCloud
key is set beside ``ETL_CLOUD_SERVICE=DATALAB``.

LlamaCloud's v2 Parse API, through its ``llama-cloud`` SDK: the file goes to
``POST /api/v2/parse/upload`` (tier ``agentic``, version ``latest``), then
``GET /api/v2/parse/{job_id}?expand=markdown,usage`` is polled until the job
is COMPLETED, and its markdown pages become the page texts. (The v1 API that
``llama-cloud-services`` spoke, ``/api/parsing/upload``, tells some
accounts "The v1 Parse API is not available for this account".)

LlamaCloud caches a parse for 48 hours: the same file with the same options
gets the earlier job back, already COMPLETED, and is not billed again. That
is logged as ``cache_hit=True credits=0``; otherwise ``credits`` is what the
job was billed (10 a page on the agentic tier; None until LlamaCloud's
billing records it).

Limits (``app.etl_pipeline.constants``):

- Each request: the llama-cloud client tries again by itself after a network
  error or timeout, 408, 409, 429 or 5xx, ``LLAMACLOUD_REQUEST_RETRIES`` (5)
  more times with waits of about 0.5, 1, 2, 4 and 8 s (each up to 25%
  shorter; a ``Retry-After`` of up to 60 s instead). When those fail on the
  upload, the file fails at once, without a resubmission; on a poll, the
  same job is polled again until its wait runs out.
- Resubmitted here (a new attempt): an empty result and a job LlamaCloud
  ends FAILED or CANCELLED, up to ``LLAMACLOUD_MAX_RETRIES`` (5) attempts in
  all, waiting ``LLAMACLOUD_BASE_DELAY`` (10 s) doubling to
  ``LLAMACLOUD_MAX_DELAY`` (120 s), +-25% jitter. A resubmission asks for a
  fresh parse (``disable_cache``), so it is billed.
- Not resubmitted, since a resubmission is billed again: a refusal (401 bad
  key, 402 a plan out of credits, any other 4xx) and a job that outlives
  its wait.
- Times: LlamaCloud may run the job for the job timeout (10 minutes plus a
  minute a page, more for a large file; at most
  ``LLAMACLOUD_MAX_BASE_JOB_TIMEOUT``, 7,200 s) plus a minute a page; this
  server waits ``max(LLAMACLOUD_MIN_JOB_WAIT (2,000 s), job + upload
  timeout)`` for it. Each request may take the upload timeout (2 to 30
  minutes by file size) to send or read, and 2 minutes to connect. Polls
  come every 1 s, a second longer each time, at most 5 s.

tests/unit/etl_pipeline/test_llamacloud_parser.py pins each.
"""

import asyncio
import logging
import math
import os
import random
import time
from pathlib import Path

import httpx

from app.config import config as app_config
from app.etl_pipeline.constants import (
    LLAMACLOUD_BASE_DELAY,
    LLAMACLOUD_MAX_BASE_JOB_TIMEOUT,
    LLAMACLOUD_MAX_DELAY,
    LLAMACLOUD_MAX_POLL_INTERVAL,
    LLAMACLOUD_MAX_RETRIES,
    LLAMACLOUD_MIN_JOB_WAIT,
    LLAMACLOUD_POLL_INTERVAL,
    LLAMACLOUD_REQUEST_RETRIES,
    PER_PAGE_JOB_TIMEOUT,
    calculate_job_timeout,
    calculate_upload_timeout,
)
from app.source_pages.paged_markdown import PagedMarkdown, pages_by_number

#: Cloud parsing is always LlamaParse's agentic tier, v1's
#: parse_page_with_agent. The cheaper tiers were "Basic" before the
#: per-upload choice became Local vs Cloud; Local now covers that ground.
LLAMA_PARSE_TIER = "agentic"
#: The tier's newest release, as v1 always gave. A dated version from
#: ``GET /api/v2/parse/versions`` would freeze its output instead.
LLAMA_PARSE_VERSION = "latest"
#: What each poll asks for: page markdown and the credits the job was billed.
RESULT_EXPAND = ("markdown", "usage")

_ENDED_IN_FAILURE = frozenset({"FAILED", "CANCELLED"})
#: Statuses below 500 that the llama-cloud client itself retries; a poll
#: still answered with one after those retries polls the same job again.
_TRANSIENT_STATUS = frozenset({408, 409, 429})


class LlamaCloudJobFailedError(Exception):
    """LlamaCloud ended the job FAILED or CANCELLED; the file is resubmitted."""


class LlamaCloudWaitExceededError(Exception):
    """The job outlived this server's wait for it; not resubmitted."""


def _markdown_pages(result) -> list:
    return getattr(getattr(result, "markdown", None), "pages", None) or []


def _extract_content(result) -> str:
    """The job's markdown, carrying each page's markdown (``PagedMarkdown``).

    Pages LlamaCloud could not process (``success: false``) are left out;
    their page numbers stay empty so the later pages keep theirs.
    """
    numbered = [
        (page.page_number, page.markdown or "")
        for page in _markdown_pages(result)
        if getattr(page, "success", False) and isinstance(page.page_number, int)
    ]
    content = "\n\n".join(text for _, text in numbered)
    if not content:
        return content
    return PagedMarkdown(content, pages_by_number(numbered))


def _failed_pages(result) -> list[int]:
    return [
        page.page_number
        for page in _markdown_pages(result)
        if not getattr(page, "success", False)
    ]


def _credits(result) -> float | None:
    """Credits LlamaCloud billed for the job; None until its billing records it."""
    usage = getattr(getattr(result, "job", None), "usage", None)
    return getattr(usage, "credits", None)


def _is_transient(exc: Exception) -> bool:
    from llama_cloud import APIConnectionError, APIStatusError

    if isinstance(exc, APIConnectionError):
        return True
    return isinstance(exc, APIStatusError) and (
        exc.status_code in _TRANSIENT_STATUS or exc.status_code >= 500
    )


async def _wait_for_result(client, job_id: str, wait: float, poll_interval):
    """Poll the job until it ends; the finished job with its markdown."""
    from llama_cloud import APIConnectionError, APIStatusError

    started = time.monotonic()
    interval = LLAMACLOUD_POLL_INTERVAL if poll_interval is None else poll_interval
    status = "PENDING"
    while True:
        await asyncio.sleep(interval)
        try:
            result = await client.parsing.get(job_id, expand=list(RESULT_EXPAND))
        except (APIConnectionError, APIStatusError) as exc:
            if not _is_transient(exc):
                raise
            logging.warning(
                "LlamaCloud poll of job %s failed (%s); polling it again",
                job_id,
                type(exc).__name__,
            )
        else:
            status = result.job.status
            if status == "COMPLETED":
                return result
            if status in _ENDED_IN_FAILURE:
                raise LlamaCloudJobFailedError(
                    f"LlamaCloud job {job_id} ended {status}: "
                    f"{result.job.error_message or 'no error message'}"
                )
        waited = time.monotonic() - started
        if waited > wait:
            raise LlamaCloudWaitExceededError(
                f"Timeout while parsing the file: LlamaCloud job {job_id} was "
                f"still {status} after {waited:.0f}s (wait {wait:.0f}s)"
            )
        if poll_interval is None:
            interval = min(interval + 1, LLAMACLOUD_MAX_POLL_INTERVAL)


async def parse_with_llamacloud(
    file_path: str,
    estimated_pages: int,
    *,
    api_key: str | None = None,
    transport: httpx.AsyncBaseTransport | None = None,
    max_retries: int = LLAMACLOUD_MAX_RETRIES,
    base_delay: float = LLAMACLOUD_BASE_DELAY,
    poll_interval: float | None = None,
) -> str:
    """Parse ``file_path`` with LlamaCloud; markdown carrying its page texts.

    The keyword arguments exist for tests and tools; the pipeline passes
    none. ``poll_interval`` replaces the polls' own (1 s growing to 5 s).
    """
    from llama_cloud import AsyncLlamaCloud

    file_size_bytes = os.path.getsize(file_path)
    file_size_mb = file_size_bytes / (1024 * 1024)

    upload_timeout = calculate_upload_timeout(file_size_bytes)
    job_timeout = calculate_job_timeout(estimated_pages, file_size_bytes)
    wait = max(LLAMACLOUD_MIN_JOB_WAIT, job_timeout + upload_timeout)
    http_timeout = httpx.Timeout(
        connect=120.0,
        read=upload_timeout,
        write=upload_timeout,
        pool=120.0,
    )
    server_timeouts = {
        "base_in_seconds": min(math.ceil(job_timeout), LLAMACLOUD_MAX_BASE_JOB_TIMEOUT),
        "extra_time_per_page_in_seconds": PER_PAGE_JOB_TIMEOUT,
    }

    logging.info(
        f"LlamaCloud upload configured: file_size={file_size_mb:.1f}MB, "
        f"pages={estimated_pages}, upload_timeout={upload_timeout:.0f}s, "
        f"job_timeout={job_timeout:.0f}s, wait={wait:.0f}s, "
        f"tier={LLAMA_PARSE_TIER}, version={LLAMA_PARSE_VERSION}"
    )

    last_exception = None
    attempt_errors: list[str] = []

    key = api_key if api_key is not None else app_config.LLAMA_CLOUD_API_KEY
    for attempt in range(1, max_retries + 1):
        try:
            async with httpx.AsyncClient(
                timeout=http_timeout, transport=transport
            ) as http_client:
                # Reads LLAMA_CLOUD_BASE_URL itself (unset: the US region).
                client = AsyncLlamaCloud(
                    api_key=key,
                    http_client=http_client,
                    timeout=http_timeout,
                    max_retries=LLAMACLOUD_REQUEST_RETRIES,
                )
                job = await client.parsing.create(
                    tier=LLAMA_PARSE_TIER,
                    version=LLAMA_PARSE_VERSION,
                    upload_file=Path(file_path),
                    processing_options={"ocr_parameters": {"languages": ["en"]}},
                    processing_control={"timeouts": server_timeouts},
                    # A resubmission would otherwise get the cached job back.
                    **({"disable_cache": True} if attempt > 1 else {}),
                )
                # Only a job LlamaCloud already had is COMPLETED at upload.
                cache_hit = job.status == "COMPLETED"
                result = await _wait_for_result(client, job.id, wait, poll_interval)

            if attempt > 1:
                logging.info(
                    f"LlamaCloud upload succeeded on attempt {attempt} after "
                    f"{len(attempt_errors)} failures"
                )

            content = _extract_content(result)
            if not content or not content.strip():
                raise RuntimeError("LlamaCloud returned empty/whitespace-only content")
            # Logged as Datalab logs its cost; a cache hit is billed nothing.
            logging.info(
                "LlamaCloud parse done: tier=%s version=%s pages=%d "
                "failed_pages=%s cache_hit=%s credits=%s attempt=%d job=%s",
                LLAMA_PARSE_TIER,
                LLAMA_PARSE_VERSION,
                len(_markdown_pages(result)),
                _failed_pages(result) or 0,
                cache_hit,
                0 if cache_hit else _credits(result),
                attempt,
                job.id,
            )
            return content

        except (RuntimeError, LlamaCloudJobFailedError) as e:
            last_exception = e
            error_type = type(e).__name__
            error_msg = str(e)[:200]
            attempt_errors.append(f"Attempt {attempt}: {error_type} - {error_msg}")

            if attempt < max_retries:
                backoff = min(
                    base_delay * (2 ** (attempt - 1)),
                    LLAMACLOUD_MAX_DELAY,
                )
                jitter = backoff * 0.25 * (2 * random.random() - 1)
                delay = backoff + jitter

                logging.warning(
                    f"LlamaCloud upload failed "
                    f"(attempt {attempt}/{max_retries}): "
                    f"{error_type}. File: {file_size_mb:.1f}MB. "
                    f"Retrying in {delay:.0f}s..."
                )
                await asyncio.sleep(delay)
            else:
                logging.error(
                    f"LlamaCloud upload failed after {max_retries} "
                    f"attempts. File size: {file_size_mb:.1f}MB, "
                    f"Pages: {estimated_pages}. "
                    f"Errors: {'; '.join(attempt_errors)}"
                )

    raise last_exception or RuntimeError(
        f"LlamaCloud parsing failed after {max_retries} retries. "
        f"File size: {file_size_mb:.1f}MB"
    )
