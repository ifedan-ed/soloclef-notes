"""Tests for EtlPipelineService -- the unified ETL pipeline public interface."""

import pytest

from app.etl_pipeline.etl_document import DocumentParser, EtlRequest
from app.etl_pipeline.etl_pipeline_service import EtlPipelineService

pytestmark = pytest.mark.unit


async def test_extract_txt_file_returns_markdown(tmp_path):
    """Tracer bullet: a .txt file is read and returned as-is in an EtlResult."""
    txt_file = tmp_path / "hello.txt"
    txt_file.write_text("Hello, world!", encoding="utf-8")

    service = EtlPipelineService()
    result = await service.extract(
        EtlRequest(file_path=str(txt_file), filename="hello.txt")
    )

    assert result.markdown_content == "Hello, world!"
    assert result.etl_service == "PLAINTEXT"
    assert result.content_type == "plaintext"


async def test_extract_md_file(tmp_path):
    """A .md file is classified as PLAINTEXT and extracted."""
    md_file = tmp_path / "readme.md"
    md_file.write_text("# Title\n\nBody text.", encoding="utf-8")

    result = await EtlPipelineService().extract(
        EtlRequest(file_path=str(md_file), filename="readme.md")
    )

    assert result.markdown_content == "# Title\n\nBody text."
    assert result.etl_service == "PLAINTEXT"
    assert result.content_type == "plaintext"


async def test_extract_markdown_file(tmp_path):
    """A .markdown file is classified as PLAINTEXT and extracted."""
    md_file = tmp_path / "notes.markdown"
    md_file.write_text("Some notes.", encoding="utf-8")

    result = await EtlPipelineService().extract(
        EtlRequest(file_path=str(md_file), filename="notes.markdown")
    )

    assert result.markdown_content == "Some notes."
    assert result.etl_service == "PLAINTEXT"


async def test_extract_python_file(tmp_path):
    """A .py source code file is classified as PLAINTEXT."""
    py_file = tmp_path / "script.py"
    py_file.write_text("print('hello')", encoding="utf-8")

    result = await EtlPipelineService().extract(
        EtlRequest(file_path=str(py_file), filename="script.py")
    )

    assert result.markdown_content == "print('hello')"
    assert result.etl_service == "PLAINTEXT"
    assert result.content_type == "plaintext"


async def test_extract_js_file(tmp_path):
    """A .js source code file is classified as PLAINTEXT."""
    js_file = tmp_path / "app.js"
    js_file.write_text("console.log('hi');", encoding="utf-8")

    result = await EtlPipelineService().extract(
        EtlRequest(file_path=str(js_file), filename="app.js")
    )

    assert result.markdown_content == "console.log('hi');"
    assert result.etl_service == "PLAINTEXT"


async def test_extract_csv_returns_markdown_table(tmp_path):
    """A .csv file is converted to a markdown table."""
    csv_file = tmp_path / "data.csv"
    csv_file.write_text("name,age\nAlice,30\nBob,25\n", encoding="utf-8")

    result = await EtlPipelineService().extract(
        EtlRequest(file_path=str(csv_file), filename="data.csv")
    )

    assert "| name | age |" in result.markdown_content
    assert "| Alice | 30 |" in result.markdown_content
    assert result.etl_service == "DIRECT_CONVERT"
    assert result.content_type == "direct_convert"


async def test_extract_tsv_returns_markdown_table(tmp_path):
    """A .tsv file is converted to a markdown table."""
    tsv_file = tmp_path / "data.tsv"
    tsv_file.write_text("x\ty\n1\t2\n", encoding="utf-8")

    result = await EtlPipelineService().extract(
        EtlRequest(file_path=str(tsv_file), filename="data.tsv")
    )

    assert "| x | y |" in result.markdown_content
    assert result.etl_service == "DIRECT_CONVERT"


async def test_extract_html_returns_markdown(tmp_path):
    """An .html file is converted to markdown."""
    html_file = tmp_path / "page.html"
    html_file.write_text("<h1>Title</h1><p>Body</p>", encoding="utf-8")

    result = await EtlPipelineService().extract(
        EtlRequest(file_path=str(html_file), filename="page.html")
    )

    assert "Title" in result.markdown_content
    assert "Body" in result.markdown_content
    assert result.etl_service == "DIRECT_CONVERT"


async def test_extract_mp3_returns_transcription(tmp_path, mocker):
    """An .mp3 audio file is transcribed via litellm.atranscription."""
    audio_file = tmp_path / "recording.mp3"
    audio_file.write_bytes(b"\x00" * 100)

    mocker.patch("app.config.config.STT_SERVICE", "openai/whisper-1")
    mocker.patch("app.config.config.STT_SERVICE_API_KEY", "fake-key")
    mocker.patch("app.config.config.STT_SERVICE_API_BASE", None)

    mock_transcription = mocker.patch(
        "app.etl_pipeline.parsers.audio.atranscription",
        return_value={"text": "Hello from audio"},
    )

    result = await EtlPipelineService().extract(
        EtlRequest(file_path=str(audio_file), filename="recording.mp3")
    )

    assert "Hello from audio" in result.markdown_content
    assert result.etl_service == "AUDIO"
    assert result.content_type == "audio"
    mock_transcription.assert_called_once()


# ---------------------------------------------------------------------------
# Slice 7 - PYMUPDF4LLM document parsing
# ---------------------------------------------------------------------------


