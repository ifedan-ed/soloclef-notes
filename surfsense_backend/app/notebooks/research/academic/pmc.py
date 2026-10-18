"""PubMed Central: find a paper's PMC record and read its open-access XML.

efetch ``db=pmc`` returns the full JATS article for the open-access subset and
only the front matter (with a comment saying so) for everything else, so the
presence of body text is what decides whether the full text can be imported.
"""

from __future__ import annotations

import json
from xml.etree.ElementTree import Element

from .jats import article_element, has_body_text
from .ncbi import eutils_url
from .polite_http import FetchText
from .xml_text import local_name, parse_xml


def pmc_efetch_url(pmcids: list[str]) -> str:
    return eutils_url("efetch", {"db": "pmc", "id": ",".join(pmcids), "retmode": "xml"})


def elink_url(pmids: list[str]) -> str:
    return eutils_url(
        "elink",
        {
            "dbfrom": "pubmed",
            "db": "pmc",
            "linkname": "pubmed_pmc",
            "id": ",".join(pmids),
            "retmode": "json",
        },
    )


def parse_elink(text: str) -> dict[str, str]:
    """PMID -> PMC id for every PMID that has a PMC record."""
    found: dict[str, str] = {}
    for linkset in json.loads(text).get("linksets") or []:
        ids = linkset.get("ids") or []
        for db in linkset.get("linksetdbs") or []:
            links = db.get("links") or []
            if db.get("linkname") == "pubmed_pmc" and ids and links:
                found[str(ids[0])] = f"PMC{links[0]}"
    return found


def _pmcid_of(article: Element) -> str | None:
    for node in article.iter():
        if local_name(node.tag) == "article-id" and node.get("pub-id-type") == "pmcid":
            text = (node.text or "").strip().upper()
            return text if text.startswith("PMC") else f"PMC{text}"
    for node in article.iter():
        if local_name(node.tag) == "article-id" and node.get("pub-id-type") in (
            "pmc",
            "pmcaid",
        ):
            return f"PMC{(node.text or '').strip()}"
    return None


def open_articles(text: str) -> dict[str, Element]:
    """PMC id -> article, for the articles whose full text came back."""
    root = parse_xml(text)
    articles = [el for el in root if local_name(el.tag) == "article"]
    if not articles:
        single = article_element(root)
        articles = [single] if single is not None else []
    found = {}
    for article in articles:
        pmcid = _pmcid_of(article)
        if pmcid and has_body_text(article):
            found[pmcid] = article
    return found


async def find_pmcid(pmid: str, fetch: FetchText) -> str | None:
    return parse_elink(await fetch(elink_url([pmid]))).get(pmid)


async def open_full_text(pmcids: list[str], fetch: FetchText) -> dict[str, Element]:
    if not pmcids:
        return {}
    return open_articles(await fetch(pmc_efetch_url(pmcids)))
