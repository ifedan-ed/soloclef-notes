"""An imported paper is read from the best free full text, and says which it got."""

from __future__ import annotations

import pytest

from app.notebooks.research.academic.clients import AcademicClients
from app.notebooks.research.academic.ingest import PaperReader
from app.notebooks.research.ingest.fetched import FetchedContent

from .academic_fixtures import ReplayFetch, fixture

pytestmark = pytest.mark.unit

PDF_TEXT = "PDF BODY: flow rates above 2 L/kg/min were studied."


class Files:
    def __init__(self, *, works: bool = True):
        self.works = works
        self.calls = []

    async def __call__(self, session, url, user_id, *, name=None):
        self.calls.append((url, name))
        return FetchedContent(markdown=PDF_TEXT) if self.works else None


class Pages:
    def __init__(self):
        self.calls = []

    async def __call__(self, runner, url):
        self.calls.append(url)
        return FetchedContent(markdown="Landing page text.")


def _reader(fetch=None, files=None, pages=None):
    return PaperReader(
        None,
        None,
        "user",
        clients=AcademicClients(fetch_text=fetch or ReplayFetch()),
        file_fetcher=files or Files(),
        page_fetcher=pages or Pages(),
    )


def _metadata(source, url, **paper):
    return {"url": url, "research_source": source, "paper": paper}


async def test_pubmed_open_access_paper_imports_the_pmc_full_text():
    fetch = ReplayFetch(
        (r"efetch\.fcgi\?db=pubmed", fixture("pubmed_efetch.xml")),
        (r"efetch\.fcgi\?db=pmc", fixture("pmc_PMC9203852.xml")),
    )
    content = await _reader(fetch).read(
        _metadata(
            "pubmed", "https://pubmed.ncbi.nlm.nih.gov/35721052/", pmid="35721052"
        ),
        "HFNC review",
    )
    assert content.metadata == {"full_text_source": "pmc_oa_xml"}
    assert "Full text: PubMed Central open-access full text (XML)" in content.markdown
    assert "PMID: 35721052" in content.markdown
    assert "## Introduction" in content.markdown
    assert "## Abstract" in content.markdown
    assert not any("elink" in url for url in fetch.urls)


async def test_pubmed_paper_outside_the_open_subset_imports_its_abstract():
    fetch = ReplayFetch(
        (r"efetch\.fcgi\?db=pubmed", fixture("pubmed_efetch.xml")),
        (r"efetch\.fcgi\?db=pmc", fixture("pmc_PMC10953464.xml")),
    )
    content = await _reader(fetch).read(
        _metadata(
            "pubmed", "https://pubmed.ncbi.nlm.nih.gov/38506440/", pmid="38506440"
        ),
        "Cochrane review",
    )
    assert content.metadata == {"full_text_source": "abstract_only"}
    assert "abstract only (no free full text found)" in content.markdown


async def test_pubmed_finds_a_missing_pmc_id_through_elink():
    fetch = ReplayFetch(
        (r"efetch\.fcgi\?db=pubmed", "<PubmedArticleSet/>"),
        (r"elink\.fcgi", fixture("pubmed_elink.json")),
        (r"efetch\.fcgi\?db=pmc", fixture("pmc_PMC9203852.xml")),
    )
    content = await _reader(fetch).read(
        _metadata(
            "pubmed", "https://pubmed.ncbi.nlm.nih.gov/35721052/", pmid="35721052"
        ),
        "HFNC review",
    )
    assert any("elink" in url for url in fetch.urls)
    assert content.metadata["full_text_source"] == "pmc_oa_xml"


async def test_arxiv_imports_the_pdf_through_the_parser():
    files = Files()
    content = await _reader(files=files).read(
        _metadata(
            "arxiv",
            "https://arxiv.org/abs/2111.11846",
            arxiv_id="2111.11846",
            pdf_url="https://arxiv.org/pdf/2111.11846v1",
        ),
        "HFNC failure",
    )
    assert files.calls == [("https://arxiv.org/pdf/2111.11846v1", "2111.11846.pdf")]
    assert content.metadata == {"full_text_source": "pdf"}
    assert PDF_TEXT in content.markdown
    assert "arXiv: 2111.11846" in content.markdown


async def test_arxiv_falls_back_to_the_abstract_when_the_pdf_fails():
    fetch = ReplayFetch((r"id_list=2111\.11846", fixture("arxiv_query.xml")))
    content = await _reader(fetch, files=Files(works=False)).read(
        _metadata("arxiv", "https://arxiv.org/abs/2111.11846", arxiv_id="2111.11846"),
        "HFNC failure",
    )
    assert content.metadata == {"full_text_source": "abstract_only"}
    assert "High Flow Nasal Cannula (HFNC) provides" in content.markdown


async def test_preprint_prefers_the_pdf():
    fetch = ReplayFetch((r"api\.biorxiv\.org/details", fixture("biorxiv_details.json")))
    files = Files()
    content = await _reader(fetch, files=files).read(
        _metadata(
            "medrxiv",
            "https://www.medrxiv.org/content/10.1101/2020.12.10.20245662",
            doi="10.1101/2020.12.10.20245662",
            server="medrxiv",
        ),
        "Aerosol generation",
    )
    assert files.calls[0][0].endswith("2020.12.10.20245662.full.pdf")
    assert content.metadata == {"full_text_source": "pdf"}


async def test_preprint_reads_the_jats_when_the_pdf_is_refused():
    fetch = ReplayFetch(
        (r"api\.biorxiv\.org/details", fixture("biorxiv_details.json")),
        (r"\.source\.xml$", fixture("medrxiv_jats.xml")),
    )
    content = await _reader(fetch, files=Files(works=False)).read(
        _metadata(
            "medrxiv",
            "https://www.medrxiv.org/content/10.1101/2020.12.10.20245662",
            doi="10.1101/2020.12.10.20245662",
            server="medrxiv",
        ),
        "Aerosol generation",
    )
    assert content.metadata == {"full_text_source": "preprint_jats_xml"}
    assert content.title.startswith("Aerosol generation in children")
    assert len(content.markdown) > 5000


async def test_scholar_imports_the_linked_pdf_else_the_landing_page():
    files, pages = Files(), Pages()
    with_pdf = await _reader(files=files, pages=pages).read(
        _metadata(
            "google_scholar",
            "https://link.springer.com/article/10.1007/s00134-013-2879-y",
            pdf_url="https://hal.umontpellier.fr/hal-02544936/document",
        ),
        "Is treatment with HFNC effective",
    )
    assert with_pdf.metadata == {"full_text_source": "pdf"}
    assert pages.calls == []

    no_pdf = await _reader(files=files, pages=pages).read(
        _metadata("google_scholar", "https://publisher.example/paper"),
        "Some paper",
    )
    assert no_pdf.metadata == {"full_text_source": "landing_page"}
    assert pages.calls == ["https://publisher.example/paper"]
    assert "Landing page text." in no_pdf.markdown