async def test_extract_pdf_with_pymupdf4llm(tmp_path, mocker):
    """With no choice made, a .pdf goes to the default Local parser."""
    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake")

    mocker.patch(
        "app.etl_pipeline.parsers.pymupdf4llm.parse_with_pymupdf4llm",
        new=mocker.AsyncMock(return_value="# Parsed PDF"),
    )

    result = await EtlPipelineService().extract(
        EtlRequest(file_path=str(pdf_file), filename="report.pdf")
    )

    assert result.markdown_content == "# Parsed PDF"
    assert result.etl_service == "PYMUPDF4LLM"
    assert result.content_type == "document"


# ---------------------------------------------------------------------------
# Slice 8 - UNSTRUCTURED document parsing
# ---------------------------------------------------------------------------


async def test_extract_pdf_with_unstructured(tmp_path, mocker):
    """Cloud backed by the unstructured library returns parsed markdown."""
    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake")

    mocker.patch("app.config.config.ETL_CLOUD_SERVICE", "UNSTRUCTURED")

    class FakeDoc:
        def __init__(self, text):
            self.page_content = text

    fake_loader_instance = mocker.AsyncMock()
    fake_loader_instance.aload.return_value = [
        FakeDoc("Page 1 content"),
        FakeDoc("Page 2 content"),
    ]
    mocker.patch(
        "langchain_unstructured.UnstructuredLoader",
        return_value=fake_loader_instance,
    )

    result = await EtlPipelineService().extract(
        EtlRequest(
            parser=DocumentParser.CLOUD, file_path=str(pdf_file), filename="report.pdf"
        )
    )

    assert "Page 1 content" in result.markdown_content
    assert "Page 2 content" in result.markdown_content
    assert result.etl_service == "UNSTRUCTURED"
    assert result.content_type == "document"


# ---------------------------------------------------------------------------
# Slice 9 - LLAMACLOUD document parsing
# ---------------------------------------------------------------------------


async def test_extract_pdf_with_llamacloud(tmp_path, mocker):
    """Cloud backed by LlamaCloud returns parsed markdown."""
    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake content " * 10)

    mocker.patch("app.config.config.ETL_CLOUD_SERVICE", "LLAMACLOUD")
    mocker.patch("app.config.config.LLAMA_CLOUD_API_KEY", "fake-key", create=True)

    _mock_llamacloud(mocker, "# LlamaCloud parsed")

    result = await EtlPipelineService().extract(
        EtlRequest(
            parser=DocumentParser.CLOUD,
            file_path=str(pdf_file),
            filename="report.pdf",
            estimated_pages=5,
        )
    )

    assert result.markdown_content == "# LlamaCloud parsed"
    assert result.etl_service == "LLAMACLOUD"
    assert result.content_type == "document"


# ---------------------------------------------------------------------------
# Slice 9b - LLAMACLOUD + Azure DI accelerator
# ---------------------------------------------------------------------------


def _mock_azure_di(mocker, content="# Azure DI parsed"):
    """Wire up Azure DI mocks and return the fake client for assertions."""

    class FakeResult:
        pass

    FakeResult.content = content

    fake_poller = mocker.AsyncMock()
    fake_poller.result.return_value = FakeResult()

    fake_client = mocker.AsyncMock()
    fake_client.begin_analyze_document.return_value = fake_poller
    fake_client.__aenter__ = mocker.AsyncMock(return_value=fake_client)
    fake_client.__aexit__ = mocker.AsyncMock(return_value=False)

    mocker.patch(
        "azure.ai.documentintelligence.aio.DocumentIntelligenceClient",
        return_value=fake_client,
    )
    mocker.patch(
        "azure.ai.documentintelligence.models.DocumentContentFormat",
        mocker.MagicMock(MARKDOWN="markdown"),
    )
    mocker.patch(
        "azure.core.credentials.AzureKeyCredential",
        return_value=mocker.MagicMock(),
    )
    return fake_client


def _mock_llamacloud(mocker, content="# LlamaCloud parsed"):
    """Replace the LlamaCloud parser; the mock, for assertions.

    Its requests to LlamaCloud are pinned in test_llamacloud_parser.py.
    """
    return mocker.patch(
        "app.etl_pipeline.parsers.llamacloud.parse_with_llamacloud",
        new=mocker.AsyncMock(return_value=content),
    )


async def test_llamacloud_with_azure_di_uses_azure_for_pdf(tmp_path, mocker):
    """When Azure DI is configured, a supported extension (.pdf) is parsed by Azure DI."""
    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake content " * 10)

    mocker.patch("app.config.config.ETL_CLOUD_SERVICE", "LLAMACLOUD")
    mocker.patch("app.config.config.LLAMA_CLOUD_API_KEY", "fake-key", create=True)
    mocker.patch(
        "app.config.config.AZURE_DI_ENDPOINT",
        "https://fake.cognitiveservices.azure.com/",
        create=True,
    )
    mocker.patch("app.config.config.AZURE_DI_KEY", "fake-key", create=True)

    fake_client = _mock_azure_di(mocker, "# Azure DI parsed")
    fake_parser = _mock_llamacloud(mocker)

    result = await EtlPipelineService().extract(
        EtlRequest(
            parser=DocumentParser.CLOUD, file_path=str(pdf_file), filename="report.pdf"
        )
    )

    assert result.markdown_content == "# Azure DI parsed"
    assert result.etl_service == "LLAMACLOUD"
    assert result.content_type == "document"
    fake_client.begin_analyze_document.assert_called_once()
    fake_parser.assert_not_awaited()


