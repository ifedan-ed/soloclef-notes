"""The Drive Picker page: nothing in it can close its script, and it is never cached or framed."""

from __future__ import annotations

import json
import re

import pytest

from app.app_handoff.picker_page import (
    MAX_PICKED_FILES,
    PickerPage,
    new_nonce,
    render_message_page,
    render_picker_page,
    script_json,
    security_headers,
)

pytestmark = pytest.mark.unit

PAGE = PickerPage(
    access_token="ya29.token</script><script>alert(1)</script>",
    api_key="key&<>",
    app_id="123456789012",
    ticket="t" * 43,
    import_path="/api/v1/app/handoff/drive-picker/import",
    done_url="soloclef://drive/done",
)


def config_of(page: str) -> dict:
    match = re.search(
        r'<script id="handoff-config" type="application/json">(.*?)</script>',
        page,
        re.S,
    )
    assert match
    return json.loads(match.group(1))


def test_the_page_carries_the_pickers_settings_as_json_its_script_reads():
    nonce = new_nonce()
    page = render_picker_page(PAGE, nonce)
    config = config_of(page)
    assert config["accessToken"] == PAGE.access_token
    assert config["apiKey"] == PAGE.api_key
    assert config["appId"] == PAGE.app_id
    assert config["ticket"] == PAGE.ticket
    assert config["importPath"] == PAGE.import_path
    assert config["doneUrl"] == "soloclef://drive/done"
    assert config["maxItems"] == MAX_PICKED_FILES == 50
    assert config["pickerScript"] == "https://apis.google.com/js/api.js"


def test_no_value_can_close_the_script_element():
    page = render_picker_page(PAGE, new_nonce())
    # The only closing tags are the page's own two scripts.
    assert page.count("</script>") == 2
    assert "<script>alert(1)" not in page
    assert script_json("</script>&") == '"\\u003c/script\\u003e\\u0026"'


def test_only_scripts_with_the_pages_nonce_run():
    nonce = new_nonce()
    page = render_picker_page(PAGE, nonce)
    executable = re.findall(r"<script(?![^>]*application/json)([^>]*)>", page)
    assert executable == [f' nonce="{nonce}"']
    csp = security_headers(nonce)["Content-Security-Policy"]
    assert f"'nonce-{nonce}'" in csp and "'strict-dynamic'" in csp
    assert (
        "frame-ancestors 'none'" in csp
        and "object-src 'none'" in csp
        and "base-uri 'none'" in csp
    )


def test_the_page_is_never_cached_or_framed_and_tells_others_only_its_origin():
    headers = security_headers(new_nonce())
    assert headers["Cache-Control"] == "no-store"
    assert headers["X-Frame-Options"] == "DENY"
    assert headers["Referrer-Policy"] == "strict-origin"
    assert '<meta name="referrer" content="strict-origin">' in render_picker_page(
        PAGE, new_nonce()
    )


def test_back_to_the_app_is_always_offered_and_cancels():
    page = render_picker_page(PAGE, new_nonce())
    assert 'href="soloclef://drive/done?error=cancelled"' in page


def test_a_message_page_escapes_its_words():
    page = render_message_page(
        "Run <out>", "Try & again", "soloclef://drive/done?error=expired", new_nonce()
    )
    assert "Run &lt;out&gt;" in page and "Try &amp; again" in page
    assert "<script" not in page
    assert (
        render_message_page("Gone", "Close this page.", None, new_nonce()).count(
            "Back to the app"
        )
        == 0
    )
