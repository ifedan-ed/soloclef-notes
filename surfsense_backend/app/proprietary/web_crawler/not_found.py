# SurfSense proprietary crawler engine.
#
# Part of the ``app.proprietary`` package; licensed separately from the
# Apache-2.0 project root (see ``app/proprietary/LICENSE``).
"""Stop the fetch ladder on a page the server says does not exist.

A static fetch answered 404 Not Found or 410 Gone used to fall through to the
full-browser tiers, which spent 10-26 s each loading the same error page
(thread 117: three guessed URLs, all dead) and then returned it as content.
The page is gone; a browser will not find it. The crawl item now says
"Page not found (HTTP 404)" and nothing is billed.

One exception keeps escalating: a client-rendered site on a static host can
answer every route with 404 and an empty app shell that the browser then
fills in. A 404 body with scripts and almost no visible text is treated as
such a shell. Documented in this package's README ("The fetch ladder").
"""

from __future__ import annotations

from lxml import html as lxml_html

NOT_FOUND_STATUSES = frozenset({404, 410})

# Visible text below this, with scripts present, reads as an app shell.
SPA_SHELL_MAX_VISIBLE_CHARS = 200

_HIDDEN_XPATH = "//script | //style | //noscript | //template | //svg | //head"


class PageNotFoundError(Exception):
    """The server answered that the page does not exist."""

    def __init__(self, status: int) -> None:
        super().__init__(f"Page not found (HTTP {status})")
        self.status = status


def _visible_chars(raw_html: str) -> int:
    root = lxml_html.fromstring(raw_html)
    for hidden in root.xpath(_HIDDEN_XPATH):
        hidden.getparent().remove(hidden)
    return len(" ".join(" ".join(root.itertext()).split()))


def is_definitely_not_found(status: int | None, raw_html: str | None) -> bool:
    """True for a 404/410 whose body is a real error page, not an app shell."""
    if status not in NOT_FOUND_STATUSES:
        return False
    html = raw_html or ""
    if "<script" not in html.lower():
        return True
    try:
        return _visible_chars(html) >= SPA_SHELL_MAX_VISIBLE_CHARS
    except Exception:
        return False
