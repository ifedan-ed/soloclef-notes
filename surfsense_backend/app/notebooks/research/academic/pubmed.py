"""PubMed through NCBI E-utilities: esearch for ids, efetch for the records.

esearch ranks by relevance; efetch returns full PubMed XML, from which the
title, authors, journal, year, DOI, PMC id and abstract are read. The record's
own ids live in ``PubmedData/ArticleIdList``; the reference list repeats that
element for every cited paper, so only the direct child is trusted.
"""

from __future__ import annotations

import json
import re
from dataclasses import dataclass, field
from xml.etree.ElementTree import Element

from ..schemas import ResultRow
from ..snippets import trim
from .ncbi import eutils_url
from .paper import PaperRef
from .polite_http import FetchText
from .rows import paper_row
from .xml_text import flat_text, parse_xml

_YEAR = re.compile(r"(1[89]|20)\d\d")


@dataclass(frozen=True)
class PubmedRecord:
    pmid: str
    title: str
    authors: list[str] = field(default_factory=list)
    journal: str | None = None
    year: int | None = None
    doi: str | None = None
    pmcid: str | None = None
    abstract: str = ""


def pubmed_url(pmid: str) -> str:
    return f"https://pubmed.ncbi.nlm.nih.gov/{pmid}/"


def esearch_url(query: str, limit: int) -> str:
    return eutils_url(
        "esearch",
        {
            "db": "pubmed",
            "term": query,
            "retmax": limit,
            "sort": "relevance",
            "retmode": "json",
        },
    )


def efetch_url(pmids: list[str]) -> str:
    return eutils_url(
        "efetch", {"db": "pubmed", "id": ",".join(pmids), "retmode": "xml"}
    )


def parse_esearch(text: str) -> list[str]:
    result = json.loads(text).get("esearchresult") or {}
    return [pmid for pmid in result.get("idlist") or [] if pmid.isdigit()]


def _authors(article: Element) -> list[str]:
    names = []
    for author in article.findall("./AuthorList/Author"):
        collective = author.findtext("CollectiveName")
        if collective:
            names.append(collective.strip())
            continue
        last = (author.findtext("LastName") or "").strip()
        initials = (author.findtext("Initials") or "").strip()
        if last:
            names.append(f"{last} {initials}".strip())
    return names


def _year(article: Element) -> int | None:
    issue_date = article.find("./Journal/JournalIssue/PubDate")
    candidates = [
        issue_date.findtext("Year") if issue_date is not None else None,
        issue_date.findtext("MedlineDate") if issue_date is not None else None,
        article.findtext("./ArticleDate/Year"),
    ]
    for value in candidates:
        match = _YEAR.search(value or "")
        if match:
            return int(match.group(0))
    return None


def _abstract(article: Element) -> str:
    parts = []
    for section in article.findall("./Abstract/AbstractText"):
        text = flat_text(section)
        if not text:
            continue
        label = section.get("Label")
        parts.append(f"{label.title()}: {text}" if label else text)
    return "\n\n".join(parts)


def _own_ids(record: Element) -> dict[str, str]:
    ids = {}
    for node in record.findall("./PubmedData/ArticleIdList/ArticleId"):
        kind = (node.get("IdType") or "").lower()
        if kind in ("doi", "pmc") and node.text:
            ids.setdefault(kind, node.text.strip())
    return ids


def parse_efetch(text: str) -> list[PubmedRecord]:
    root = parse_xml(text)
    records = []
    for node in root.findall("./PubmedArticle"):
        citation = node.find("./MedlineCitation")
        article = citation.find("./Article") if citation is not None else None
        pmid = citation.findtext("./PMID") if citation is not None else None
        if article is None or not pmid:
            continue
        ids = _own_ids(node)
        doi = ids.get("doi")
        if not doi:
            for location in article.findall("./ELocationID"):
                if location.get("EIdType") == "doi" and location.text:
                    doi = location.text.strip()
                    break
        journal = article.find("./Journal")
        records.append(
            PubmedRecord(
                pmid=pmid.strip(),
                title=flat_text(article.find("./ArticleTitle")).rstrip("."),
                authors=_authors(article),
                journal=(
                    journal.findtext("Title") or journal.findtext("ISOAbbreviation")
                )
                if journal is not None
                else None,
                year=_year(article),
                doi=doi,
                pmcid=ids.get("pmc"),
                abstract=_abstract(article),
            )
        )
    return records


def record_paper(record: PubmedRecord) -> PaperRef:
    badges = ["pubmed", "pmc"] if record.pmcid else ["pubmed"]
    return PaperRef.model_validate(
        {
            "doi": _valid_or_none(record.doi),
            "pmid": record.pmid,
            "pmcid": record.pmcid,
            "authors": record.authors,
            "venue": record.journal,
            "year": record.year,
            "badges": badges,
            # Everything in PMC is free to read; whether it is in the
            # open-access subset (full-text XML) is settled at import.
            "open_access": bool(record.pmcid),
        }
    )


def _valid_or_none(doi: str | None) -> str | None:
    try:
        return PaperRef(doi=doi).doi if doi else None
    except ValueError:
        return None


def record_row(record: PubmedRecord) -> ResultRow | None:
    return paper_row(
        "pubmed",
        url=pubmed_url(record.pmid),
        title=record.title,
        snippet=trim(record.abstract) if record.abstract else "",
        paper=record_paper(record),
    )


async def search_pubmed(query: str, limit: int, fetch: FetchText) -> list[PubmedRecord]:
    pmids = parse_esearch(await fetch(esearch_url(query, limit)))
    if not pmids:
        return []
    records = {r.pmid: r for r in parse_efetch(await fetch(efetch_url(pmids)))}
    # efetch does not keep esearch's relevance order.
    return [records[pmid] for pmid in pmids if pmid in records]
