"""The recommended setup a workspace starts on, from the measured comparison.

Owner-approved on 2026-09-23. GPT-5.6 Luna passed the video script,
interactive report and quiz runs with no failures, was fastest and cost about
a cent a run; DeepSeek v4.1 Flash wrote the best interactive report and is
cheap but slower. So Luna is the default with DeepSeek behind it, and the
interactive report swaps the two.

Updated 2026-09-24 after every output type was measured: DeepSeek leads
for documents and visuals (slides, mind map, flashcards, data table, the
three reports, infographic), Luna for audio, video and quiz.
Models are named by their provider model id and matched against what the
workspace can reach, so nothing breaks where they are not configured.
"""

from __future__ import annotations

LUNA = "openai-gpt-5.6-luna"
DEEPSEEK_FLASH = "ds-deepseek-v4.1-flash"

RECOMMENDED_DEFAULT: tuple[str, str | None] = (LUNA, DEEPSEEK_FLASH)

# slot -> (primary, fallback)
_DEEPSEEK_FIRST = (
    "slides",
    "mindmap",
    "flashcards",
    "data_table",
    "report_pdf",
    "report_docx",
    "report_interactive",
    "infographic",
)
_LUNA_FIRST = ("audio", "video", "quiz")

RECOMMENDED_OVERRIDES: dict[str, tuple[str, str | None]] = {
    **dict.fromkeys(_DEEPSEEK_FIRST, (DEEPSEEK_FLASH, LUNA)),
    **dict.fromkeys(_LUNA_FIRST, (LUNA, DEEPSEEK_FLASH)),
}