async def test_llamacloud_azure_di_fallback_on_failure(tmp_path, mocker):
    """When Azure DI fails, LlamaCloud is used as a fallback."""
    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake content " * 10)

    mocker.patch("app.config.config.ETL_CLOUD_SERVICE", "LLAMACLOUD")
    mocker.patch("app.config.config.LLAMA_CLOUD_API_KEY", "fake-key", create=True)
    mocker.patch(
        "app.config.config.AZURE_DI_ENDPOINT",
        "https://fake.cognitiveservices.azure.com/",
        create=True,
    )
    mocker.patch("app.config.config.AZURE_DI_KEY", "fake-key", create=True)

    mocker.patch(
        "app.etl_pipeline.parsers.azure_doc_intelligence.parse_with_azure_doc_intelligence",
        side_effect=RuntimeError("Azure DI unavailable"),
    )
    fake_parser = _mock_llamacloud(mocker, "# LlamaCloud fallback")

    result = await EtlPipelineService().extract(
        EtlRequest(
            parser=DocumentParser.CLOUD,
            file_path=str(pdf_file),
            filename="report.pdf",
            estimated_pages=5,
        )
    )

    assert result.markdown_content == "# LlamaCloud fallback"
    assert result.etl_service == "LLAMACLOUD"
    assert result.content_type == "document"
    fake_parser.assert_awaited_once()


async def test_llamacloud_skips_azure_di_for_unsupported_ext(tmp_path, mocker):
    """Azure DI is skipped for extensions it doesn't support (e.g. .epub)."""
    epub_file = tmp_path / "book.epub"
    epub_file.write_bytes(b"\x00" * 10)

    mocker.patch("app.config.config.ETL_CLOUD_SERVICE", "LLAMACLOUD")
    mocker.patch("app.config.config.LLAMA_CLOUD_API_KEY", "fake-key", create=True)
    mocker.patch(
        "app.config.config.AZURE_DI_ENDPOINT",
        "https://fake.cognitiveservices.azure.com/",
        create=True,
    )
    mocker.patch("app.config.config.AZURE_DI_KEY", "fake-key", create=True)

    fake_client = _mock_azure_di(mocker)
    fake_parser = _mock_llamacloud(mocker, "# Epub from LlamaCloud")

    result = await EtlPipelineService().extract(
        EtlRequest(
            parser=DocumentParser.CLOUD,
            file_path=str(epub_file),
            filename="book.epub",
            estimated_pages=50,
        )
    )

    assert result.markdown_content == "# Epub from LlamaCloud"
    assert result.etl_service == "LLAMACLOUD"
    fake_client.begin_analyze_document.assert_not_called()
    fake_parser.assert_awaited_once()


async def test_llamacloud_without_azure_di_uses_llamacloud_directly(tmp_path, mocker):
    """When Azure DI is not configured, LlamaCloud handles all file types directly."""
    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake content " * 10)

    mocker.patch("app.config.config.ETL_CLOUD_SERVICE", "LLAMACLOUD")
    mocker.patch("app.config.config.LLAMA_CLOUD_API_KEY", "fake-key", create=True)
    mocker.patch("app.config.config.AZURE_DI_ENDPOINT", None, create=True)
    mocker.patch("app.config.config.AZURE_DI_KEY", None, create=True)

    fake_parser = _mock_llamacloud(mocker, "# Direct LlamaCloud")

    result = await EtlPipelineService().extract(
        EtlRequest(
            parser=DocumentParser.CLOUD,
            file_path=str(pdf_file),
            filename="report.pdf",
            estimated_pages=5,
        )
    )

    assert result.markdown_content == "# Direct LlamaCloud"
    assert result.etl_service == "LLAMACLOUD"
    assert result.content_type == "document"
    fake_parser.assert_awaited_once()


async def test_llamacloud_heif_accepted_only_with_azure_di(tmp_path, mocker):
    """.heif is accepted by LLAMACLOUD only when Azure DI credentials are set."""
    from app.etl_pipeline.exceptions import EtlUnsupportedFileError

    heif_file = tmp_path / "photo.heif"
    heif_file.write_bytes(b"\x00" * 100)

    mocker.patch("app.config.config.ETL_CLOUD_SERVICE", "LLAMACLOUD")
    mocker.patch("app.config.config.LLAMA_CLOUD_API_KEY", "fake-key", create=True)
    mocker.patch("app.config.config.AZURE_DI_ENDPOINT", None, create=True)
    mocker.patch("app.config.config.AZURE_DI_KEY", None, create=True)

    with pytest.raises(
        EtlUnsupportedFileError, match="document parser does not support this format"
    ):
        await EtlPipelineService().extract(
            EtlRequest(
                parser=DocumentParser.CLOUD,
                file_path=str(heif_file),
                filename="photo.heif",
            )
        )

    mocker.patch(
        "app.config.config.AZURE_DI_ENDPOINT",
        "https://fake.cognitiveservices.azure.com/",
    )
    mocker.patch("app.config.config.AZURE_DI_KEY", "fake-key")

    fake_client = _mock_azure_di(mocker, "# HEIF from Azure DI")
    result = await EtlPipelineService().extract(
        EtlRequest(
            parser=DocumentParser.CLOUD, file_path=str(heif_file), filename="photo.heif"
        )
    )

    assert result.markdown_content == "# HEIF from Azure DI"
    assert result.etl_service == "LLAMACLOUD"
    fake_client.begin_analyze_document.assert_called_once()


# ---------------------------------------------------------------------------
# Slice 10 - unknown extension falls through to document ETL
# ---------------------------------------------------------------------------


async def test_unknown_extension_uses_document_etl(tmp_path, mocker):
    """An allowlisted document extension (.docx) routes to the document ETL path."""
    docx_file = tmp_path / "doc.docx"
    docx_file.write_bytes(b"PK fake docx")

    mocker.patch(
        "app.etl_pipeline.parsers.pymupdf4llm.parse_with_pymupdf4llm",
        new=mocker.AsyncMock(return_value="Docx content"),
    )

    result = await EtlPipelineService().extract(
        EtlRequest(file_path=str(docx_file), filename="doc.docx")
    )

    assert result.markdown_content == "Docx content"
    assert result.content_type == "document"


