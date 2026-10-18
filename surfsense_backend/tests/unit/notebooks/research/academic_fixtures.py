"""Recorded answers from the live scholarly APIs (Sep 2026), and a fetch that replays them.

No test reaches the network: each fake fetch maps a URL pattern to a recorded
body and fails loudly on anything it was not told about.
"""

from __future__ import annotations

import re
from pathlib import Path

FIXTURES = Path(__file__).parent / "fixtures"


def fixture(name: str) -> str:
    return (FIXTURES / name).read_text(encoding="utf-8")


class ReplayFetch:
    """``await fetch(url)`` answers from the first matching ``(pattern, body)``."""

    def __init__(self, *routes: tuple[str, str]) -> None:
        self.routes = [(re.compile(pattern), body) for pattern, body in routes]
        self.urls: list[str] = []

    async def __call__(self, url: str) -> str:
        self.urls.append(url)
        for pattern, body in self.routes:
            if pattern.search(url):
                return body
        raise AssertionError(f"unexpected fetch: {url}")
