"""The real parser on documents built for the test."""

from parse_service.convert import to_pages
from tests.documents import scanned_pdf, text_pdf


def test_a_text_pdf_comes_back_one_entry_per_page(tmp_path):
    result = to_pages(str(text_pdf(tmp_path / "book.pdf", pages=3)))

    pages = result["pages"]
    assert [page["number"] for page in pages] == [1, 2, 3]
    assert "Chapter 2" in pages[1]["markdown"]
    assert "The body of page 3 is here." in pages[2]["markdown"]
    # The layout model reads the large line as a heading, which is the reason
    # this parser is kept at all.
    assert pages[0]["markdown"].lstrip().startswith("#")


def test_a_scan_is_refused_as_having_no_text_layer(tmp_path):
    result = to_pages(str(scanned_pdf(tmp_path / "scan.pdf")))

    assert result["refusal"]["kind"] == "no_text_layer"


def test_a_file_the_parser_cannot_open_is_a_parse_error(tmp_path):
    damaged = tmp_path / "damaged.pdf"
    damaged.write_bytes(b"this is not a PDF")

    result = to_pages(str(damaged))

    assert result["refusal"]["kind"] == "parse_error"
    assert "FileDataError" in result["refusal"]["message"]
