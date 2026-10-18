"""A video's source is given whole when it fits, and by section when not.

The P-value paper (Goodman, "A Dirty Dozen") lost misconceptions #6 to #12 to
a 12,000-character cut. The fixture mirrors its shape: twelve run-in bold
headings with long paragraphs, a table and a markdown heading.
"""

from __future__ import annotations

import json
import re
from types import SimpleNamespace

import pytest

from app.deliverables.video import executor
from app.deliverables.video.source_budget import (
    CONDENSED_NOTE,
    VIDEO_SOURCE_MAX_CHARS,
    fit_source,
    source_budget_chars,
)
from app.notebooks.studio.source_material import SourceDocument, whole_source_material

pytestmark = pytest.mark.unit

_MISCONCEPTIONS = [
    "If P=.05, the null hypothesis has only a 5% chance of being true.",
    "A nonsignificant difference means there is no difference between groups.",
    "A statistically significant finding is clinically important.",
    "Studies with P values on opposite sides of .05 are conflicting.",
    "Studies with the same P value provide the same evidence against the null.",
    "P = .05 means data that would occur only 5% of the time under the null.",
    "P = .05 and P <= .05 mean the same thing.",
    "P values are properly written as inequalities.",
    "P = .05 means a rejected null has only a 5% chance of a type I error.",
    "With a P = .05 threshold, the chance of a type I error will be 5%.",
    "Use a one-sided P value when one direction does not matter.",
    "A conclusion should be based on whether or not P is significant.",
]


def _paper() -> str:
    filler = (
        "The evidence from a study must be weighed against prior work, and the "
        "magnitude of the effect matters as much as its precision. "
    ) * 14
    parts = [
        "# A Dirty Dozen: Twelve P-Value Misconceptions",
        "**Steven Goodman**",
        "The P value is a measure of statistical evidence. " + filler,
        "| # | Misconception |\n| - | - |\n"
        + "\n".join(f"| {n} | {m} |" for n, m in enumerate(_MISCONCEPTIONS, 1)),
    ]
    for number, claim in enumerate(_MISCONCEPTIONS, 1):
        parts.append(f"**Misconception #{number}: {claim}** " + filler)
        parts.append("A further paragraph of discussion. " + filler)
    parts.append("## Conclusion\n\n" + filler)
    return "\n\n".join(parts)


def _headings_in(text: str) -> list[int]:
    return [n for n in range(1, 13) if re.search(rf"\*\*Misconception #{n}:", text)]


def test_full_text_is_kept_when_it_fits() -> None:
    paper = _paper()
    fitted = fit_source(paper, len(paper))
    assert fitted.text == paper.strip()
    assert not fitted.condensed


@pytest.mark.parametrize("budget", [30_000, 12_000, 6_000, 3_000])
def test_every_misconception_heading_survives_condensation(budget: int) -> None:
    paper = _paper()
    assert len(paper) > 40_000

    fitted = fit_source(paper, budget)

    assert fitted.condensed
    assert len(fitted.text) <= budget
    assert _headings_in(fitted.text) == list(range(1, 13))
    assert "## Conclusion" in fitted.text


def test_the_old_twelve_thousand_character_cut_lost_the_later_headings() -> None:
    assert _headings_in(_paper()[:12_000]) != list(range(1, 13))


def test_lists_and_tables_are_kept_while_there_is_room() -> None:
    text = "# Title\n\nIntro. " + "x " * 5_000 + "\n\n- first item\n- second item"
    text += "\n\n| a | b |\n| - | - |\n| 1 | 2 |"
    fitted = fit_source(text, 3_000)
    assert "- first item\n- second item" in fitted.text
    assert "| 1 | 2 |" in fitted.text


def test_budget_scales_with_the_model_context_up_to_sixty_thousand_tokens() -> None:
    assert source_budget_chars(1_048_576) == VIDEO_SOURCE_MAX_CHARS
    assert source_budget_chars(32_000) < source_budget_chars(128_000)
    assert source_budget_chars(None) == source_budget_chars(32_000)


def test_studio_sends_the_full_sources_with_their_line_breaks() -> None:
    source = whole_source_material(
        [SourceDocument(id=1, title="Dirty Dozen", text=_paper())]
    )
    assert not source.condensed
    assert source.text.startswith("## Source: Dirty Dozen\n\n# A Dirty Dozen")
    assert _headings_in(source.text) == list(range(1, 13))


class _CapturingLLM:
    def __init__(self, max_input_tokens: int) -> None:
        self.profile = {"max_input_tokens": max_input_tokens}
        self.calls: list = []

    async def ainvoke(self, messages):
        self.calls.append(list(messages))
        reply = {
            "scenes": [{"transcript": "Hi.", "on_screen_markdown": "Hi", "code": "x"}]
        }
        return SimpleNamespace(content=json.dumps(reply))


def _request(paper: str) -> executor.VideoJobRequestV1:
    return executor.VideoJobRequestV1.model_validate(
        {
            "version": 1,
            "brief": "Explain it.",
            "root_thread_id": 1,
            "source_material": paper,
        }
    )


@pytest.mark.parametrize(
    ("max_input_tokens", "condensed"), [(1_048_576, False), (8_000, True)]
)
async def test_author_brief_carries_every_heading_for_the_models_context(
    max_input_tokens: int, condensed: bool
) -> None:
    llm = _CapturingLLM(max_input_tokens)

    await executor._author_video(llm, "Dirty Dozen", _request(_paper()))

    sent = json.loads(llm.calls[0][1].content)
    assert _headings_in(sent["source_material"]) == list(range(1, 13))
    assert (sent.get("source_note") == CONDENSED_NOTE) is condensed
    assert len(sent["source_material"]) <= source_budget_chars(max_input_tokens)


async def test_a_brief_without_source_material_is_unchanged() -> None:
    llm = _CapturingLLM(1_048_576)
    request = executor.VideoJobRequestV1.model_validate(
        {"version": 1, "brief": "Explain it.", "root_thread_id": 1}
    )
    await executor._author_video(llm, "T", request)
    sent = json.loads(llm.calls[0][1].content)
    assert "source_material" not in sent and "source_note" not in sent
