"""The Local parser: pymupdf4llm with one fixed, text-layer-first profile.

The Docling parser this replaced shipped a forced-OCR mode that threw
away the PDF's own text layer. Local keeps the text layer and OCRs only
what the layout model flags, and a Local upload must reach this parser
whatever ETL_CLOUD_SERVICE says.
"""

import pytest

from app.etl_pipeline.etl_document import DocumentParser, EtlRequest
from app.etl_pipeline.etl_pipeline_service import EtlPipelineService
from app.etl_pipeline.parsers.pymupdf4llm import parse_with_pymupdf4llm

pytestmark = pytest.mark.unit


async def test_a_local_upload_reaches_pymupdf4llm_whatever_the_cloud_service(
    tmp_path, mocker
):
    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake")

    mocker.patch("app.config.config.ETL_CLOUD_SERVICE", "LLAMACLOUD")
    parser = mocker.patch(
        "app.etl_pipeline.parsers.pymupdf4llm.parse_with_pymupdf4llm",
        new=mocker.AsyncMock(return_value="# Parsed PDF"),
    )
    cloud = mocker.patch(
        "app.etl_pipeline.parsers.llamacloud.parse_with_llamacloud",
        new=mocker.AsyncMock(return_value="cloud"),
    )

    result = await EtlPipelineService().extract(
        EtlRequest(
            file_path=str(pdf_file),
            filename="report.pdf",
            parser=DocumentParser.LOCAL,
        )
    )

    assert result.markdown_content == "# Parsed PDF"
    assert result.etl_service == "PYMUPDF4LLM"
    parser.assert_awaited_once_with(str(pdf_file), "report.pdf")
    cloud.assert_not_awaited()


async def test_local_ocrs_only_flagged_regions_on_top_of_the_text_layer(
    tmp_path, mocker
):
    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake")

    to_markdown = mocker.patch(
        "pymupdf4llm.to_markdown", return_value="# md", create=True
    )

    await parse_with_pymupdf4llm(str(pdf_file), "report.pdf")

    kwargs = to_markdown.call_args.kwargs
    # A scanned page must never come back blank ...
    assert kwargs["use_ocr"] is True
    # ... but the retired premium profile (forced OCR at 300 dpi) is gone.
    assert kwargs["force_ocr"] is False
    assert kwargs["ocr_dpi"] == 150


async def test_local_parses_a_real_pdf_text_layer(tmp_path):
    """No mocks: a generated PDF goes through MuPDF and comes back as text."""
    pymupdf = pytest.importorskip("pymupdf")
    pytest.importorskip("pymupdf4llm")

    pdf_path = tmp_path / "real.pdf"
    document = pymupdf.open()
    page = document.new_page()
    page.insert_text((72, 72), "Quarterly revenue rose twelve percent.")
    document.save(pdf_path)
    document.close()

    result = await EtlPipelineService().extract(
        EtlRequest(
            file_path=str(pdf_path),
            filename="real.pdf",
            parser=DocumentParser.LOCAL,
        )
    )

    assert "Quarterly revenue rose twelve percent." in result.markdown_content
    assert result.etl_service == "PYMUPDF4LLM"


async def test_images_are_left_for_the_picture_describer(tmp_path, mocker):
    """The parser emits text only. Describing pictures is a later stage.

    Turning either of these on would put file paths or base64 blobs into
    the markdown we are about to chunk and embed, and would duplicate
    work picture_describer already does with a vision LLM.
    """
    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake")

    to_markdown = mocker.patch(
        "pymupdf4llm.to_markdown", return_value="# md", create=True
    )

    await parse_with_pymupdf4llm(str(pdf_file), "report.pdf")

    kwargs = to_markdown.call_args.kwargs
    assert kwargs["write_images"] is False
    assert kwargs["embed_images"] is False


async def test_parse_returns_the_markdown_unchanged(tmp_path, mocker):
    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake")

    mocker.patch(
        "pymupdf4llm.to_markdown",
        return_value="# Title\n\n| a | b |\n|---|---|\n| 1 | 2 |\n",
        create=True,
    )

    out = await parse_with_pymupdf4llm(str(pdf_file), "report.pdf")

    assert out.startswith("# Title")
    assert "| a | b |" in out


async def test_parsing_does_not_block_the_event_loop(tmp_path, mocker):
    """to_markdown is CPU-bound; it must run off the loop.

    A synchronous call would stall every other coroutine in the worker
    for the length of a parse -- seconds per document, and minutes on a
    scanned book.
    """
    import asyncio

    pdf_file = tmp_path / "report.pdf"
    pdf_file.write_bytes(b"%PDF-1.4 fake")

    calling_threads = []

    def _slow_to_markdown(*args, **kwargs):
        import threading

        calling_threads.append(threading.current_thread())
        return "# md"

    mocker.patch("pymupdf4llm.to_markdown", new=_slow_to_markdown, create=True)

    main_thread = __import__("threading").current_thread()
    await parse_with_pymupdf4llm(str(pdf_file), "report.pdf")

    assert calling_threads, "to_markdown was never called"
    assert calling_threads[0] is not main_thread
    assert asyncio.get_running_loop().is_running()
