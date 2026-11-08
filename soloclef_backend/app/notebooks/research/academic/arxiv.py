"""arXiv through its official Atom API (export.arxiv.org/api/query).

Every word of the query must appear (``all:`` terms joined by AND), ranked by
relevance; when no paper has them all (common for clinical queries, which
arXiv covers thinly), the words are asked for again joined by OR, still ranked
by relevance, so the papers sharing the rarest words come first. arXiv asks for
one request every three seconds; the polite fetcher spaces them. Every arXiv
paper is open access, with a PDF at arxiv.org/pdf.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field

from ..schemas import ResultRow
from ..snippets import trim
from .paper import PaperRef
from .polite_http import FetchText, build_url
from .rows import paper_row
from .xml_text import flat_text, parse_xml

API = "https://export.arxiv.org/api/query"
_ATOM = "{http://www.w3.org/2005/Atom}"
_ARXIV = "{http://arxiv.org/schemas/atom}"
_WORD = re.compile(r"[\w\-]+", re.UNICODE)
_ID = re.compile(r"arxiv\.org/abs/(.+?)(v\d+)?$")
MAX_TERMS = 8


@dataclass(frozen=True)
class ArxivEntry:
    arxiv_id: str
    version: str | None
    title: str
    summary: str
    authors: list[str] = field(default_factory=list)
    published: str | None = None
    categories: list[str] = field(default_factory=list)
    doi: str | None = None
    journal_ref: str | None = None


def _words(query: str) -> list[str]:
    return [w for w in _WORD.findall(query) if len(w) > 1][:MAX_TERMS]


def search_query(query: str, joiner: str = "AND") -> str:
    terms = [f"all:{w}" for w in _words(query)]
    return f" {joiner} ".join(terms) or f"all:{query.strip()}"


def query_url(query: str, limit: int, joiner: str = "AND") -> str:
    return build_url(
        API,
        {
            "search_query": search_query(query, joiner),
            "start": 0,
            "max_results": limit,
            "sortBy": "relevance",
            "sortOrder": "descending",
        },
    )


def id_url(arxiv_id: str) -> str:
    return build_url(API, {"id_list": arxiv_id, "max_results": 1})


def abs_url(arxiv_id: str) -> str:
    return f"https://arxiv.org/abs/{arxiv_id}"


def pdf_url(arxiv_id: str, version: str | None = None) -> str:
    return f"https://arxiv.org/pdf/{arxiv_id}{version or ''}"


def parse_feed(text: str) -> list[ArxivEntry]:
    root = parse_xml(text)
    entries = []
    for entry in root.findall(f"{_ATOM}entry"):
        match = _ID.search((entry.findtext(f"{_ATOM}id") or "").strip())
        if not match:
            continue
        entries.append(
            ArxivEntry(
                arxiv_id=match.group(1),
                version=match.group(2),
                title=flat_text(entry.find(f"{_ATOM}title")),
                summary=flat_text(entry.find(f"{_ATOM}summary")),
                authors=[
                    flat_text(author.find(f"{_ATOM}name"))
                    for author in entry.findall(f"{_ATOM}author")
                ],
                published=(entry.findtext(f"{_ATOM}published") or "")[:10] or None,
                categories=[
                    c.get("term", "") for c in entry.findall(f"{_ATOM}category")
                ],
                doi=(entry.findtext(f"{_ARXIV}doi") or "").strip() or None,
                journal_ref=flat_text(entry.find(f"{_ARXIV}journal_ref")) or None,
            )
        )
    return entries


def entry_paper(entry: ArxivEntry) -> PaperRef:
    try:
        doi = PaperRef(doi=entry.doi).doi if entry.doi else None
    except ValueError:
        doi = None
    return PaperRef.model_validate(
        {
            "arxiv_id": entry.arxiv_id,
            "version": entry.version,
            "doi": doi,
            "pdf_url": pdf_url(entry.arxiv_id, entry.version),
            "authors": entry.authors,
            "venue": entry.journal_ref or "arXiv",
            "year": int(entry.published[:4]) if entry.published else None,
            "published": entry.published,
            "categories": [c for c in entry.categories if c],
            "badges": ["arxiv"],
            "open_access": True,
        }
    )


def entry_row(entry: ArxivEntry) -> ResultRow | None:
    paper = entry_paper(entry)
    row = paper_row(
        "arxiv",
        url=abs_url(entry.arxiv_id),
        title=entry.title,
        snippet=trim(entry.summary),
        paper=paper,
    )
    return row


async def search_arxiv(query: str, limit: int, fetch: FetchText) -> list[ArxivEntry]:
    entries = parse_feed(await fetch(query_url(query, limit)))
    if entries or len(_words(query)) < 2:
        return entries
    return parse_feed(await fetch(query_url(query, limit, "OR")))


async def fetch_entry(arxiv_id: str, fetch: FetchText) -> ArxivEntry | None:
    entries = parse_feed(await fetch(id_url(arxiv_id)))
    return entries[0] if entries else None
