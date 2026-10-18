"""Deterministic fake for the quick-tasks model (``get_quick_llm``).

These notebook features ask the quick model for a short reply:

- chat follow-ups (``notebooks/chat/followups.py``): a JSON array of 3 questions;
- Studio and welcome-card suggestions (``notebooks/studio/suggestions``): a JSON
  object whose ideas sit under a per-kind field (``designs``, ``angles``, …) that
  the prompt shows in its sample reply;
- source guides (``notebooks/source_guide.py``): a paragraph plus bullets;
- a report's section suggestions (``notebooks/studio/report_view``): three
  suggestions under sections 1-3;
- automatic notebook names (``notebooks/autoname/naming.py``): a JSON object
  with a title and one of the picker's icons.

Each module binds ``get_quick_llm`` at import, so the patch goes on every
binding site. The replies carry an ``E2E`` marker so specs can assert them.
"""

from __future__ import annotations

import json
import re
from typing import Any

from langchain_core.messages import AIMessage, BaseMessage

FOLLOWUPS = [
    "E2E follow-up: what are the key terms?",
    "E2E follow-up: how strong is the evidence?",
    "E2E follow-up: what should I read next?",
]
IDEAS = [f"E2E idea {n}: a seeded angle on the sources" for n in range(1, 10)]
GUIDE = (
    "E2E source guide: this seeded source covers the canary topic used by the "
    "Playwright suite.\n\n- E2E key topic one\n- E2E key topic two\n- E2E key topic three"
)

_SAMPLE_KEY = re.compile(r'\{\s*"(?!examples")([a-z_]+)"\s*:\s*\[')


def _text(messages: list[BaseMessage]) -> str:
    parts = []
    for m in messages:
        content = m.content
        parts.append(content if isinstance(content, str) else json.dumps(content))
    return "\n".join(parts)


NOTEBOOK_TITLE = "E2E Named Notebook"
NOTEBOOK_ICON = "🔬"

REPORT_SUGGESTIONS = [
    {
        "section": 1,
        "kind": "flashcards",
        "title": "E2E cards: rounds",
        "focus": "E2E focus on the first section.",
    },
    {
        "section": 2,
        "kind": "quiz",
        "title": "E2E quiz: escalation",
        "focus": "E2E focus on the second section.",
    },
    {
        "section": 3,
        "kind": "mindmap",
        "title": "E2E map: summary",
        "focus": "E2E focus on the third section.",
    },
]


def reply_for(prompt: str) -> str:
    if '"report_suggestions"' in prompt:
        # The reading view's section suggestions (studio/report_view).
        return json.dumps({"report_suggestions": REPORT_SUGGESTIONS})
    if "Name a research notebook" in prompt:
        return json.dumps({"title": NOTEBOOK_TITLE, "icon": NOTEBOOK_ICON})
    if "follow-up questions" in prompt:
        return json.dumps(FOLLOWUPS)
    if "The user is about to create" in prompt or '"examples"' in prompt:
        match = _SAMPLE_KEY.search(prompt)
        key = match.group(1) if match else "ideas"
        return json.dumps({key: IDEAS, "examples": ["E2E example focus"]})
    if "source guide" in prompt:
        return GUIDE
    return "E2E quick reply"


class FakeQuickLLM:
    """Just enough of a chat model for ``ainvoke`` callers."""

    async def ainvoke(self, messages: list[BaseMessage], *args: Any, **kwargs: Any):
        return AIMessage(content=reply_for(_text(messages)))

    def invoke(self, messages: list[BaseMessage], *args: Any, **kwargs: Any):
        return AIMessage(content=reply_for(_text(messages)))

    def bind(self, **kwargs: Any) -> FakeQuickLLM:
        return self

    def with_config(self, *args: Any, **kwargs: Any) -> FakeQuickLLM:
        return self


async def fake_get_quick_llm(*args: Any, **kwargs: Any) -> FakeQuickLLM:
    return FakeQuickLLM()


def install(patches: list[Any]) -> None:
    from unittest.mock import patch

    for target in (
        "app.quick_tasks.llm.get_quick_llm",
        "app.notebooks.chat.followups.get_quick_llm",
        "app.notebooks.source_guide.get_quick_llm",
        "app.notebooks.studio.suggestions.generate.get_quick_llm",
        "app.notebooks.studio.report_view.generate.get_quick_llm",
        "app.notebooks.autoname.naming.get_quick_llm",
    ):
        try:
            p = patch(target, fake_get_quick_llm)
            p.start()
            patches.append(p)
        except (ModuleNotFoundError, AttributeError):
            continue
