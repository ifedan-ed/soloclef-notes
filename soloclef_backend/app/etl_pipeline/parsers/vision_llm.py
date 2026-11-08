import asyncio
import time

from langchain_core.messages import HumanMessage

from app.etl_pipeline.parsers.vision_image import image_data_url
from app.observability.domains import agent
from app.utils.content_utils import extract_text_content


def reply_text(content) -> str:
    """A vision reply's text, whether it came as a string or as content blocks."""
    if isinstance(content, str | list | dict):
        return extract_text_content(content) or ""
    return str(content or "")


# Single-shot prompt used by standalone image uploads (.png/.jpg/etc).
# A standalone image IS the document, so we want everything: visual
# content plus any text the model can read off it. The output is
# combined markdown that the chunker treats as the full document body.
_PROMPT = (
    "Describe this image in markdown. "
    "Transcribe any visible text verbatim. "
    "Be concise but complete — let the image content guide the level of detail."
)

# Per-image-in-PDF prompt. Here the image is *inside* a larger
# document, and the ETL service (pymupdf4llm/Azure DI/LlamaCloud/...) is
# already running OCR over the whole page — including text rendered
# into images. So we explicitly tell the model NOT to transcribe text
# and to focus only on visual interpretation. This avoids paying
# output tokens for OCR content the ETL pipeline already captured.
_DESCRIPTION_PROMPT = (
    "Describe what this image visually depicts in concise markdown. "
    "Focus on visual content — anatomy, structures, charts, diagrams, "
    "spatial relationships, colors, modality (e.g. axial CT, ECG strip, "
    "histology slide), and any clinically or structurally relevant "
    "findings.\n\n"
    "Do NOT transcribe text from the image. Any text in the image "
    "(axis labels, annotations, scale bars, lab values, etc.) is "
    "already extracted by a separate OCR pipeline; duplicating it "
    "here would be redundant. Stick to the visual interpretation."
)

_INVOKE_TIMEOUT_SECONDS = 120


def _image_to_data_url(file_path: str) -> str:
    return image_data_url(file_path)


async def _invoke_vision(llm, prompt: str, data_url: str, filename: str) -> str:
    message = HumanMessage(
        content=[
            {"type": "text", "text": prompt},
            {"type": "image_url", "image_url": {"url": data_url}},
        ]
    )
    model_id = getattr(llm, "model", None)
    t0 = time.perf_counter()
    with agent.model_call_span(model_id=model_id):
        try:
            response = await asyncio.wait_for(
                llm.ainvoke([message]), timeout=_INVOKE_TIMEOUT_SECONDS
            )
        finally:
            agent.record_model_call_duration(
                (time.perf_counter() - t0) * 1000, model=model_id, provider=None
            )
    # Reasoning models (DeepSeek, Anthropic) reply with a list of blocks,
    # thinking first; only the text blocks are the description.
    text = reply_text(response.content if hasattr(response, "content") else response)
    if not text.strip():
        raise ValueError(f"Vision LLM returned empty content for {filename}")
    return text.strip()


async def parse_with_vision_llm(file_path: str, filename: str, llm) -> str:
    """Single-shot: returns combined markdown for a standalone image upload.

    Used when the operator uploads an image file directly (jpg/png/etc).
    The image is the document, so the prompt asks for both visual
    description and verbatim text in one go.
    """
    data_url = _image_to_data_url(file_path)
    return await _invoke_vision(llm, _PROMPT, data_url, filename)


async def parse_image_for_description(file_path: str, filename: str, llm) -> str:
    """Visual-description-only call for per-image-in-PDF use.

    Used by ``picture_describer`` when an image is embedded inside a
    larger document. Returns a markdown description of what the image
    visually depicts; deliberately does NOT include text-in-image OCR
    because the ETL service (pymupdf4llm, Azure DI, LlamaCloud, ...) is
    already running OCR over the entire page and would duplicate that
    text content.
    """
    data_url = _image_to_data_url(file_path)
    return await _invoke_vision(llm, _DESCRIPTION_PROMPT, data_url, filename)


__all__ = [
    "parse_image_for_description",
    "parse_with_vision_llm",
]
