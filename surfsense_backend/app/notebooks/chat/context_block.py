"""The per-turn note telling the agent it is working inside a notebook.

It rides in the turn's context block rather than the system prompt: the
compiled agent and its system prompt are cached across threads (and prompt-
cached by the provider), so anything that varies per notebook belongs with the
turn, the same place @-mentions and referenced chats already go.
"""

from __future__ import annotations

from .pins import MAX_LISTED_SOURCES, NotebookPins
from .settings import render_chat_settings


def _clean(text: str) -> str:
    return " ".join(text.split())[:200]


def render_notebook_context(pins: NotebookPins | None) -> str | None:
    if pins is None:
        return None
    lines = [
        "<notebook_context>",
        f'This conversation belongs to the notebook "{_clean(pins.title)}".',
    ]
    if pins.is_empty:
        lines.append(
            "The notebook has no enabled sources yet, so there is nothing to "
            "search or read: the knowledge base is limited to the notebook's "
            "sources. Unless you already said so earlier in this "
            "conversation, say so briefly at the start of your answer (for "
            "example: I don't see any sources in this notebook yet), then "
            "offer next steps such as adding files or websites as sources, "
            "finding web pages to add with the notebook's \"Search the web "
            'for new sources" box, or ticking sources that are already in '
            "the notebook."
        )
    else:
        lines.append(
            "Answer from its enabled sources: search_knowledge_base and the "
            "/documents files reach only these, and nothing else in the "
            "workspace (other notebooks' sources, Studio outputs) is "
            "available. Ground answers in them and cite them with [n]. If the "
            "sources do not answer the question, say so and suggest adding "
            "sources; do not search the web or offer to. Anything a tool the "
            "user picked for this message returns is from the web, not one of "
            "these sources: say so and never cite it as a source."
        )
        titles = pins.source_titles[:MAX_LISTED_SOURCES]
        lines.append("Enabled sources:")
        lines.extend(f"- {_clean(title)}" for title in titles)
        hidden = len(pins.source_titles) - len(titles)
        if hidden > 0:
            lines.append(f"- … and {hidden} more")
    lines.extend(render_chat_settings(pins.settings))
    lines.append("</notebook_context>")
    return "\n".join(lines)
