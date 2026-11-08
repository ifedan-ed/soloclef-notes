"""A podcast transcript sees its whole source, condensed by section if needed.

It used to see the first 12,000 characters, about a third of the P-value
paper, so misconceptions #5 to #12 never reached the script.
"""

from __future__ import annotations

from types import SimpleNamespace

import pytest
from langchain_core.messages import AIMessage

from app.deliverables.video.source_budget import CONDENSED_NOTE
from app.notebooks.studio.source_material import SourceDocument, compile_material
from app.podcasts.generation.transcript import nodes
from app.podcasts.generation.transcript.source_fit import (
    PODCAST_SOURCE_MAX_CHARS,
    podcast_source_block,
)
from app.podcasts.generation.transcript.state import TranscriptState
from tests.unit.deliverables.test_video_source_budget import _MISCONCEPTIONS, _paper

pytestmark = pytest.mark.unit


def _llm(max_input_tokens):
    return SimpleNamespace(profile={"max_input_tokens": max_input_tokens})


def _headings_kept(text: str) -> int:
    return sum(f"Misconception #{n}:" in text for n in range(1, 13))


def test_the_paper_is_longer_than_the_old_cut():
    assert len(_paper()) > 12_000


def test_a_large_context_model_gets_the_whole_paper():
    block = podcast_source_block(_paper(), _llm(1_000_000))

    assert _paper().strip() in block
    assert CONDENSED_NOTE not in block


def test_a_small_model_gets_every_section_condensed_and_is_told():
    block = podcast_source_block(_paper(), _llm(8_000))

    assert len(block) < len(_paper())
    assert _headings_kept(block) == 12
    assert block.endswith(CONDENSED_NOTE)


def test_a_huge_source_is_capped_for_every_call():
    huge = _paper() * 20
    block = podcast_source_block(huge, _llm(1_000_000))

    assert len(block) <= PODCAST_SOURCE_MAX_CHARS + len(CONDENSED_NOTE) + 100


async def test_the_outline_call_carries_the_whole_source(make_spec):
    seen = []

    class Recorder:
        profile = {"max_input_tokens": 1_000_000}

        async def ainvoke(self, messages):
            seen.append(messages)
            return AIMessage(
                content='{"segments": [{"title": "Open", "target_words": 200}]}'
            )

    config = {
        "configurable": {"workspace_id": 1, "spec": make_spec(), "llm": Recorder()}
    }
    state = TranscriptState(db_session=None, source_content=_paper())

    await nodes.plan_outline(state, config)

    human = seen[0][-1].content
    assert _headings_kept(human) == 12
    assert _MISCONCEPTIONS[-1] in human


def test_studio_material_condenses_a_long_source_instead_of_cutting_its_tail():
    documents = [
        SourceDocument(id=1, title="Paper", text=_paper()),
        SourceDocument(id=2, title="Note", text="A short note."),
    ]

    material = compile_material(documents, 12_000)

    assert len(material) <= 12_500
    assert _headings_kept(material) == 12
    assert "A short note." in material
