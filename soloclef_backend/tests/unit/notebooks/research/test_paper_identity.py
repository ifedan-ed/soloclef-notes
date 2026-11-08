"""Papers are known by DOI, PMID, PMC and arXiv ids; the same paper folds into one row."""

from __future__ import annotations

import pytest
from pydantic import ValidationError

from app.notebooks.research.academic.identifiers import dedupe_papers
from app.notebooks.research.academic.paper import PaperRef
from app.notebooks.research.schemas import ImportItem, ResultRow

pytestmark = pytest.mark.unit


def _row(url, source, **paper):
    return ResultRow(
        id=url, url=url, title="t", snippet="", source=source, paper=PaperRef(**paper)
    )


def test_ids_are_normalised():
    paper = PaperRef(
        doi="https://doi.org/10.1000/ABC.1",
        pmcid="123",
        arxiv_id="2111.11846v3",
    )
    assert paper.doi == "10.1000/abc.1"
    assert paper.pmcid == "PMC123"
    assert paper.arxiv_id == "2111.11846"


@pytest.mark.parametrize(
    "field,value",
    [
        ("doi", "javascript:alert(1)"),
        ("pmid", "12a"),
        ("pmcid", "PMC12;drop"),
        ("arxiv_id", "../../etc/passwd"),
    ],
)
def test_malformed_ids_are_refused(field, value):
    with pytest.raises(ValidationError):
        PaperRef(**{field: value})


def test_a_non_http_pdf_link_is_dropped():
    assert PaperRef(pdf_url="file:///etc/passwd").pdf_url is None


def test_an_import_item_carries_its_paper():
    item = ImportItem.model_validate(
        {
            "url": "https://pubmed.ncbi.nlm.nih.gov/1/",
            "title": "T",
            "source": "pubmed",
            "paper": {"pmid": "1", "badges": ["pubmed"]},
        }
    )
    assert item.paper.pmid == "1"


def test_the_same_paper_from_two_places_folds_into_the_first():
    rows = dedupe_papers(
        [
            _row(
                "https://a.example/1",
                "google_scholar",
                doi="10.1000/x",
                badges=["scholar"],
            ),
            _row(
                "https://pubmed.ncbi.nlm.nih.gov/9/",
                "google_scholar",
                doi="10.1000/X",
                pmid="9",
                badges=["pubmed"],
                open_access=True,
            ),
            _row("https://b.example/2", "google_scholar", arxiv_id="2101.00001"),
        ]
    )
    assert [r.url for r in rows] == ["https://a.example/1", "https://b.example/2"]
    merged = rows[0].paper
    assert merged.badges == ["scholar", "pubmed"]
    assert merged.pmid == "9"
    assert merged.open_access is True
