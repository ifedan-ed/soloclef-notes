"""The podcast source as each transcript call sees it: whole, or condensed by section.

The transcript used to see only the first 12,000 characters, about a third of
a typical paper, so the rest of an episode came from the model's own
knowledge. It now gets the whole source when it fits the model's budget (the
same one a video's author gets), and otherwise the section-aware
condensation video uses, with the note that says so. The source travels with
the outline call and every segment call, so the podcast budget is capped
lower than a video's: 30k tokens a call.
"""

from __future__ import annotations

from app.deliverables.video.source_budget import (
    CHARS_PER_TOKEN,
    CONDENSED_NOTE,
    fit_source,
    source_budget_chars,
)

PODCAST_SOURCE_MAX_CHARS = 30_000 * CHARS_PER_TOKEN


def podcast_source_block(source_content: str | None, llm) -> str:
    profile = getattr(llm, "profile", None)
    max_input_tokens = (
        profile.get("max_input_tokens") if isinstance(profile, dict) else None
    )
    budget = min(source_budget_chars(max_input_tokens), PODCAST_SOURCE_MAX_CHARS)
    fitted = fit_source(source_content or "", budget)
    block = f"<source_content>{fitted.text}</source_content>"
    if fitted.condensed:
        block += f"\n\n{CONDENSED_NOTE}"
    return block
