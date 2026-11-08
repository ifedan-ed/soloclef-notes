"""Parsers hand back each page's markdown alongside the document markdown."""

from types import SimpleNamespace

import pytest

from app.etl_pipeline.etl_document import DocumentParser, EtlRequest
from app.etl_pipeline.etl_pipeline_service import EtlPipelineService
from app.source_pages.paged_markdown import (
    PagedMarkdown,
    page_texts_of,
    pages_by_number,
)

pytestmark = pytest.mark.unit


def test_paged_markdown_is_still_the_markdown_string():
    md = PagedMarkdown("page one\npage two\n", ["page one\n", "page two\n"])

    assert md == "page one\npage two\n"
    assert isinstance(md, str)
    assert page_texts_of(md) == ["page one\n", "page two\n"]
    assert page_texts_of("plain parser output") is None


def test_pages_by_number_keeps_numbers_when_a_page_is_missing():
    assert pages_by_number([(1, "a"), (3, "c")]) == ["a", "", "c"]
    assert pages_by_number([]) == []


def test_pymupdf4llm_page_chunks_join_to_the_document_markdown():
    from app.etl_pipeline.parsers.pymupdf4llm import _join_pages

    joined = _join_pages(
        [
            {"metadata": {"page_number": 1}, "text": "# Title\n\n"},
            {"metadata": {"page_number": 2}, "text": "Body text.\n\n"},
        ]
    )

    assert joined == "# Title\n\nBody text.\n\n"
    assert page_texts_of(joined) == ["# Title\n\n", "Body text.\n\n"]


async def test_a_real_multi_page_pdf_parses_with_its_pages(tmp_path):
    pymupdf = pytest.importorskip("pymupdf")
    pytest.importorskip("pymupdf4llm")

    pdf_path = tmp_path / "three.pdf"
    document = pymupdf.open()
    for sentence in (
        "Bronchiolitis peaks in the first winter of life.",
        "Supportive care is the mainstay of treatment.",
        "Apnoea is a feature in the youngest infants.",
    ):
        document.new_page().insert_text((72, 72), sentence)
    document.save(pdf_path)
    document.close()

    result = await EtlPipelineService().extract(
        EtlRequest(
            file_path=str(pdf_path), filename="three.pdf", parser=DocumentParser.LOCAL
        )
    )

    assert result.page_texts is not None
    assert len(result.page_texts) == 3
    assert "Supportive care" in result.page_texts[1]
    assert "".join(result.page_texts) == result.markdown_content
    assert type(result.markdown_content) is str


async def test_the_pipeline_passes_parser_pages_through(tmp_path, mocker):
    pdf_file = tmp_path / "doc.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake")
    mocker.patch(
        "app.etl_pipeline.parsers.pymupdf4llm.parse_with_pymupdf4llm",
        new=mocker.AsyncMock(return_value=PagedMarkdown("ab", ["a", "b"])),
    )

    result = await EtlPipelineService().extract(
        EtlRequest(
            file_path=str(pdf_file), filename="doc.pdf", parser=DocumentParser.LOCAL
        )
    )

    assert result.markdown_content == "ab"
    assert result.page_texts == ["a", "b"]


async def test_a_parser_without_pages_leaves_page_texts_empty(tmp_path, mocker):
    pdf_file = tmp_path / "doc.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake")
    mocker.patch(
        "app.etl_pipeline.parsers.pymupdf4llm.parse_with_pymupdf4llm",
        new=mocker.AsyncMock(return_value="# plain"),
    )

    result = await EtlPipelineService().extract(
        EtlRequest(
            file_path=str(pdf_file), filename="doc.pdf", parser=DocumentParser.LOCAL
        )
    )

    assert result.page_texts is None


def test_llamacloud_pages_come_from_the_parse_result():
    from app.etl_pipeline.parsers.llamacloud import _extract_content

    result = SimpleNamespace(
        markdown=SimpleNamespace(
            pages=[
                SimpleNamespace(page_number=1, success=True, markdown="# One"),
                SimpleNamespace(page_number=2, success=True, markdown="# Two"),
            ]
        ),
    )

    content = _extract_content(result)

    assert content == "# One\n\n# Two"
    assert page_texts_of(content) == ["# One", "# Two"]


def test_azure_pages_are_sliced_from_the_content_by_their_spans():
    from app.etl_pipeline.parsers.azure_doc_intelligence import _page_markdowns

    content = "# Page one text\n<!-- PageBreak -->\nPage two text"
    result = SimpleNamespace(
        pages=[
            SimpleNamespace(
                page_number=1, spans=[SimpleNamespace(offset=0, length=15)]
            ),
            SimpleNamespace(
                page_number=2, spans=[SimpleNamespace(offset=35, length=13)]
            ),
        ]
    )

    assert _page_markdowns(result, content) == ["# Page one text", "Page two text"]
