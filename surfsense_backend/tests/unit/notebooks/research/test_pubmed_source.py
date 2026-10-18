"""PubMed through E-utilities, replayed from recorded answers."""

from __future__ import annotations

import pytest

from app.config import config
from app.notebooks.research.academic.clients import AcademicClients
from app.notebooks.research.academic.ncbi import eutils_url
from app.notebooks.research.academic.pmc import open_articles, parse_elink
from app.notebooks.research.academic.pubmed import (
    parse_efetch,
    parse_esearch,
    record_row,
    search_pubmed,
)
from app.notebooks.research.academic.search import search_academic

from .academic_fixtures import ReplayFetch, fixture

pytestmark = pytest.mark.unit

PMIDS = ["38506440", "35721052", "39294604", "31999912", "30655267"]


def _pubmed_fetch() -> ReplayFetch:
    return ReplayFetch(
        (r"esearch\.fcgi", fixture("pubmed_esearch.json")),
        (r"efetch\.fcgi\?db=pubmed", fixture("pubmed_efetch.xml")),
        (r"efetch\.fcgi\?db=pmc", fixture("pmc_PMC9203852.xml")),
    )


def test_esearch_ids_keep_relevance_order():
    assert parse_esearch(fixture("pubmed_esearch.json")) == PMIDS


def test_efetch_reads_title_authors_journal_year_ids_and_abstract():
    records = {r.pmid: r for r in parse_efetch(fixture("pubmed_efetch.xml"))}
    assert set(records) == set(PMIDS)
    review = records["35721052"]
    assert review.pmcid == "PMC9203852"
    assert review.doi == "10.3389/fmed.2022.920549"
    assert review.title
    assert review.authors and all(" " in name for name in review.authors)
    assert review.journal
    assert review.year == 2022
    assert len(review.abstract) > 200


def test_cited_papers_ids_are_not_mistaken_for_the_records_own():
    # The reference list repeats ArticleIdList with PMC ids of cited papers.
    records = parse_efetch(fixture("pubmed_efetch.xml"))
    own = {r.pmcid for r in records if r.pmcid}
    assert own <= {"PMC10953464", "PMC9203852", "PMC11412039", "PMC7027347"}
    assert next(r for r in records if r.pmid == "30655267").pmcid is None


def test_a_record_becomes_a_pubmed_row_with_badges_and_byline():
    record = next(
        r for r in parse_efetch(fixture("pubmed_efetch.xml")) if r.pmid == "35721052"
    )
    row = record_row(record)
    assert row.url == "https://pubmed.ncbi.nlm.nih.gov/35721052/"
    assert row.source == "pubmed"
    assert row.paper.pmid == "35721052"
    assert row.paper.badges == ["pubmed", "pmc"]
    assert row.paper.open_access is True
    assert "2022" in row.meta
    assert len(row.snippet) <= 400


async def test_search_asks_esearch_then_efetch_and_keeps_order():
    fetch = _pubmed_fetch()
    records = await search_pubmed("bronchiolitis high flow nasal cannula", 5, fetch)
    assert [r.pmid for r in records] == PMIDS
    assert "retmax=5" in fetch.urls[0]
    assert "sort=relevance" in fetch.urls[0]
    assert "id=" + ",".join(PMIDS) in fetch.urls[1]


def test_elink_maps_pmids_to_pmc():
    links = parse_elink(fixture("pubmed_elink.json"))
    assert links["35721052"] == "PMC9203852"
    assert "30655267" not in links


def test_pmc_open_access_article_has_its_body_and_withheld_one_does_not():
    assert set(open_articles(fixture("pmc_PMC9203852.xml"))) == {"PMC9203852"}
    assert open_articles(fixture("pmc_PMC10953464.xml")) == {}


async def test_deep_pubmed_quotes_the_open_access_full_text():
    fetch = _pubmed_fetch()
    rows = await search_academic(
        "pubmed",
        "high flow nasal cannula",
        deep=True,
        clients=AcademicClients(fetch_text=fetch),
    )
    assert any("db=pmc" in url for url in fetch.urls)
    review = next(r for r in rows if r.paper.pmid == "35721052")
    abstract_row = record_row(
        next(
            r
            for r in parse_efetch(fixture("pubmed_efetch.xml"))
            if r.pmid == "35721052"
        )
    )
    assert review.snippet != abstract_row.snippet
    assert "nasal cannula" in review.snippet.lower()


async def test_fast_pubmed_reads_no_full_text():
    fetch = _pubmed_fetch()
    rows = await search_academic(
        "pubmed", "x", deep=False, clients=AcademicClients(fetch_text=fetch)
    )
    assert len(rows) == 5
    assert not any("db=pmc" in url for url in fetch.urls)
    assert "retmax=10" in fetch.urls[0]


def test_eutils_url_names_the_tool_and_only_a_configured_contact(monkeypatch):
    monkeypatch.setattr(config, "NCBI_EMAIL", "")
    monkeypatch.setattr(config, "NCBI_API_KEY", "")
    url = eutils_url("esearch", {"db": "pubmed", "term": "a b"})
    assert "tool=surfsense" in url
    assert "email=" not in url
    assert "api_key=" not in url
    monkeypatch.setattr(config, "NCBI_EMAIL", "me@example.org")
    monkeypatch.setattr(config, "NCBI_API_KEY", "k123")
    url = eutils_url("esearch", {"db": "pubmed"})
    assert "email=me%40example.org" in url
    assert "api_key=k123" in url
