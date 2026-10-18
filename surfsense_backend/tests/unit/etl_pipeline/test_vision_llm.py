"""Unit tests for the vision_llm parser helpers.

Two helpers exist:

- :func:`parse_with_vision_llm` -- single-shot for standalone image
  uploads (.png/.jpg/etc). Returns combined markdown (description +
  verbatim OCR mixed) since the image *is* the document.
- :func:`parse_image_for_description` -- per-image-in-PDF call. Returns
  visual description only; OCR is the ETL service's job.
"""

from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock

import pytest

pytestmark = pytest.mark.unit


# ---------------------------------------------------------------------------
# parse_with_vision_llm: legacy single-shot path
# ---------------------------------------------------------------------------


async def test_parse_with_vision_llm_returns_combined_markdown(tmp_path):
    """Standalone image uploads still go through the combined-markdown path."""
    from app.etl_pipeline.parsers.vision_llm import parse_with_vision_llm

    img = tmp_path / "scan.png"
    img.write_bytes(b"\x89PNG\r\n\x1a\n" + b"\x00" * 200)

    fake_response = MagicMock()
    fake_response.content = "# A scan of something."
    fake_llm = AsyncMock()
    fake_llm.ainvoke.return_value = fake_response

    out = await parse_with_vision_llm(str(img), "scan.png", fake_llm)
    assert out == "# A scan of something."
    fake_llm.ainvoke.assert_awaited_once()


async def test_parse_with_vision_llm_rejects_empty_response(tmp_path):
    """An empty model response raises rather than silently returning blanks."""
    from app.etl_pipeline.parsers.vision_llm import parse_with_vision_llm

    img = tmp_path / "scan.png"
    img.write_bytes(b"\x89PNG\r\n\x1a\n" + b"\x00" * 200)

    fake_response = MagicMock()
    fake_response.content = ""
    fake_llm = AsyncMock()
    fake_llm.ainvoke.return_value = fake_response

    with pytest.raises(ValueError, match="empty content"):
        await parse_with_vision_llm(str(img), "scan.png", fake_llm)


# ---------------------------------------------------------------------------
# parse_image_for_description: per-image-in-PDF, description only
# ---------------------------------------------------------------------------


async def test_parse_image_for_description_returns_description(tmp_path):
    """Description-only path returns the model's markdown unchanged."""
    from app.etl_pipeline.parsers.vision_llm import parse_image_for_description

    img = tmp_path / "scan.png"
    img.write_bytes(b"\x89PNG\r\n\x1a\n" + b"\x00" * 200)

    fake_response = MagicMock()
    fake_response.content = "Axial CT showing a large cystic mass."
    fake_llm = AsyncMock()
    fake_llm.ainvoke.return_value = fake_response

    out = await parse_image_for_description(str(img), "scan.png", fake_llm)
    assert out == "Axial CT showing a large cystic mass."


async def test_parse_image_for_description_uses_description_only_prompt(tmp_path):
    """The prompt explicitly tells the model NOT to transcribe text.

    This is the contract that lets us drop OCR from the response: the
    ETL pipeline already has the text (from page-level OCR), so asking
    the vision LLM for it would be redundant cost.
    """
    from app.etl_pipeline.parsers.vision_llm import parse_image_for_description

    img = tmp_path / "scan.png"
    img.write_bytes(b"\x89PNG\r\n\x1a\n" + b"\x00" * 200)

    fake_response = MagicMock()
    fake_response.content = "A description"
    fake_llm = AsyncMock()
    fake_llm.ainvoke.return_value = fake_response

    await parse_image_for_description(str(img), "scan.png", fake_llm)

    # The prompt is the first text part of the message we sent.
    sent_messages = fake_llm.ainvoke.call_args.args[0]
    prompt_text = sent_messages[0].content[0]["text"].lower()
    assert "describe what this image visually depicts" in prompt_text
    assert "do not transcribe text" in prompt_text


async def test_parse_image_for_description_rejects_empty(tmp_path):
    """Empty response surfaces as ValueError so the caller can skip the image."""
    from app.etl_pipeline.parsers.vision_llm import parse_image_for_description

    img = tmp_path / "scan.png"
    img.write_bytes(b"\x89PNG\r\n\x1a\n" + b"\x00" * 200)

    fake_response = MagicMock()
    fake_response.content = "   "  # whitespace-only counts as empty
    fake_llm = AsyncMock()
    fake_llm.ainvoke.return_value = fake_response

    with pytest.raises(ValueError, match="empty content"):
        await parse_image_for_description(str(img), "scan.png", fake_llm)


# ---------------------------------------------------------------------------
# Image size + extension validation (shared by both paths)
# ---------------------------------------------------------------------------


def test_image_to_data_url_rejects_oversized(tmp_path):
    """Images larger than 5 MB raise before any LLM call is made."""
    from app.etl_pipeline.parsers.vision_llm import _image_to_data_url

    big = tmp_path / "huge.png"
    big.write_bytes(b"\x89PNG" + b"\x00" * (6 * 1024 * 1024))

    with pytest.raises(ValueError, match="Image too large"):
        _image_to_data_url(str(big))


