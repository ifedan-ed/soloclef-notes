"""How much narration fits in a video, expressed in words an author can count.

The author and shortening prompts write words, never seconds; the TTS provider
turns them into seconds. These constants translate the policy's duration limit
into a word budget before any paid synthesis, and translate a measured overrun
back into a word target afterwards.
"""

from __future__ import annotations

import math
from typing import Final

from app.deliverables.jobs.policy import VIDEO_SPEC

# Measured on 2026-09-23 against the deployed TTS (openai tts-1-hd, voice
# "alloy"): 134 words of report-style prose synthesized to 53.2 s, 2.52 w/s.
# Rounded down so pauses at scene boundaries and slower voices stay inside it.
# Rechecked 2026-09-24 for gpt-4o-mini-tts, voice "coral", with the default
# "warm, clear, unhurried medical lecturer" instruction: 108 words in 38.2 s,
# 2.83 w/s, so the budget stays conservative.
NARRATION_WORDS_PER_SECOND: Final = 2.4
# Headroom so a draft exactly on budget still lands under the hard limit.
NARRATION_BUDGET_SAFETY_FACTOR: Final = 0.9
# After a measured overrun, aim this far below the limit, not at it.
NARRATION_OVERRUN_TARGET_RATIO: Final = 0.95


def narration_word_budget(max_seconds: int = VIDEO_SPEC.max_duration_seconds) -> int:
    """Words of narration an author may write for a ``max_seconds`` video."""
    return math.floor(
        max_seconds * NARRATION_WORDS_PER_SECOND * NARRATION_BUDGET_SAFETY_FACTOR
    )


def count_narration_words(transcripts: list[str]) -> int:
    """Count spoken words across every scene transcript."""
    return sum(len(transcript.split()) for transcript in transcripts)


def overrun_word_target(
    current_words: int,
    measured_seconds: float,
    max_seconds: int = VIDEO_SPEC.max_duration_seconds,
) -> int:
    """Scale the current word count to land at the target share of the limit."""
    ratio = (max_seconds * NARRATION_OVERRUN_TARGET_RATIO) / measured_seconds
    return max(1, math.floor(current_words * ratio))
