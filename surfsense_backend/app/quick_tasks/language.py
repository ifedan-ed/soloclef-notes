"""The language a quick-task reply must be written in, and a check that it was.

The quick-tasks model (DeepSeek v4.1 Flash, thinking off) drifts into Chinese
when a prompt only says "in the document's language": the P-value paper's
source guide came back entirely in Chinese. Every quick-task prompt now names
the language outright: the user's UI locale when the request carries one,
else the language detected in the material, else English. A reply in the
wrong script (CJK for an English target, Latin for a Chinese one) is asked
for once more with a firmer instruction, and a second miss is not accepted.
"""

from __future__ import annotations

import logging
import re
import unicodedata
from collections.abc import Awaitable, Callable

logger = logging.getLogger(__name__)

DEFAULT_LANGUAGE = "en"

LANGUAGE_NAMES = {
    "en": "English",
    "es": "Spanish",
    "pt": "Portuguese",
    "hi": "Hindi",
    "zh": "Chinese",
    "ko": "Korean",
    "ja": "Japanese",
    "fr": "French",
    "de": "German",
    "it": "Italian",
    "nl": "Dutch",
    "ru": "Russian",
    "uk": "Ukrainian",
    "ar": "Arabic",
    "tr": "Turkish",
    "pl": "Polish",
    "sv": "Swedish",
    "vi": "Vietnamese",
}

# The script a language is written in; anything unlisted is Latin.
_SCRIPT = {
    "zh": "cjk",
    "ja": "cjk",
    "ko": "hangul",
    "hi": "devanagari",
    "ru": "cyrillic",
    "uk": "cyrillic",
    "ar": "arabic",
}
_TAG = re.compile(r"^([A-Za-z]{2,3})(?:[-_][A-Za-z0-9]{1,8})*$")
# Fewer letters than this cannot be told apart by language...
_MIN_LETTERS = 12
# ...but even a short title in the wrong script can.
_MIN_SCRIPT_LETTERS = 4


class LanguageMismatchError(ValueError):
    """The model answered in another language twice."""


def language_code(value: str | None) -> str | None:
    """``pt-BR`` -> ``pt``; ``None`` for anything that is not a language tag."""
    match = _TAG.match((value or "").strip())
    return match.group(1).lower() if match else None


def language_name(code: str) -> str:
    return LANGUAGE_NAMES.get(code, code)


def detect_language(text: str | None) -> str | None:
    """The main language of ``text``, or None when it cannot tell."""
    sample = (text or "").strip()[:5000]
    if sum(ch.isalpha() for ch in sample) < _MIN_LETTERS:
        return None
    script = _dominant_script(sample)
    if script == "cjk":
        return "ja" if _has_kana(sample) else "zh"
    if script == "hangul":
        return "ko"
    if script == "devanagari":
        return "hi"
    if script == "arabic":
        return "ar"
    try:
        from langdetect import DetectorFactory, detect

        DetectorFactory.seed = 0
        detected = language_code(detect(sample))
    except Exception:
        detected = None
    if script == "cyrillic":
        return detected if detected in {"ru", "uk"} else "ru"
    return detected or DEFAULT_LANGUAGE


def target_language(*, locale: str | None = None, material: str | None = None) -> str:
    """The UI locale if given, else the material's language, else English."""
    return language_code(locale) or detect_language(material) or DEFAULT_LANGUAGE


def language_instruction(code: str) -> str:
    name = language_name(code)
    return (
        f"Write your entire reply in {name}, whatever language the material is "
        f"in. Do not switch to any other language."
    )


def retry_instruction(code: str) -> str:
    name = language_name(code)
    return (
        f"Your previous reply was not in {name}. Reply again, in {name} only: "
        f"every sentence, heading and item in {name}."
    )


def _letter_script(ch: str) -> str | None:
    if not ch.isalpha():
        return None
    name = unicodedata.name(ch, "")
    if name.startswith(("CJK", "HIRAGANA", "KATAKANA")):
        return "cjk"
    if name.startswith("HANGUL"):
        return "hangul"
    if name.startswith("DEVANAGARI"):
        return "devanagari"
    if name.startswith("CYRILLIC"):
        return "cyrillic"
    if name.startswith("ARABIC"):
        return "arabic"
    return "latin"


def _script_counts(text: str) -> dict[str, int]:
    counts: dict[str, int] = {}
    for ch in text:
        script = _letter_script(ch)
        if script is not None:
            counts[script] = counts.get(script, 0) + 1
    return counts


def _dominant_script(text: str) -> str:
    counts = _script_counts(text)
    # CJK characters carry a word each; weigh them against Latin letters.
    weighted = {s: n * (3 if s in {"cjk", "hangul"} else 1) for s, n in counts.items()}
    return max(weighted, key=weighted.get) if weighted else "latin"


def _has_kana(text: str) -> bool:
    return any(
        unicodedata.name(ch, "").startswith(("HIRAGANA", "KATAKANA")) for ch in text
    )


def reply_in_language(text: str, code: str) -> bool:
    """Whether ``text`` is written in ``code``'s script (a few foreign terms are fine)."""
    counts = _script_counts(text)
    total = sum(counts.values())
    if total < _MIN_SCRIPT_LETTERS:
        return True
    expected = _SCRIPT.get(code, "latin")
    if expected == "latin":
        foreign = sum(n for s, n in counts.items() if s != "latin")
        return foreign / total < 0.1
    return counts.get(expected, 0) / total >= 0.3


async def in_language[T](
    ask: Callable[[str | None], Awaitable[T]],
    text_of: Callable[[T], str],
    code: str,
) -> T:
    """``ask()``'s result, asked once more if it came back in another language.

    ``ask`` takes an extra instruction for the retry (None the first time).
    """
    first = await ask(None)
    if reply_in_language(text_of(first), code):
        return first
    logger.info("Quick-task reply was not in %s; asking once more", code)
    second = await ask(retry_instruction(code))
    if reply_in_language(text_of(second), code):
        return second
    raise LanguageMismatchError(f"the reply was not in {language_name(code)}")