# ---------------------------------------------------------------------------
# Slice 11 - EtlRequest validation
# ---------------------------------------------------------------------------


def test_etl_request_requires_filename():
    """EtlRequest rejects missing filename."""
    with pytest.raises(ValueError, match="filename must not be empty"):
        EtlRequest(file_path="/tmp/some.txt", filename="")


# ---------------------------------------------------------------------------
# Slice 12 - unknown ETL_CLOUD_SERVICE raises EtlServiceUnavailableError
# ---------------------------------------------------------------------------


async def test_unknown_etl_service_raises(tmp_path, mocker):
    """A Cloud upload with an unrecognised ETL_CLOUD_SERVICE is unavailable."""
    from app.etl_pipeline.exceptions import EtlServiceUnavailableError

    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF fake")

    mocker.patch("app.config.config.ETL_CLOUD_SERVICE", "NONEXISTENT")

    with pytest.raises(EtlServiceUnavailableError, match="unknown_service"):
        await EtlPipelineService().extract(
            EtlRequest(
                parser=DocumentParser.CLOUD,
                file_path=str(pdf_file),
                filename="report.pdf",
            )
        )


# ---------------------------------------------------------------------------
# Slice 13 - unsupported file types are rejected before reaching any parser
# ---------------------------------------------------------------------------


def test_unknown_extension_classified_as_unsupported():
    """An unknown extension defaults to UNSUPPORTED (allowlist behaviour)."""
    from app.etl_pipeline.file_classifier import FileCategory, classify_file

    assert classify_file("random.xyz") == FileCategory.UNSUPPORTED


@pytest.mark.parametrize(
    "filename",
    [
        "malware.exe",
        "archive.zip",
        "video.mov",
        "font.woff2",
        "model.blend",
        "data.parquet",
        "package.deb",
        "firmware.bin",
    ],
)
def test_unsupported_extensions_classified_correctly(filename):
    """Extensions not in any allowlist are classified as UNSUPPORTED."""
    from app.etl_pipeline.file_classifier import FileCategory, classify_file

    assert classify_file(filename) == FileCategory.UNSUPPORTED


@pytest.mark.parametrize(
    "filename,expected",
    [
        ("report.pdf", "document"),
        ("doc.docx", "document"),
        ("slides.pptx", "document"),
        ("sheet.xlsx", "document"),
        ("photo.png", "image"),
        ("photo.jpg", "image"),
        ("photo.webp", "image"),
        ("photo.gif", "image"),
        ("photo.heic", "image"),
        ("book.epub", "document"),
        ("letter.odt", "document"),
        ("readme.md", "plaintext"),
        ("data.csv", "direct_convert"),
    ],
)
def test_parseable_extensions_classified_correctly(filename, expected):
    """Parseable files are classified into their correct category."""
    from app.etl_pipeline.file_classifier import FileCategory, classify_file

    result = classify_file(filename)
    assert result != FileCategory.UNSUPPORTED
    assert result.value == expected


async def test_extract_unsupported_file_raises_error(tmp_path):
    """EtlPipelineService.extract() raises EtlUnsupportedFileError for .exe files."""
    from app.etl_pipeline.exceptions import EtlUnsupportedFileError

    exe_file = tmp_path / "program.exe"
    exe_file.write_bytes(b"\x00" * 10)

    with pytest.raises(EtlUnsupportedFileError, match="not supported"):
        await EtlPipelineService().extract(
            EtlRequest(file_path=str(exe_file), filename="program.exe")
        )


async def test_extract_zip_raises_unsupported_error(tmp_path):
    """EtlPipelineService.extract() raises EtlUnsupportedFileError for .zip archives."""
    from app.etl_pipeline.exceptions import EtlUnsupportedFileError

    zip_file = tmp_path / "archive.zip"
    zip_file.write_bytes(b"PK\x03\x04")

    with pytest.raises(EtlUnsupportedFileError, match="not supported"):
        await EtlPipelineService().extract(
            EtlRequest(file_path=str(zip_file), filename="archive.zip")
        )


# ---------------------------------------------------------------------------
# Slice 14 - should_skip_for_service (per-parser document filtering)
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "filename,etl_service,expected_skip",
    [
        ("file.eml", "PYMUPDF4LLM", True),
        ("file.eml", "UNSTRUCTURED", False),
        ("file.docm", "LLAMACLOUD", False),
        ("file.docm", "PYMUPDF4LLM", True),
        ("file.txt", "PYMUPDF4LLM", False),
        ("file.csv", "LLAMACLOUD", False),
        ("file.mp3", "UNSTRUCTURED", False),
        ("file.exe", "LLAMACLOUD", True),
        ("file.pdf", "PYMUPDF4LLM", False),
        # MuPDF refuses WebP outright, so it is skipped rather than
        # handed to a parser that will throw on it.
        ("file.webp", "PYMUPDF4LLM", True),
        ("file.webp", "UNSTRUCTURED", True),
        ("file.gif", "LLAMACLOUD", False),
        ("file.gif", "PYMUPDF4LLM", False),
        # MuPDF opens .xlsx but renders it with every string cell gone.
        ("file.xlsx", "PYMUPDF4LLM", True),
        ("file.xlsx", "LLAMACLOUD", False),
        ("file.heic", "UNSTRUCTURED", False),
        ("file.heic", "PYMUPDF4LLM", True),
        ("file.svg", "LLAMACLOUD", False),
        ("file.svg", "PYMUPDF4LLM", False),
        ("file.p7s", "UNSTRUCTURED", False),
        ("file.p7s", "LLAMACLOUD", True),
        ("file.heif", "LLAMACLOUD", True),
        ("file.heif", "PYMUPDF4LLM", True),
        ("file.heif", "UNSTRUCTURED", True),
    ],
)
def test_should_skip_for_service(filename, etl_service, expected_skip, monkeypatch):
    monkeypatch.setattr("app.config.config.AZURE_DI_ENDPOINT", None, raising=False)
    monkeypatch.setattr("app.config.config.AZURE_DI_KEY", None, raising=False)
    from app.etl_pipeline.file_classifier import should_skip_for_service

    assert should_skip_for_service(filename, etl_service) is expected_skip, (
        f"{filename} with {etl_service}: expected skip={expected_skip}"
    )


