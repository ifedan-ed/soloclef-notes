"""Scholarly APIs are asked politely: spaced per host, retried when they throttle."""

from __future__ import annotations

import pytest

from app.notebooks.research.academic import polite_http
from app.notebooks.research.academic.polite_http import (
    FetchFailedError,
    HostPacer,
    ThrottledError,
    build_url,
    min_interval_for,
    polite_get_text,
)
from app.notebooks.research.curl_http import HEADERS, Reply

pytestmark = pytest.mark.unit


@pytest.fixture(autouse=True)
def _no_waiting(monkeypatch):
    monkeypatch.setattr(polite_http, "_RETRY_BASE_S", 0.0)
    monkeypatch.setattr(polite_http, "_pacer", HostPacer(lambda host: 0.0))


def test_arxiv_is_asked_every_three_seconds_and_ncbi_three_times_a_second():
    assert min_interval_for("export.arxiv.org") == 3.0
    assert min_interval_for("eutils.ncbi.nlm.nih.gov") == pytest.approx(0.34)


def test_pacer_spaces_one_host_but_not_another():
    pacer = HostPacer(lambda host: 3.0 if host == "a" else 0.0)
    assert pacer.reserve("a") == 0
    assert pacer.reserve("a") == pytest.approx(3.0, abs=0.05)
    assert pacer.reserve("b") == 0


def test_build_url_keeps_colons_and_drops_nones():
    assert build_url("https://x/q", {"s": "all:a b", "n": None, "m": 3}) == (
        "https://x/q?s=all:a%20b&m=3"
    )


def test_requests_offer_gzip_only():
    # arXiv's edge answers 406 to a request offering zstd.
    assert HEADERS["Accept-Encoding"] == "gzip"


async def test_a_throttling_answer_is_retried():
    answers = iter([Reply(406, ""), Reply(200, "<feed/>")])
    sent = []

    async def send(url):
        sent.append(url)
        return next(answers)

    text = await polite_get_text("https://export.arxiv.org/api/query?x=1", send=send)
    assert text == "<feed/>"
    assert len(sent) == 2


async def test_persistent_throttling_raises():
    async def send(url):
        return Reply(429, "")

    with pytest.raises(ThrottledError):
        await polite_get_text("https://www.ebi.ac.uk/x", send=send)


async def test_a_real_error_is_not_retried():
    calls = []

    async def send(url):
        calls.append(url)
        return Reply(404, "")

    with pytest.raises(FetchFailedError):
        await polite_get_text("https://api.biorxiv.org/x", send=send)
    assert len(calls) == 1


async def test_an_unreachable_host_is_a_fetch_failure():
    async def send(url):
        raise OSError("connection refused")

    with pytest.raises(FetchFailedError):
        await polite_get_text("https://api.biorxiv.org/x", send=send)
