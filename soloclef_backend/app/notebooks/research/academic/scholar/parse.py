"""Read a Google Scholar results page into papers.

Each result is a ``div.gs_r.gs_or``: the title link in ``h3.gs_rt``, the
byline ("authors - venue, year - host") in ``div.gs_a``, the snippet in
``div.gs_rs``, "Cited by N" among the footer links, and a ``[PDF]`` link in the
side column when Scholar knows a free copy. Citation-only entries ([CITATION],
no link) are skipped: there is nothing to import.

``ponytail:`` these class names have been stable for years but are Scholar's
to change; the fixture test is the tripwire.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from urllib.parse import urljoin, urlsplit

from bs4 import BeautifulSoup, Tag

from ...schemas import ResultRow
from ...snippets import plain_text, trim
from ...urls import is_http_url
from ..paper import PaperRef
from ..rows import paper_row

BASE = "https://scholar.google.com/"
_YEAR = re.compile(r"\b(1[89]\d\d|20\d\d)\b")
_CITED = re.compile(r"Cited by (\d+)")
_DOI_IN_URL = re.compile(r"(10\.\d{4,9}/[^\s?#&]+)")
_PMID_URL = re.compile(r"pubmed\.ncbi\.nlm\.nih\.gov/(\d+)")
_PMC_URL = re.compile(r"(PMC\d+)", re.I)
_ARXIV_URL = re.compile(r"arxiv\.org/(?:abs|pdf)/([\w.\-/]+?)(?:v\d+)?(?:\.pdf)?$")
RESULT_MARKERS = ('class="gs_r gs_or', 'id="gs_res_ccl_mid"')
NO_RESULTS_MARKER = 'id="gs_res_ccl_mid"'


@dataclass(frozen=True)
class ScholarHit:
    title: str
    url: str
    snippet: str = ""
    authors: list[str] = field(default_factory=list)
    venue: str | None = None
    year: int | None = None
    cited_by: int | None = None
    pdf_url: str | None = None


def page_has_results(html: str) -> bool:
    """True for a real results page (even one with zero hits), False for a wall."""
    return any(marker in (html or "") for marker in RESULT_MARKERS)


def _byline(text: str) -> tuple[list[str], str | None, int | None]:
    parts = [p.strip() for p in text.replace("\xa0", " ").split(" - ")]
    authors = [
        a.strip(" …").strip() for a in parts[0].split(",") if a.strip(" …").strip()
    ]
    middle = parts[1] if len(parts) > 2 else (parts[1] if len(parts) == 2 else "")
    year_match = _YEAR.search(middle)
    year = int(year_match.group(1)) if year_match else None
    venue = _YEAR.sub("", middle).strip(" ,…") or None
    if len(parts) == 2 and venue and "." in venue and " " not in venue:
        venue = None  # "authors - host.com": the host, not a venue
    return authors, venue, year


def _pdf_link(result: Tag) -> str | None:
    side = result.select_one("div.gs_ggs a[href]")
    if side is None:
        return None
    label = side.select_one(".gs_ctg2")
    href = side.get("href") or ""
    if label is not None and "PDF" in label.get_text() and is_http_url(href):
        return href
    return None


def parse_results(html: str) -> list[ScholarHit]:
    soup = BeautifulSoup(html or "", "lxml")
    hits = []
    for result in soup.select("div.gs_r.gs_or"):
        anchor = result.select_one("h3.gs_rt a[href]")
        if anchor is None:
            continue
        url = urljoin(BASE, anchor.get("href") or "")
        if not is_http_url(url) or urlsplit(url).hostname == "scholar.google.com":
            continue
        byline = result.select_one("div.gs_a")
        authors, venue, year = _byline(byline.get_text(" ") if byline else "")
        cited = None
        for link in result.select("div.gs_fl a"):
            match = _CITED.search(link.get_text())
            if match:
                cited = int(match.group(1))
                break
        snippet = result.select_one("div.gs_rs")
        hits.append(
            ScholarHit(
                title=plain_text(anchor.get_text(" ")),
                url=url,
                snippet=plain_text(snippet.get_text(" ")) if snippet else "",
                authors=authors,
                venue=venue,
                year=year,
                cited_by=cited,
                pdf_url=_pdf_link(result),
            )
        )
    return hits


def _ids_from_links(*urls: str | None) -> dict[str, str]:
    ids: dict[str, str] = {}
    for url in urls:
        if not url:
            continue
        if match := _PMID_URL.search(url):
            ids.setdefault("pmid", match.group(1))
        if "ncbi.nlm.nih.gov" in url and (match := _PMC_URL.search(url)):
            ids.setdefault("pmcid", match.group(1).upper())
        if match := _ARXIV_URL.search(url):
            ids.setdefault("arxiv_id", match.group(1))
        if match := _DOI_IN_URL.search(url):
            ids.setdefault("doi", match.group(1).rstrip(".").lower())
        host = urlsplit(url).hostname or ""
        for server in ("medrxiv", "biorxiv"):
            if host.endswith(f"{server}.org"):
                ids.setdefault("server", server)
    return ids


def _safe_ids(ids: dict[str, str]) -> dict[str, str]:
    """Drop any id the paper model would refuse rather than lose the whole row."""
    kept = {}
    for key, value in ids.items():
        try:
            PaperRef.model_validate({key: value})
        except ValueError:
            continue
        kept[key] = value
    return kept


def hit_paper(hit: ScholarHit) -> PaperRef:
    ids = _safe_ids(_ids_from_links(hit.url, hit.pdf_url))
    badges = ["scholar"]
    if "pmid" in ids:
        badges.append("pubmed")
    if "pmcid" in ids:
        badges.append("pmc")
    if "arxiv_id" in ids:
        badges.append("arxiv")
    if "server" in ids:
        badges.append(ids["server"])
    return PaperRef.model_validate(
        {
            **ids,
            "pdf_url": hit.pdf_url,
            "authors": hit.authors,
            "venue": hit.venue,
            "year": hit.year,
            "cited_by": hit.cited_by,
            "badges": badges,
            "open_access": bool(
                hit.pdf_url or "pmcid" in ids or "arxiv_id" in ids or "server" in ids
            ),
        }
    )


def hit_row(hit: ScholarHit) -> ResultRow | None:
    return paper_row(
        "google_scholar",
        url=hit.url,
        title=hit.title,
        snippet=trim(hit.snippet),
        paper=hit_paper(hit),
    )
