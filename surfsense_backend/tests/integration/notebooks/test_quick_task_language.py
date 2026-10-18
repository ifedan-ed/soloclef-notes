"""Source guides, Studio ideas and follow-ups come back in the language asked for.

The P-value paper's source guide came back in Chinese from the quick-tasks
model. The model is faked (an external boundary) with one that drifts into
Chinese on the first call; the cache and routes are real.
"""

from __future__ import annotations

import pytest
from langchain_core.messages import AIMessage
from sqlalchemy import select

from app.notebooks import source_guide as guides
from app.notebooks.chat import followups
from app.notebooks.persistence import SourceGuide
from app.notebooks.studio.source_material import SourceDocument
from app.notebooks.studio.suggestions import (
    SuggestionPool,
    focus_suggestions,
    generate as suggestion_generate,
)
from app.notebooks.studio.suggestions.pool import pool_key, store_pool
from app.notebooks.studio.suggestions.service import suggestions_key

pytestmark = pytest.mark.integration

PAPER = (
    "# A Dirty Dozen: Twelve P-Value Misconceptions\n\n"
    "The P value is a measure of statistical evidence. A nonsignificant "
    "difference does not mean there is no difference between groups. " * 5
)
CHINESE = "这是一份关于P值的来源指南。它涵盖了十二个常见的误解和统计证据。"
ENGLISH = "A source guide to the P-value paper.\n\n- Twelve misconceptions"


class _Drifting:
    """A quick model that answers in Chinese first, then as asked."""

    def __init__(self, replies):
        self.replies = list(replies)
        self.prompts: list[str] = []

    async def ainvoke(self, messages):
        self.prompts.append(messages[0].content)
        return AIMessage(content=self.replies.pop(0))


@pytest.fixture
def quick(monkeypatch):
    def install(module, replies):
        model = _Drifting(replies)

        async def get_quick_llm(_session, _workspace_id):
            return model

        monkeypatch.setattr(module, "get_quick_llm", get_quick_llm)
        return model

    return install


async def test_a_guide_names_its_language_and_retries_a_chinese_reply(
    db_session, make_owner, make_document, quick
):
    owner = await make_owner()
    document = await make_document(owner.workspace.id, "P values", PAPER)
    model = quick(guides, [CHINESE, ENGLISH])

    summary = await guides.source_guide(db_session, document)

    assert summary == ENGLISH
    assert "Write your entire reply in English" in model.prompts[0]
    assert "not in English" in model.prompts[1]


async def test_a_cached_chinese_guide_for_an_english_paper_is_regenerated(
    db_session, make_owner, make_document, quick
):
    owner = await make_owner()
    document = await make_document(owner.workspace.id, "P values", PAPER)
    db_session.add(
        SourceGuide(
            document_id=document.id,
            content_hash=document.content_hash,
            summary=CHINESE,
        )
    )
    await db_session.flush()
    quick(guides, [ENGLISH])

    assert await guides.source_guide(db_session, document) == ENGLISH
    stored = await db_session.scalar(
        select(SourceGuide).where(SourceGuide.document_id == document.id)
    )
    assert stored.summary == ENGLISH


async def test_the_ui_locale_sets_the_guide_language(
    db_session, make_owner, make_document, quick
):
    owner = await make_owner()
    document = await make_document(owner.workspace.id, "P values", PAPER)
    spanish = "Una guía del artículo sobre valores p y sus doce errores comunes."
    model = quick(guides, [spanish])

    assert await guides.source_guide(db_session, document, locale="es") == spanish
    assert "Write your entire reply in Spanish" in model.prompts[0]


async def test_two_chinese_guides_are_not_cached(
    db_session, make_owner, make_document, quick
):
    owner = await make_owner()
    document = await make_document(owner.workspace.id, "P values", PAPER)
    quick(guides, [CHINESE, CHINESE])

    assert await guides.source_guide(db_session, document) is None
    assert (
        await db_session.scalar(
            select(SourceGuide).where(SourceGuide.document_id == document.id)
        )
        is None
    )


async def test_studio_ideas_are_retried_and_a_chinese_pool_is_replaced(
    db_session, make_owner, make_notebook, quick, monkeypatch
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    documents = [SourceDocument(id=1, title="P values", text=PAPER)]
    chinese_pool = {
        "targets": [f"误解{n}的统计意义" for n in range(9)],
        "examples": ["解释P值"],
    }
    english_pool = {
        "targets": [f"Misconception {n} and what P really measures" for n in range(9)],
        "examples": ["Explain what a P value is"],
    }
    import json

    model = quick(
        suggestion_generate,
        [json.dumps(chinese_pool), json.dumps(english_pool)],
    )
    # A pool cached in Chinese before this fix.
    store_pool(
        notebook,
        suggestions_key([1]),
        pool_key("quiz", None, None),
        SuggestionPool(
            ideas=chinese_pool["targets"], examples=chinese_pool["examples"]
        ),
    )

    result = await focus_suggestions(db_session, notebook, documents, kind="quiz")

    assert result.pool[0].startswith("Misconception")
    assert "in English" in model.prompts[0]
    assert "not in English" in model.prompts[1]


async def test_followups_name_the_answer_language_and_retry(
    db_session, make_owner, quick
):
    owner = await make_owner()
    model = quick(
        followups,
        [
            '["为什么P值不是概率?", "什么是效应量?", "如何报告?"]',
            '["Why is P not a probability?", "What is an effect size?", "How should P be reported?"]',
        ],
    )

    questions = await followups.followup_questions(
        db_session,
        workspace_id=owner.workspace.id,
        answer="The P value is not the probability that the null is true.",
        source_titles=["P values"],
    )

    assert questions[0] == "Why is P not a probability?"
    assert "Write your entire reply in English" in model.prompts[0]
