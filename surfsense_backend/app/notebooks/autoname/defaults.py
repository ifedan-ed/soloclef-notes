"""The default notebook title and the icons a notebook may be given.

The web and mobile apps create a notebook titled with their own translation of
"Untitled notebook" (``notebooks.untitled`` in each locale file), so every one
of those counts as the default. Keep in step with surfsense_web/messages/*.json
and surfsense_mobile/src/lib/i18n/messages/*.json; migration c0008 carries a
frozen copy for the notebooks that existed before automatic naming.
"""

from __future__ import annotations

DEFAULT_TITLE = "Untitled notebook"

DEFAULT_TITLES = frozenset(
    {
        DEFAULT_TITLE,  # en
        "Cuaderno sin título",  # es
        "Caderno sem título",  # pt
        "बिना शीर्षक की नोटबुक",  # hi
        "제목 없는 노트북",  # ko
        "未命名笔记本",  # zh
    }
)

# The customize dialog's emoji choices (NOTEBOOK_ICONS in the web and mobile
# apps); an automatic icon is always one of them.
NOTEBOOK_ICONS = (
    "📓",
    "📚",
    "🧪",
    "🧠",
    "🗂️",
    "🔬",
    "💡",
    "🧭",
    "🩺",
    "📊",
    "🌍",
    "✏️",
)


_FOLDED = frozenset(t.casefold() for t in DEFAULT_TITLES)


def is_default_title(title: str | None) -> bool:
    return " ".join((title or "").split()).casefold() in _FOLDED
