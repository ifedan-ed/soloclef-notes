"""Tests for the DOCUMENT_EXTENSIONS allowlist module."""

import pytest

pytestmark = pytest.mark.unit


def test_pdf_is_supported_document():
    from app.utils.file_extensions import is_supported_document_extension

    assert is_supported_document_extension("report.pdf") is True


def test_exe_is_not_supported_document():
    from app.utils.file_extensions import is_supported_document_extension

    assert is_supported_document_extension("malware.exe") is False


@pytest.mark.parametrize(
    "filename",
    [
        "report.pdf",
        "doc.docx",
        "old.doc",
        "sheet.xlsx",
        "legacy.xls",
        "slides.pptx",
        "deck.ppt",
        "macro.docm",
        "macro.xlsm",
        "macro.pptm",
        "photo.png",
        "photo.jpg",
        "photo.jpeg",
        "scan.bmp",
        "scan.tiff",
        "scan.tif",
        "photo.webp",
        "anim.gif",
        "manual.rtf",
        "book.epub",
        "letter.odt",
        "data.ods",
        "presentation.odp",
        "korean.hwpx",
        "korean.hwp",
        "template.dot",
        "template.dotm",
        "template.pot",
        "template.potx",
        "binary.xlsb",
        "workspace.xlw",
        "vector.svg",
    ],
)
def test_document_extensions_are_supported(filename):
    from app.utils.file_extensions import is_supported_document_extension

    assert is_supported_document_extension(filename) is True, (
        f"{filename} should be supported"
    )


@pytest.mark.parametrize(
    "filename",
    [
        "malware.exe",
        "archive.zip",
        "video.mov",
        "font.woff2",
        "model.blend",
        "random.xyz",
        "data.parquet",
        "package.deb",
        # Only the retired unstructured parser opened these. .heic still
        # reaches the vision model as an image (IMAGE_EXTENSIONS); what it
        # lost is the document parser behind that.
        "iphone.heic",
        "inbox.eml",
        "outlook.msg",
        "signature.p7s",
    ],
)
def test_non_document_extensions_are_not_supported(filename):
    from app.utils.file_extensions import is_supported_document_extension

    assert is_supported_document_extension(filename) is False, (
        f"{filename} should NOT be supported"
    )


# ---------------------------------------------------------------------------
# Per-parser extension sets
# ---------------------------------------------------------------------------


def test_union_includes_all_parser_extension_sets():
    # Datalab joined the parsers (and the union) with ETL_CLOUD_SERVICE=DATALAB.
    from app.utils.file_extensions import (
        AZURE_DI_DOCUMENT_EXTENSIONS,
        DATALAB_DOCUMENT_EXTENSIONS,
        DOCUMENT_EXTENSIONS,
        LLAMAPARSE_DOCUMENT_EXTENSIONS,
        PYMUPDF4LLM_DOCUMENT_EXTENSIONS,
    )

    expected = (
        PYMUPDF4LLM_DOCUMENT_EXTENSIONS
        | LLAMAPARSE_DOCUMENT_EXTENSIONS
        | AZURE_DI_DOCUMENT_EXTENSIONS
        | DATALAB_DOCUMENT_EXTENSIONS
    )
    assert expected == DOCUMENT_EXTENSIONS


def test_get_extensions_for_pymupdf4llm():
    from app.utils.file_extensions import get_document_extensions_for_service

    exts = get_document_extensions_for_service("PYMUPDF4LLM")
    assert ".pdf" in exts
    assert ".docx" in exts
    assert ".gif" in exts
    assert ".svg" in exts
    assert ".epub" in exts
    # MuPDF opens neither of these: .webp it refuses outright, and .xlsx
    # it opens but renders with every string cell missing.
    assert ".webp" not in exts
    assert ".xlsx" not in exts
    assert ".eml" not in exts
    assert ".docm" not in exts
    assert ".heic" not in exts


def test_docling_is_retired_so_its_name_falls_back_to_the_union():
    """No DOCLING entry survives; an unknown service gets the full union."""
    from app.utils.file_extensions import (
        DOCUMENT_EXTENSIONS,
        get_document_extensions_for_service,
    )

    assert get_document_extensions_for_service("DOCLING") == DOCUMENT_EXTENSIONS


def test_get_extensions_for_llamacloud():
    from app.utils.file_extensions import get_document_extensions_for_service

    exts = get_document_extensions_for_service("LLAMACLOUD")
    assert ".docm" in exts
    assert ".gif" in exts
    assert ".svg" in exts
    assert ".hwp" in exts
    assert ".eml" not in exts
    assert ".heic" not in exts


def test_unstructured_is_retired_so_the_formats_only_it_read_are_gone():
    """Capability removal, pinned: no parser here opens these any more."""
    from app.utils.file_extensions import (
        DOCUMENT_EXTENSIONS,
        get_document_extensions_for_service,
    )

    for ext in (".eml", ".msg", ".p7s", ".heic"):
        assert ext not in DOCUMENT_EXTENSIONS, ext
    # Its name is now unknown, so it gets the union like any other.
    assert get_document_extensions_for_service("UNSTRUCTURED") == DOCUMENT_EXTENSIONS


def test_get_extensions_for_none_returns_union():
    from app.utils.file_extensions import (
        DOCUMENT_EXTENSIONS,
        get_document_extensions_for_service,
    )

    assert get_document_extensions_for_service(None) == DOCUMENT_EXTENSIONS


# ---------------------------------------------------------------------------
# IMAGE_EXTENSIONS
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "ext",
    [
        ".png",
        ".jpg",
        ".jpeg",
        ".gif",
        ".bmp",
        ".tiff",
        ".tif",
        ".webp",
        ".svg",
        ".heic",
        ".heif",
    ],
)
def test_image_extensions_contains_expected(ext):
    from app.utils.file_extensions import IMAGE_EXTENSIONS

    assert ext in IMAGE_EXTENSIONS


#: .heic was read by the retired unstructured parser and by nothing else
#: here, so it is the one image format with no document parser behind the
#: vision model. A .heic upload with Vision off is refused at the upload
#: route, and one whose vision call fails is refused by the pipeline
#: ("vision LLM failed ... and document parser does not support this
#: format") -- never stored as an empty document.
IMAGES_WITHOUT_A_DOCUMENT_PARSER = frozenset({".heic"})


def test_every_image_extension_but_heic_has_a_document_parser_behind_it():
    """The vision model reads them all; this is the fallback when it cannot."""
    from app.utils.file_extensions import DOCUMENT_EXTENSIONS, IMAGE_EXTENSIONS

    missing = IMAGE_EXTENSIONS - DOCUMENT_EXTENSIONS
    assert missing == IMAGES_WITHOUT_A_DOCUMENT_PARSER, (
        f"image extensions without a document parser changed: {missing}"
    )
