"""What "Fewer", "Standard" and "More" mean for flashcards and quizzes.

| Output     | Fewer | Standard | More |
|------------|-------|----------|------|
| Flashcards | 10    | 20       | 35   |
| Quiz       | 5     | 10       | 20   |

The prompt states the exact number. At save, more is trimmed to it and
fewer than 90% of it (never below the format's own minimum) is refused, so
the model adds the rest; see ``app.deliverables.artifact.item_count``.
"""

from __future__ import annotations

import math

from app.artifacts.verification.formats.flashcards import FLASHCARDS_MIN_CARDS
from app.artifacts.verification.formats.quiz import QUIZ_MIN_QUESTIONS
from app.deliverables.artifact.item_count import ItemCount

STUDY_COUNTS: dict[str, dict[str, int]] = {
    "flashcards": {"fewer": 10, "standard": 20, "more": 35},
    "quiz": {"fewer": 5, "standard": 10, "more": 20},
}
_FORMAT_MINIMUM = {"flashcards": FLASHCARDS_MIN_CARDS, "quiz": QUIZ_MIN_QUESTIONS}


def study_count(kind: str, size: str) -> ItemCount:
    target = STUDY_COUNTS[kind][size]
    minimum = max(_FORMAT_MINIMUM[kind], math.ceil(target * 0.9))
    return ItemCount(target=target, minimum=min(minimum, target))