def test_heif_not_skipped_for_llamacloud_when_azure_di_configured(monkeypatch):
    """With Azure DI credentials, .heif is accepted by LLAMACLOUD."""
    monkeypatch.setattr(
        "app.config.config.AZURE_DI_ENDPOINT",
        "https://fake.cognitiveservices.azure.com/",
        raising=False,
    )
    monkeypatch.setattr("app.config.config.AZURE_DI_KEY", "fake-key", raising=False)
    from app.etl_pipeline.file_classifier import should_skip_for_service

    assert should_skip_for_service("file.heif", "LLAMACLOUD") is False


# ---------------------------------------------------------------------------
# Slice 14b - ETL pipeline rejects per-parser incompatible documents
# ---------------------------------------------------------------------------


async def test_extract_docm_with_pymupdf4llm_raises_unsupported(tmp_path, mocker):
    """MuPDF cannot parse .docm -- pipeline should reject before dispatching."""
    from app.etl_pipeline.exceptions import EtlUnsupportedFileError

    docm_file = tmp_path / "macro.docm"
    docm_file.write_bytes(b"\x00" * 10)

    with pytest.raises(EtlUnsupportedFileError, match="local parser"):
        await EtlPipelineService().extract(
            EtlRequest(
                file_path=str(docm_file),
                filename="macro.docm",
                parser=DocumentParser.LOCAL,
            )
        )


async def test_extract_eml_with_pymupdf4llm_raises_unsupported(tmp_path, mocker):
    """MuPDF cannot parse .eml -- pipeline should reject before dispatching."""
    from app.etl_pipeline.exceptions import EtlUnsupportedFileError

    eml_file = tmp_path / "mail.eml"
    eml_file.write_bytes(b"From: test@example.com")

    with pytest.raises(EtlUnsupportedFileError, match="local parser"):
        await EtlPipelineService().extract(
            EtlRequest(
                file_path=str(eml_file),
                filename="mail.eml",
                parser=DocumentParser.LOCAL,
            )
        )


# ---------------------------------------------------------------------------
# Image extraction via vision LLM
# ---------------------------------------------------------------------------


async def test_extract_image_with_vision_llm(tmp_path):
    """An image file is analyzed by the vision LLM when provided."""
    from unittest.mock import AsyncMock, MagicMock

    img_file = tmp_path / "photo.png"
    img_file.write_bytes(b"\x89PNG\r\n\x1a\n" + b"\x00" * 50)

    fake_response = MagicMock()
    fake_response.content = "# A photo of a sunset over the ocean"
    fake_llm = AsyncMock()
    fake_llm.ainvoke.return_value = fake_response

    service = EtlPipelineService(vision_llm=fake_llm)
    result = await service.extract(
        EtlRequest(file_path=str(img_file), filename="photo.png")
    )

    assert result.markdown_content == "# A photo of a sunset over the ocean"
    assert result.etl_service == "VISION_LLM"
    assert result.content_type == "image"
    fake_llm.ainvoke.assert_called_once()


async def test_extract_image_falls_back_to_document_without_vision_llm(
    tmp_path, mocker
):
    """Without a vision LLM, image files fall back to the document parser."""
    mocker.patch(
        "app.etl_pipeline.parsers.pymupdf4llm.parse_with_pymupdf4llm",
        new=mocker.AsyncMock(return_value="# OCR text from image"),
    )

    img_file = tmp_path / "scan.png"
    img_file.write_bytes(b"\x89PNG\r\n\x1a\n" + b"\x00" * 50)

    service = EtlPipelineService()
    result = await service.extract(
        EtlRequest(file_path=str(img_file), filename="scan.png")
    )

    assert result.markdown_content == "# OCR text from image"
    assert result.etl_service == "PYMUPDF4LLM"
    assert result.content_type == "document"


# ---------------------------------------------------------------------------
# Document path with vision LLM: per-image descriptions are appended
# ---------------------------------------------------------------------------


def _fake_extraction_result(*descriptions):
    from app.etl_pipeline.picture_describer import (
        PictureDescription,
        PictureExtractionResult,
    )

    return PictureExtractionResult(
        descriptions=[
            PictureDescription(
                page_number=d["page"],
                ordinal_in_page=d.get("ordinal", 0),
                name=d["name"],
                sha256=d.get("sha", "deadbeef"),
                description=d["desc"],
            )
            for d in descriptions
        ]
    )


