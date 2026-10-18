"""medRxiv and bioRxiv preprints.

The servers' own API (api.biorxiv.org) has no keyword search: it lists
preprints by date window or looks one up by DOI. So discovery goes through
Europe PMC's documented search API, which indexes both servers' preprints
(``SRC:PPR`` filtered to their publisher names), and each import then reads the
canonical record from api.biorxiv.org by DOI: its latest version, abstract and
the full-text links. The limitation is Europe PMC's indexing lag, usually a
few days behind a new posting.
"""

from __future__ import annotations

import json
import re
from dataclasses import dataclass, field
from typing import Any

from ..schemas import ResultRow
from ..snippets import plain_text, trim
from .paper import PaperRef, PreprintServer
from .polite_http import FetchText, build_url
from .rows import paper_row

EUROPE_PMC = "https://www.ebi.ac.uk/europepmc/webservices/rest/search"
DETAILS = "https://api.biorxiv.org/details/{server}/{doi}"
_SERVERS: dict[str, PreprintServer] = {"medrxiv": "medrxiv", "biorxiv": "biorxiv"}
_HEADINGS = re.compile(r"</?h\d[^>]*>", re.I)


@dataclass(frozen=True)
class Preprint:
    doi: str
    server: PreprintServer
    title: str
    abstract: str = ""
    authors: list[str] = field(default_factory=list)
    year: int | None = None
    published: str | None = None
    pdf_url: str | None = None
    cited_by: int | None = None


@dataclass(frozen=True)
class PreprintDetails:
    doi: str
    server: PreprintServer
    version: str | None
    title: str
    abstract: str
    authors: list[str]
    published: str | None
    category: str | None
    jats_url: str | None


def search_url(query: str, limit: int) -> str:
    clean = query.replace('"', " ").strip()
    return build_url(
        EUROPE_PMC,
        {
            "query": f'({clean}) AND SRC:PPR AND (PUBLISHER:"medRxiv" OR PUBLISHER:"bioRxiv")',
            "format": "json",
            "pageSize": limit,
            "resultType": "core",
        },
    )


def details_url(server: PreprintServer, doi: str) -> str:
    return DETAILS.format(server=server, doi=doi)


def landing_url(server: PreprintServer, doi: str) -> str:
    return f"https://www.{server}.org/content/{doi}"


def _server(record: dict[str, Any]) -> PreprintServer | None:
    publisher = (
        (record.get("bookOrReportDetails") or {}).get("publisher") or ""
    ).lower()
    return _SERVERS.get(publisher)


def _pdf(record: dict[str, Any]) -> str | None:
    for link in (record.get("fullTextUrlList") or {}).get("fullTextUrl") or []:
        url = link.get("url") or ""
        if link.get("documentStyle") == "pdf" and url.startswith("https://www."):
            return url
    return None


def _authors(record: dict[str, Any]) -> list[str]:
    listed = (record.get("authorList") or {}).get("author") or []
    names = [a.get("fullName", "").strip() for a in listed if a.get("fullName")]
    if names:
        return names
    return [
        n.strip()
        for n in (record.get("authorString") or "").rstrip(".").split(",")
        if n.strip()
    ]


def abstract_text(value: str | None) -> str:
    """Europe PMC abstracts carry ``<h4>`` section heads; keep them as words."""
    return plain_text(_HEADINGS.sub(" ", value or ""))


def parse_search(text: str) -> list[Preprint]:
    found = []
    for record in (json.loads(text).get("resultList") or {}).get("result") or []:
        server = _server(record)
        doi = (record.get("doi") or "").strip().lower()
        if not server or not doi.startswith("10.1101/"):
            continue
        year = record.get("pubYear")
        found.append(
            Preprint(
                doi=doi,
                server=server,
                title=(record.get("title") or "").strip().rstrip("."),
                abstract=abstract_text(record.get("abstractText")),
                authors=_authors(record),
                year=int(year) if str(year or "").isdigit() else None,
                published=record.get("firstPublicationDate"),
                pdf_url=_pdf(record),
                cited_by=record.get("citedByCount") or None,
            )
        )
    return found


def preprint_paper(preprint: Preprint) -> PaperRef:
    return PaperRef.model_validate(
        {
            "doi": preprint.doi,
            "server": preprint.server,
            "pdf_url": preprint.pdf_url,
            "authors": preprint.authors,
            "venue": "medRxiv" if preprint.server == "medrxiv" else "bioRxiv",
            "year": preprint.year,
            "published": preprint.published,
            "cited_by": preprint.cited_by,
            "badges": [preprint.server],
            "open_access": True,
        }
    )


def preprint_row(preprint: Preprint) -> ResultRow | None:
    return paper_row(
        "medrxiv",
        url=landing_url(preprint.server, preprint.doi),
        title=preprint.title,
        snippet=trim(preprint.abstract),
        paper=preprint_paper(preprint),
    )


def parse_details(text: str, server: PreprintServer) -> PreprintDetails | None:
    """The newest version of the preprint in a details answer."""
    versions = json.loads(text).get("collection") or []
    if not versions:
        return None
    latest = max(versions, key=lambda v: int(v.get("version") or 0))
    return PreprintDetails(
        doi=(latest.get("doi") or "").lower(),
        server=server,
        version=str(latest.get("version") or "") or None,
        title=(latest.get("title") or "").strip(),
        abstract=(latest.get("abstract") or "").strip(),
        authors=[
            a.strip() for a in (latest.get("authors") or "").split(";") if a.strip()
        ],
        published=latest.get("date"),
        category=latest.get("category"),
        jats_url=latest.get("jatsxml") or None,
    )


def pdf_from_jats_url(server: PreprintServer, jats_url: str | None) -> str | None:
    """The servers file the PDF beside the JATS source, under the server's name."""
    if not jats_url or not jats_url.endswith(".source.xml"):
        return None
    return jats_url.replace("/content/early/", f"/content/{server}/early/").replace(
        ".source.xml", ".full.pdf"
    )


async def search_preprints(query: str, limit: int, fetch: FetchText) -> list[Preprint]:
    return parse_search(await fetch(search_url(query, limit)))


async def fetch_details(
    server: PreprintServer, doi: str, fetch: FetchText
) -> PreprintDetails | None:
    return parse_details(await fetch(details_url(server, doi)), server)
