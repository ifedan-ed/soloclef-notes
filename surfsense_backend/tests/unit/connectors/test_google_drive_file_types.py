"""Tests for Google Drive file type filtering."""

import pytest

from app.connectors.google_drive.file_types import should_skip_by_extension

pytestmark = pytest.mark.unit


def _deploy(mocker, service):
    """Connector syncs choose no parser: files go to Local, or to Cloud if only
    it can read them. ``PYMUPDF4LLM`` stands for a deployment with no usable
    Cloud; ``LLAMACLOUD``/``UNSTRUCTURED`` name the service behind Cloud."""
    cloud_available = service != "PYMUPDF4LLM"
    mocker.patch("app.config.config.ETL_DEFAULT_PARSER", "LOCAL")
    mocker.patch(
        "app.config.config.ETL_CLOUD_SERVICE",
        service if cloud_available else "LLAMACLOUD",
    )
    mocker.patch(
        "app.config.config.LLAMA_CLOUD_API_KEY", "key" if cloud_available else None
    )
    mocker.patch("app.config.config.AZURE_DI_ENDPOINT", None)
    mocker.patch("app.config.config.AZURE_DI_KEY", None)


@pytest.mark.parametrize(
    "filename",
    [
        "malware.exe",
        "archive.zip",
        "video.mov",
        "font.woff2",
        "model.blend",
    ],
)
def test_unsupported_extensions_are_skipped_regardless_of_service(filename, mocker):
    """Truly unsupported files are skipped no matter which ETL service is configured."""
    for service in ("PYMUPDF4LLM", "LLAMACLOUD", "UNSTRUCTURED"):
        _deploy(mocker, service)
        skip, _ext = should_skip_by_extension(filename)
        assert skip is True


@pytest.mark.parametrize(
    "filename",
    [
        "report.pdf",
        "doc.docx",
        "slides.pptx",
        "readme.txt",
        "data.csv",
        "photo.png",
        "notes.md",
    ],
)
def test_universal_extensions_are_not_skipped(filename, mocker):
    """Files supported by all parsers (or handled by plaintext/direct_convert) are never skipped."""
    for service in ("PYMUPDF4LLM", "LLAMACLOUD", "UNSTRUCTURED"):
        _deploy(mocker, service)
        skip, ext = should_skip_by_extension(filename)
        assert skip is False, f"{filename} should NOT be skipped with {service}"
        assert ext is None


@pytest.mark.parametrize(
    "filename,service,expected_skip",
    [
        ("macro.docm", "PYMUPDF4LLM", True),
        ("macro.docm", "LLAMACLOUD", False),
        ("mail.eml", "PYMUPDF4LLM", True),
        ("mail.eml", "UNSTRUCTURED", False),
        # MuPDF reads GIF, so pymupdf4llm takes it where Docling would not.
        ("photo.gif", "PYMUPDF4LLM", False),
        ("photo.gif", "LLAMACLOUD", False),
        ("photo.heic", "UNSTRUCTURED", False),
        ("photo.heic", "PYMUPDF4LLM", True),
        # MuPDF opens .xlsx but renders it with every string cell missing,
        # so the spreadsheet is skipped rather than indexed as gibberish.
        ("sheet.xlsx", "PYMUPDF4LLM", True),
        ("sheet.xlsx", "LLAMACLOUD", False),
        ("sheet.xlsx", "UNSTRUCTURED", False),
    ],
)
def test_parser_specific_extensions(filename, service, expected_skip, mocker):
    _deploy(mocker, service)
    skip, ext = should_skip_by_extension(filename)
    assert skip is expected_skip, (
        f"{filename} with {service}: expected skip={expected_skip}"
    )
    if expected_skip:
        assert ext is not None, "unsupported extension should be returned"
    else:
        assert ext is None


def test_returns_unsupported_extension(mocker):
    """When a file is skipped, the unsupported extension string is returned."""
    _deploy(mocker, "PYMUPDF4LLM")
    skip, ext = should_skip_by_extension("macro.docm")
    assert skip is True
    assert ext == ".docm"