async def test_extract_pdf_with_vision_llm_inlines_image_blocks(tmp_path, mocker):
    """A PDF with an `<!-- image -->` placeholder + caption gets the
    block spliced inline (no orphaned ``## Image Content`` section).

    This is the headline scenario for the medxpertqa benchmark: the
    image content lives in the same chunk as the surrounding case text
    so retrieval pulls the question, image, and answer options together.
    """
    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake content")

    mocker.patch(
        "app.etl_pipeline.parsers.pymupdf4llm.parse_with_pymupdf4llm",
        new=mocker.AsyncMock(
            return_value="# MedXpertQA-MM MM-130\n\n"
            "## Clinical case\n\nA 44-year-old man...\n\n"
            "<!-- image -->\nImage: MM-130-a.jpeg\n\n"
            "## Answer choices\n\nA) ...\n"
        ),
    )

    extraction = _fake_extraction_result(
        {
            "page": 1,
            "name": "Im0",
            "desc": "Axial CT showing a large cystic mass.",
        }
    )
    mocker.patch(
        "app.etl_pipeline.picture_describer.describe_pictures",
        new=mocker.AsyncMock(return_value=extraction),
    )

    fake_llm = mocker.MagicMock()
    result = await EtlPipelineService(vision_llm=fake_llm).extract(
        EtlRequest(file_path=str(pdf_file), filename="report.pdf")
    )

    md = result.markdown_content
    # The placeholder + caption are gone, replaced by a horizontal-
    # rule-delimited section with the captioned filename.
    assert "<!-- image -->" not in md
    assert "Image: MM-130-a.jpeg" not in md
    assert "**Embedded image:** `MM-130-a.jpeg`" in md
    assert "**Visual description:**" in md
    assert "Axial CT showing a large cystic mass." in md
    # No OCR section -- our fake_extraction_result has no ocr_text,
    # and the format omits the section when there's no text to show.
    assert "**OCR text:**" not in md
    # No raw HTML / XML tags or blockquote wrapping leak.
    assert "<image" not in md
    assert "> **Embedded image:**" not in md
    # No appended section -- everything went inline.
    assert "## Image Content" not in md
    # Surrounding case text + answer options are preserved.
    assert "A 44-year-old man..." in md
    assert "## Answer choices" in md
    assert "A) ..." in md


async def test_extract_pdf_with_vision_llm_appends_when_no_marker(tmp_path, mocker):
    """When parser markdown has no image markers, descriptions get appended.

    This is the fallback path for parsers that drop image placeholders
    entirely. The image content still ends up in the markdown -- just
    in a clearly-labeled section rather than inline.
    """
    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake content")

    mocker.patch(
        "app.etl_pipeline.parsers.pymupdf4llm.parse_with_pymupdf4llm",
        new=mocker.AsyncMock(
            return_value="# Parsed PDF text\n\nNo image markers anywhere.\n"
        ),
    )

    extraction = _fake_extraction_result(
        {"page": 1, "name": "Im0", "desc": "An image description."}
    )
    mocker.patch(
        "app.etl_pipeline.picture_describer.describe_pictures",
        new=mocker.AsyncMock(return_value=extraction),
    )

    fake_llm = mocker.MagicMock()
    result = await EtlPipelineService(vision_llm=fake_llm).extract(
        EtlRequest(file_path=str(pdf_file), filename="report.pdf")
    )

    md = result.markdown_content
    assert "# Parsed PDF text" in md
    assert "## Image Content (vision-LLM extracted)" in md
    assert "**Embedded image:** `Im0`" in md
    assert "An image description." in md


async def test_extract_pdf_without_vision_llm_skips_picture_descriptions(
    tmp_path, mocker
):
    """No vision LLM -> parser markdown returned as-is."""
    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake content")

    mocker.patch(
        "app.etl_pipeline.parsers.pymupdf4llm.parse_with_pymupdf4llm",
        new=mocker.AsyncMock(return_value="# Parsed PDF text"),
    )

    describe_mock = mocker.patch(
        "app.etl_pipeline.picture_describer.describe_pictures",
        new=mocker.AsyncMock(),
    )

    result = await EtlPipelineService().extract(
        EtlRequest(file_path=str(pdf_file), filename="report.pdf")
    )

    assert result.markdown_content == "# Parsed PDF text"
    assert "<image" not in result.markdown_content
    describe_mock.assert_not_called()


async def test_extract_pdf_with_vision_llm_swallows_describe_failure(tmp_path, mocker):
    """A pypdf or vision LLM blow-up never fails the document upload."""
    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake content")

    mocker.patch(
        "app.etl_pipeline.parsers.pymupdf4llm.parse_with_pymupdf4llm",
        new=mocker.AsyncMock(return_value="# Parsed PDF text"),
    )

    mocker.patch(
        "app.etl_pipeline.picture_describer.describe_pictures",
        new=mocker.AsyncMock(side_effect=RuntimeError("pypdf exploded")),
    )

    fake_llm = mocker.MagicMock()
    result = await EtlPipelineService(vision_llm=fake_llm).extract(
        EtlRequest(file_path=str(pdf_file), filename="report.pdf")
    )

    assert result.markdown_content == "# Parsed PDF text"
    assert result.etl_service == "PYMUPDF4LLM"


async def test_extract_pdf_with_vision_llm_no_images_returns_parser_text(
    tmp_path, mocker
):
    """Vision-LLM-enabled PDF with zero extracted images is unchanged."""
    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake content")

    mocker.patch(
        "app.etl_pipeline.parsers.pymupdf4llm.parse_with_pymupdf4llm",
        new=mocker.AsyncMock(return_value="# Just text, no images"),
    )

    empty = _fake_extraction_result()
    mocker.patch(
        "app.etl_pipeline.picture_describer.describe_pictures",
        new=mocker.AsyncMock(return_value=empty),
    )

    fake_llm = mocker.MagicMock()
    result = await EtlPipelineService(vision_llm=fake_llm).extract(
        EtlRequest(file_path=str(pdf_file), filename="report.pdf")
    )

    assert result.markdown_content == "# Just text, no images"
    assert "<image" not in result.markdown_content