def test_image_to_data_url_rejects_unsupported_extension(tmp_path):
    """Unknown extensions raise rather than guessing a MIME type."""
    from app.etl_pipeline.parsers.vision_llm import _image_to_data_url

    weird = tmp_path / "scan.xyz"
    weird.write_bytes(b"\x00" * 100)

    with pytest.raises(ValueError, match="Unsupported image extension"):
        _image_to_data_url(str(weird))


# ---------------------------------------------------------------------------
# Reasoning models reply with content blocks (DeepSeek: thinking + text)
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "content",
    [
        [
            {"type": "thinking", "thinking": "Let me look at the axes..."},
            {"type": "text", "text": "A bar chart of mortality by age."},
        ],
        [
            {"type": "reasoning", "reasoning": "hmm"},
            {"type": "text", "text": "A bar chart of mortality by age."},
        ],
        "  A bar chart of mortality by age.\n",
    ],
    ids=["thinking-blocks", "reasoning-blocks", "plain-string"],
)
async def test_block_content_is_read_as_its_text_blocks(tmp_path, content):
    from app.etl_pipeline.parsers.vision_llm import parse_image_for_description

    img = tmp_path / "fig.png"
    img.write_bytes(b"\x89PNG\r\n\x1a\n" + b"\x00" * 200)
    fake_llm = AsyncMock()
    fake_llm.ainvoke.return_value = MagicMock(content=content)

    out = await parse_image_for_description(str(img), "fig.png", fake_llm)

    assert out == "A bar chart of mortality by age."


async def test_blocks_with_only_thinking_count_as_empty(tmp_path):
    from app.etl_pipeline.parsers.vision_llm import parse_with_vision_llm

    img = tmp_path / "fig.png"
    img.write_bytes(b"\x89PNG\r\n\x1a\n" + b"\x00" * 200)
    fake_llm = AsyncMock()
    fake_llm.ainvoke.return_value = MagicMock(
        content=[{"type": "thinking", "thinking": "..."}]
    )

    with pytest.raises(ValueError, match="empty content"):
        await parse_with_vision_llm(str(img), "fig.png", fake_llm)


# ---------------------------------------------------------------------------
# Formats providers refuse (TIFF, BMP, JPEG 2000) are sent as PNG
# ---------------------------------------------------------------------------


def _decode_data_url(url: str):
    import base64
    import io

    from PIL import Image

    header, encoded = url.split(",", 1)
    return header, Image.open(io.BytesIO(base64.b64decode(encoded)))


@pytest.mark.parametrize(
    ("name", "pil_format", "mode"),
    [
        ("Im0.tiff", "TIFF", "1"),  # a CCITT-style bilevel scan
        ("Im1.tif", "TIFF", "CMYK"),
        ("Im2.bmp", "BMP", "RGB"),
        ("Im3.png", "JPEG2000", "RGB"),  # JPEG 2000 bytes behind a .png name
    ],
)
def test_non_web_images_are_converted_to_png(tmp_path, name, pil_format, mode):
    from PIL import Image

    from app.etl_pipeline.parsers.vision_llm import _image_to_data_url

    path = tmp_path / name
    Image.new(mode, (300, 200), 0 if mode == "1" else None).save(
        path, format=pil_format
    )

    header, image = _decode_data_url(_image_to_data_url(str(path)))

    assert header == "data:image/png;base64"
    assert image.format == "PNG"
    assert image.size == (300, 200)


def test_a_large_tiff_is_scaled_down_to_the_long_edge_limit(tmp_path):
    from PIL import Image

    from app.etl_pipeline.parsers.vision_image import MAX_LONG_EDGE, image_data_url

    path = tmp_path / "scan.tiff"
    Image.new("L", (5000, 2500), 128).save(path, format="TIFF")

    _, image = _decode_data_url(image_data_url(str(path)))

    assert max(image.size) == MAX_LONG_EDGE
    assert image.size == (MAX_LONG_EDGE, MAX_LONG_EDGE // 2)


def test_a_decompression_bomb_is_refused(tmp_path, monkeypatch):
    from PIL import Image

    from app.etl_pipeline.parsers import vision_image

    monkeypatch.setattr(vision_image, "MAX_PIXELS", 10_000)
    path = tmp_path / "bomb.tiff"
    Image.new("L", (200, 200), 0).save(path, format="TIFF")

    with pytest.raises(ValueError, match="too many pixels"):
        vision_image.image_data_url(str(path))


def test_web_images_are_sent_unchanged(tmp_path):
    import base64

    from PIL import Image

    from app.etl_pipeline.parsers.vision_image import image_data_url

    path = tmp_path / "photo.jpg"
    Image.new("RGB", (64, 64), (200, 10, 10)).save(path, format="JPEG")

    url = image_data_url(str(path))

    assert (
        url == "data:image/jpeg;base64," + base64.b64encode(path.read_bytes()).decode()
    )


def test_a_corrupt_tiff_is_refused_with_a_reason(tmp_path):
    from app.etl_pipeline.parsers.vision_image import image_data_url

    path = tmp_path / "broken.tiff"
    path.write_bytes(b"II*\x00garbage" * 50)

    with pytest.raises(ValueError, match="Unreadable image"):
        image_data_url(str(path))
