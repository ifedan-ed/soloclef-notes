"""Browserbase as a chat tool: open one public page and keep its text.

Browserbase is stood in at its HTTP boundary and the remote browser by the
same fakes the research box's tests use; the reader and guards are real.
"""

from __future__ import annotations

import pytest

from app.capabilities.browserbase.read_page.definition import read_page_capability
from app.capabilities.browserbase.read_page.executor import (
    COULD_NOT_READ,
    NOT_CONNECTED,
    NOT_PUBLIC,
    build_read_page_executor,
)
from app.capabilities.browserbase.read_page.schemas import ReadPageInput
from app.capabilities.core.types import BillingUnit
from app.capabilities.core.unavailable import CapabilityUnavailableError
from app.notebooks.research.page_read.browserbase_api import BrowserbaseApi
from tests.e2e.fakes.browserbase import (
    E2E_JS_PAGE_HTML,
    E2E_JS_PAGE_TEXT,
    E2E_JS_PAGE_TITLE,
    E2E_KEY,
    FakeBrowser,
    FakeBrowserbase,
)

pytestmark = pytest.mark.unit

URL = "https://93.184.215.14/e2e-js-page"


def _executor(bb, browser, *, key=E2E_KEY, lookups=None):
    async def lookup():
        if lookups is not None:
            lookups.append(True)
        return key

    return build_read_page_executor(
        7,
        lookup_key=lookup,
        api_factory=lambda k: BrowserbaseApi(
            k, base_url="https://bb.test", transport=bb.transport()
        ),
        loader=browser,
    )


async def test_the_page_is_read_in_browserbase_and_the_session_ended():
    bb = FakeBrowserbase()
    browser = FakeBrowser(pages={URL: (E2E_JS_PAGE_TITLE, E2E_JS_PAGE_HTML)})

    output = await _executor(bb, browser)(ReadPageInput(url=URL))

    (page,) = output.items
    assert E2E_JS_PAGE_TEXT in page.markdown
    assert page.title == E2E_JS_PAGE_TITLE
    assert page.truncated is False
    assert bb.open_sessions == [] and len(bb.created) == 1
    # One page that came back with text is one billed page read.
    assert output.billable_units == 1
    assert output.captcha_attempts == 0


async def test_a_long_page_is_cut_and_says_so():
    bb = FakeBrowserbase()
    long_html = E2E_JS_PAGE_HTML.replace(
        "</article>", "<p>" + ("word " * 2000) + "</p></article>"
    )
    browser = FakeBrowser(pages={URL: (E2E_JS_PAGE_TITLE, long_html)})

    output = await _executor(bb, browser)(ReadPageInput(url=URL, max_chars=1_000))

    assert len(output.items[0].markdown) == 1_000
    assert output.items[0].truncated is True


async def test_a_private_address_is_refused_before_any_session_or_key_read():
    bb = FakeBrowserbase()
    lookups: list[bool] = []

    with pytest.raises(CapabilityUnavailableError) as refused:
        await _executor(bb, FakeBrowser(), lookups=lookups)(
            ReadPageInput(url="http://10.0.0.5/admin")
        )

    assert refused.value.message == NOT_PUBLIC
    assert lookups == [] and bb.created == []


async def test_a_redirect_to_a_private_address_is_refused():
    bb = FakeBrowserbase()
    browser = FakeBrowser(
        pages={URL: (E2E_JS_PAGE_TITLE, E2E_JS_PAGE_HTML)},
        redirect_to="http://192.168.1.1/",
    )

    with pytest.raises(CapabilityUnavailableError) as refused:
        await _executor(bb, browser)(ReadPageInput(url=URL))

    assert refused.value.message == NOT_PUBLIC
    assert bb.open_sessions == []


async def test_without_a_key_nothing_is_opened():
    bb = FakeBrowserbase()

    with pytest.raises(CapabilityUnavailableError) as refused:
        await _executor(bb, FakeBrowser(), key=None)(ReadPageInput(url=URL))

    assert refused.value.message == NOT_CONNECTED
    assert bb.created == []


async def test_a_refused_key_is_reported_in_browserbases_words():
    bb = FakeBrowserbase(reject_key=True)

    with pytest.raises(CapabilityUnavailableError, match="refused this workspace"):
        await _executor(bb, FakeBrowser())(ReadPageInput(url=URL))


async def test_a_page_that_does_not_load_is_reported():
    bb = FakeBrowserbase()

    with pytest.raises(CapabilityUnavailableError, match="didn't load"):
        await _executor(bb, FakeBrowser(pages={}))(ReadPageInput(url=URL))
    assert bb.open_sessions == []


def _failing_loader(error: Exception):
    async def load(connect_url: str, url: str):
        raise error

    return load


def _playwright_errors():
    from playwright.async_api import Error, TimeoutError as PlaywrightTimeout

    return [
        # The CDP attach to the Browserbase session timing out.
        PlaywrightTimeout("BrowserType.connect_over_cdp: Timeout 15000ms exceeded."),
        # A page whose script navigates away while it is being read.
        Error("Page.content: Execution context was destroyed"),
        RuntimeError("anything else"),
    ]


@pytest.mark.parametrize("error", _playwright_errors(), ids=["cdp", "context", "other"])
async def test_a_browser_level_failure_is_a_sentence_not_a_failed_turn(error):
    bb = FakeBrowserbase()

    with pytest.raises(CapabilityUnavailableError) as refused:
        await _executor(bb, _failing_loader(error))(ReadPageInput(url=URL))

    assert refused.value.message == COULD_NOT_READ
    assert len(bb.created) == 1 and bb.open_sessions == []


def test_read_page_is_charged_as_one_web_page_read():
    capability = read_page_capability(7)

    assert capability.billing_unit is BillingUnit.WEB_CRAWL
    assert capability.name.replace(".", "_") == "browserbase_read_page"
    assert ReadPageInput(url=URL).estimated_units == 1
    with pytest.raises(ValueError):
        ReadPageInput(url="ftp://example.com/file")