# ---------------------------------------------------------------------------
# Per-image OCR runner: wiring + behaviour
#
# When extracting a PDF with a vision LLM, the ETL service must ALSO
# pass an ``ocr_runner`` to picture_describer. The runner is a closure
# that re-feeds each extracted image through a vision-LLM-less
# EtlPipelineService -- i.e. the same OCR engine that handles
# standalone image uploads (pymupdf4llm/Azure DI/LlamaCloud) gets a crack
# at each embedded image, with the text attached to the inline block.
# ---------------------------------------------------------------------------


async def test_extract_pdf_passes_ocr_runner_to_describe_pictures(tmp_path, mocker):
    """The ETL service must wire an ocr_runner kwarg to describe_pictures."""
    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake content")

    mocker.patch(
        "app.etl_pipeline.parsers.pymupdf4llm.parse_with_pymupdf4llm",
        new=mocker.AsyncMock(return_value="# Parsed PDF text"),
    )

    describe_mock = mocker.patch(
        "app.etl_pipeline.picture_describer.describe_pictures",
        new=mocker.AsyncMock(return_value=_fake_extraction_result()),
    )

    fake_llm = mocker.MagicMock()
    await EtlPipelineService(vision_llm=fake_llm).extract(
        EtlRequest(file_path=str(pdf_file), filename="report.pdf")
    )

    describe_mock.assert_awaited_once()
    _, kwargs = describe_mock.await_args
    assert "ocr_runner" in kwargs
    assert callable(kwargs["ocr_runner"])


async def test_extract_pdf_ocr_runner_invokes_document_parser_on_image(
    tmp_path, mocker
):
    """The OCR runner closure should re-extract each image via the parser.

    We capture the runner that the ETL service passes to
    describe_pictures, invoke it with a fake image path, and assert
    that the parser was called with that image. This proves the closure
    is wired to a vision-LLM-less sub-pipeline (otherwise it would
    recurse into the vision LLM and never hit the OCR engine).
    """
    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake content")
    image_file = tmp_path / "Im0.png"
    image_file.write_bytes(b"\x89PNG\r\n\x1a\n" + b"\x00" * 100)

    fake_parser = mocker.patch(
        "app.etl_pipeline.parsers.pymupdf4llm.parse_with_pymupdf4llm",
        new=mocker.AsyncMock(return_value="Slice 24 / 60   L   R"),
    )

    captured: dict = {}

    async def capture_runner(*args, **kwargs):
        captured["runner"] = kwargs["ocr_runner"]
        return _fake_extraction_result()

    mocker.patch(
        "app.etl_pipeline.picture_describer.describe_pictures",
        new=capture_runner,
    )

    fake_llm = mocker.MagicMock()
    await EtlPipelineService(vision_llm=fake_llm).extract(
        EtlRequest(file_path=str(pdf_file), filename="report.pdf")
    )

    runner = captured["runner"]
    ocr_text = await runner(str(image_file), "Im0.png")

    assert ocr_text == "Slice 24 / 60   L   R"
    # The parser was invoked twice in total: once for the PDF, once
    # for the image we re-fed via the runner.
    assert fake_parser.await_count == 2


async def test_extract_pdf_ocr_runner_returns_empty_on_unsupported_image(
    tmp_path, mocker
):
    """Unsupported image format → runner returns empty string, doesn't raise.

    Common case: a PDF embeds a JPEG2000 or CCITT-TIFF image that
    the parser can't load. We don't want an unsupported format on ONE
    embedded image to spoil the whole PDF extraction; the runner
    should swallow the EtlUnsupportedFileError and return "" so the
    image gets a description but no OCR tag.
    """
    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake content")
    weird_image = tmp_path / "Im0.jp2"  # JPEG2000, unlikely to be supported
    weird_image.write_bytes(b"\x00\x00\x00\x0cjP" + b"\x00" * 50)

    mocker.patch(
        "app.etl_pipeline.parsers.pymupdf4llm.parse_with_pymupdf4llm",
        new=mocker.AsyncMock(return_value="# Parsed PDF text"),
    )

    captured: dict = {}

    async def capture_runner(*args, **kwargs):
        captured["runner"] = kwargs["ocr_runner"]
        return _fake_extraction_result()

    mocker.patch(
        "app.etl_pipeline.picture_describer.describe_pictures",
        new=capture_runner,
    )

    fake_llm = mocker.MagicMock()
    await EtlPipelineService(vision_llm=fake_llm).extract(
        EtlRequest(file_path=str(pdf_file), filename="report.pdf")
    )

    runner = captured["runner"]
    ocr_text = await runner(str(weird_image), "Im0.jp2")

    assert ocr_text == ""


# ---------------------------------------------------------------------------
# Parser choice on the request
# ---------------------------------------------------------------------------


def test_etl_request_defaults_to_no_parser_choice():
    req = EtlRequest(file_path="/tmp/test.pdf", filename="test.pdf")
    assert req.parser is None


def test_etl_request_carries_the_chosen_parser():
    req = EtlRequest(
        file_path="/tmp/test.pdf", filename="test.pdf", parser=DocumentParser.CLOUD
    )
    assert req.parser is DocumentParser.CLOUD


# ---------------------------------------------------------------------------
# Cloud always runs the thorough tier of whichever service backs it
# ---------------------------------------------------------------------------


async def test_cloud_azure_di_accelerator_uses_prebuilt_layout(tmp_path, mocker):
    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake content " * 10)

    mocker.patch(
        "app.config.config.AZURE_DI_ENDPOINT",
        "https://fake.cognitiveservices.azure.com/",
        create=True,
    )
    mocker.patch("app.config.config.AZURE_DI_KEY", "fake-key", create=True)

    fake_client = _mock_azure_di(mocker, "# Azure layout")
    _mock_llamacloud(mocker)

    result = await EtlPipelineService().extract(
        EtlRequest(
            file_path=str(pdf_file),
            filename="report.pdf",
            parser=DocumentParser.CLOUD,
        )
    )

    assert result.markdown_content == "# Azure layout"
    call_args = fake_client.begin_analyze_document.call_args
    assert call_args[0][0] == "prebuilt-layout"


