"""An image as a data URL a vision provider will accept.

OpenAI, DeepSeek and Anthropic take PNG, JPEG, GIF and WebP only, and
refuse anything else with a 400. PDFs routinely embed TIFF (CCITT scans),
JPEG 2000 and BMP images, so those are decoded and re-encoded as PNG, scaled
down to a size a provider reads well. The format is read from the bytes, not
the file name: pypdf names a JPEG 2000 image ``.jp2`` and the describer used
to label it ``.png``.
"""

from __future__ import annotations

import base64
import io
import os

# Anthropic's per-image limit, the strictest of the major providers.
MAX_SENT_BYTES = 5 * 1024 * 1024
# A raw TIFF or BMP is uncompressed, so it may be large and still shrink
# to well under the sent limit once re-encoded.
MAX_CONVERTIBLE_BYTES = 40 * 1024 * 1024
# Providers downscale past about 2000 px on the long edge anyway.
MAX_LONG_EDGE = 2048
# Refuse decompression bombs before decoding them.
MAX_PIXELS = 100_000_000

# Extensions of formats worth decoding and re-encoding as PNG.
_CONVERTIBLE_EXT = {
    ".tif",
    ".tiff",
    ".bmp",
    ".jp2",
    ".jpx",
    ".j2k",
    ".png",
    ".jpg",
    ".jpeg",
    ".gif",
    ".webp",
}

_WEB_EXT = {
    ".png": "image/png",
    ".jpg": "image/jpeg",
    ".jpeg": "image/jpeg",
    ".gif": "image/gif",
    ".webp": "image/webp",
}

# Formats Pillow cannot decode here; passed through by extension as before.
_PASSTHROUGH_EXT = {
    ".svg": "image/svg+xml",
    ".heic": "image/heic",
    ".heif": "image/heif",
}


class VisionImageError(ValueError):
    """The image cannot be sent to a vision model."""


def _to_png(data: bytes, filename: str) -> bytes:
    from PIL import Image

    try:
        with Image.open(io.BytesIO(data)) as image:
            if image.width * image.height > MAX_PIXELS:
                raise VisionImageError(
                    f"Image has too many pixels ({image.width}x{image.height}): {filename}"
                )
            image.seek(0)
            frame = image.convert("RGBA" if _has_alpha(image) else "RGB")
    except VisionImageError:
        raise
    except Exception as exc:
        raise VisionImageError(f"Unreadable image {filename}: {exc}") from exc

    long_edge = MAX_LONG_EDGE
    while True:
        frame.thumbnail((long_edge, long_edge))
        out = io.BytesIO()
        frame.save(out, format="PNG", optimize=True)
        if out.tell() <= MAX_SENT_BYTES or long_edge <= 256:
            break
        long_edge //= 2
    if out.tell() > MAX_SENT_BYTES:
        raise VisionImageError(f"Image stays over 5 MB as PNG: {filename}")
    return out.getvalue()


def _has_alpha(image) -> bool:
    return image.mode in {"RGBA", "LA", "PA"} or "transparency" in image.info


def _web_format(data: bytes) -> str | None:
    """The mime type when the bytes are already a format providers accept."""
    if data.startswith(b"\x89PNG\r\n\x1a\n"):
        return "image/png"
    if data.startswith(b"\xff\xd8\xff"):
        return "image/jpeg"
    if data[:6] in (b"GIF87a", b"GIF89a"):
        return "image/gif"
    if data[:4] == b"RIFF" and data[8:12] == b"WEBP":
        return "image/webp"
    return None


def is_web_format(data: bytes) -> bool:
    return _web_format(data[:16]) is not None


def image_data_url(file_path: str) -> str:
    """A ``data:`` URL for the image at ``file_path``, converted if needed."""
    filename = os.path.basename(file_path)
    size = os.path.getsize(file_path)
    ext = os.path.splitext(file_path)[1].lower()
    if ext in _PASSTHROUGH_EXT:
        if size > MAX_SENT_BYTES:
            raise VisionImageError(_too_large(size, filename))
        mime, data = _PASSTHROUGH_EXT[ext], _read(file_path)
    else:
        with open(file_path, "rb") as f:
            head = f.read(16)
        mime = _web_format(head) or _web_mime_if_undecodable(ext, file_path)
        if mime is not None:
            if size > MAX_SENT_BYTES:
                raise VisionImageError(_too_large(size, filename))
            data = _read(file_path)
        elif size > MAX_CONVERTIBLE_BYTES:
            raise VisionImageError(_too_large(size, filename))
        elif ext not in _CONVERTIBLE_EXT and not _decodable(file_path):
            raise VisionImageError(f"Unsupported image extension {ext!r}: {filename}")
        else:
            mime, data = "image/png", _to_png(_read(file_path), filename)
    return f"data:{mime};base64,{base64.b64encode(data).decode('ascii')}"


def _web_mime_if_undecodable(ext: str, file_path: str) -> str | None:
    """A web-named file Pillow cannot read is sent as named, as it always was."""
    if ext in _WEB_EXT and not _decodable(file_path):
        return _WEB_EXT[ext]
    return None


def _decodable(file_path: str) -> bool:
    from PIL import Image

    try:
        with Image.open(file_path):
            return True
    except Exception:
        return False


def _read(file_path: str) -> bytes:
    with open(file_path, "rb") as f:
        return f.read()


def _too_large(size: int, filename: str) -> str:
    return (
        f"Image too large for vision LLM ({size / (1024 * 1024):.1f} MB, "
        f"limit {MAX_SENT_BYTES // (1024 * 1024)} MB): {filename}"
    )
