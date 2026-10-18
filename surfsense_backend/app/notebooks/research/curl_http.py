"""HTTP for research fetches through libcurl rather than httpx.

arXiv's edge refuses python-httpx's TLS/HTTP fingerprint outright (406 on the
API, PDFs cut off after 1 MB) while serving curl normally; measured live, eight
alternating requests went 4/4 406 for httpx and 4/4 200 for curl. curl_cffi is
already installed with the scrapers, and a plain (non-impersonating) session
looks like the curl it is.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from curl_cffi.requests import AsyncSession

USER_AGENT = "SurfSense/1.0 (notebook research)"
HEADERS = {"User-Agent": USER_AGENT, "Accept-Encoding": "gzip"}


@dataclass(frozen=True)
class Reply:
    status: int
    text: str
    headers: dict[str, str] = field(default_factory=dict)


async def curl_get(url: str, timeout: float = 30.0) -> Reply:
    """GET ``url`` following redirects; the body decoded as text."""
    async with AsyncSession() as session:
        response = await session.get(
            url, headers=HEADERS, timeout=timeout, allow_redirects=True
        )
    return Reply(
        status=response.status_code,
        text=response.text,
        headers={k.lower(): v for k, v in response.headers.items()},
    )