async def test_cloud_llamacloud_always_uses_the_agentic_tier(tmp_path, mocker):
    import functools

    import httpx

    from app.etl_pipeline.parsers import llamacloud

    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake content " * 10)
    uploads: list[httpx.Request] = []

    def llamacloud_v2(request: httpx.Request) -> httpx.Response:
        if request.method == "POST":
            uploads.append(request)
            return httpx.Response(
                200, json={"id": "pjb-1", "project_id": "p", "status": "PENDING"}
            )
        page = {"page_number": 1, "success": True, "markdown": "# Llama agentic"}
        return httpx.Response(
            200,
            json={
                "job": {"id": "pjb-1", "project_id": "p", "status": "COMPLETED"},
                "markdown": {"pages": [page]},
            },
        )

    mocker.patch.object(
        llamacloud,
        "parse_with_llamacloud",
        functools.partial(
            llamacloud.parse_with_llamacloud,
            transport=httpx.MockTransport(llamacloud_v2),
            poll_interval=0,
        ),
    )

    result = await EtlPipelineService().extract(
        EtlRequest(
            file_path=str(pdf_file),
            filename="report.pdf",
            estimated_pages=5,
            parser=DocumentParser.CLOUD,
        )
    )

    assert result.markdown_content == "# Llama agentic"
    (upload,) = uploads
    assert upload.url.path == "/api/v2/parse/upload"
    assert '"tier": "agentic"' in upload.content.decode("latin-1")


# ---------------------------------------------------------------------------
# Images: vision LLM when asked for, otherwise the chosen parser
# ---------------------------------------------------------------------------


async def test_image_without_vision_goes_to_the_chosen_cloud_parser(tmp_path, mocker):
    img_file = tmp_path / "scan.png"
    img_file.write_bytes(b"\x89PNG\r\n\x1a\n" + b"\x00" * 50)

    local = mocker.patch(
        "app.etl_pipeline.parsers.pymupdf4llm.parse_with_pymupdf4llm",
        new=mocker.AsyncMock(return_value="local"),
    )
    cloud = mocker.patch(
        "app.etl_pipeline.parsers.llamacloud.parse_with_llamacloud",
        new=mocker.AsyncMock(return_value="# Cloud OCR"),
    )

    result = await EtlPipelineService().extract(
        EtlRequest(
            file_path=str(img_file), filename="scan.png", parser=DocumentParser.CLOUD
        )
    )

    assert result.markdown_content == "# Cloud OCR"
    assert result.etl_service == "LLAMACLOUD"
    cloud.assert_awaited_once()
    local.assert_not_awaited()


async def test_image_with_vision_never_reaches_the_parser(tmp_path, mocker):
    from unittest.mock import AsyncMock, MagicMock

    img_file = tmp_path / "scan.png"
    img_file.write_bytes(b"\x89PNG\r\n\x1a\n" + b"\x00" * 50)
    cloud = mocker.patch(
        "app.etl_pipeline.parsers.llamacloud.parse_with_llamacloud",
        new=mocker.AsyncMock(return_value="cloud"),
    )
    fake_llm = AsyncMock()
    fake_llm.ainvoke.return_value = MagicMock(content="# A described scan")

    result = await EtlPipelineService(vision_llm=fake_llm).extract(
        EtlRequest(
            file_path=str(img_file), filename="scan.png", parser=DocumentParser.CLOUD
        )
    )

    assert result.etl_service == "VISION_LLM"
    cloud.assert_not_awaited()


async def test_image_the_local_parser_cannot_open_is_not_sent_to_cloud(tmp_path):
    from app.etl_pipeline.exceptions import EtlUnsupportedFileError

    img_file = tmp_path / "photo.webp"
    img_file.write_bytes(b"RIFF\x00\x00\x00\x00WEBP")

    with pytest.raises(EtlUnsupportedFileError, match=r"photo\.webp"):
        await EtlPipelineService().extract(
            EtlRequest(
                file_path=str(img_file),
                filename="photo.webp",
                parser=DocumentParser.LOCAL,
            )
        )


async def test_picture_ocr_uses_the_parser_the_document_chose(tmp_path, mocker):
    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake content")
    image_file = tmp_path / "Im0.png"
    image_file.write_bytes(b"\x89PNG\r\n\x1a\n" + b"\x00" * 100)

    local = mocker.patch(
        "app.etl_pipeline.parsers.pymupdf4llm.parse_with_pymupdf4llm",
        new=mocker.AsyncMock(return_value="local"),
    )
    cloud = mocker.patch(
        "app.etl_pipeline.parsers.llamacloud.parse_with_llamacloud",
        new=mocker.AsyncMock(return_value="cloud text"),
    )
    captured: dict = {}

    async def capture_runner(*args, **kwargs):
        captured["runner"] = kwargs["ocr_runner"]
        return _fake_extraction_result()

    mocker.patch(
        "app.etl_pipeline.picture_describer.describe_pictures", new=capture_runner
    )

    await EtlPipelineService(vision_llm=mocker.MagicMock()).extract(
        EtlRequest(
            file_path=str(pdf_file), filename="report.pdf", parser=DocumentParser.CLOUD
        )
    )

    assert await captured["runner"](str(image_file), "Im0.png") == "cloud text"
    assert cloud.await_count == 2
    local.assert_not_awaited()
