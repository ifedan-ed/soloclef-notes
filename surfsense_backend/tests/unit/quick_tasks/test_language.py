"""Quick-task replies are asked for in one named language and checked."""

from __future__ import annotations

import pytest

from app.quick_tasks.language import (
    LanguageMismatchError,
    detect_language,
    in_language,
    language_instruction,
    reply_in_language,
    target_language,
)

pytestmark = pytest.mark.unit

ENGLISH = (
    "The P value is a measure of statistical evidence. A nonsignificant "
    "difference does not mean there is no difference between groups."
)
SPANISH = (
    "El valor p es una medida de la evidencia estadística. Una diferencia no "
    "significativa no significa que no haya diferencia entre los grupos."
)
CHINESE = "P值是统计证据的一种度量。不显著的差异并不意味着各组之间没有差异。"
KOREAN = "P 값은 통계적 증거의 척도입니다. 유의하지 않은 차이가 차이가 없음을 의미하지는 않습니다."


@pytest.mark.parametrize(
    ("text", "code"),
    [
        (ENGLISH, "en"),
        (SPANISH, "es"),
        (CHINESE, "zh"),
        (KOREAN, "ko"),
        ("P < .05", None),
    ],
)
def test_the_material_language_is_detected(text, code):
    assert detect_language(text) == code


@pytest.mark.parametrize(
    ("locale", "material", "code"),
    [
        ("pt-BR", ENGLISH, "pt"),  # the UI locale wins
        (None, SPANISH, "es"),  # else the material's language
        (None, "", "en"),  # else English
        ("not a tag!", CHINESE, "zh"),
    ],
)
def test_the_target_language(locale, material, code):
    assert target_language(locale=locale, material=material) == code


def test_the_instruction_names_the_language():
    assert "Write your entire reply in English" in language_instruction("en")


@pytest.mark.parametrize(
    ("text", "code", "ok"),
    [
        (CHINESE, "en", False),
        (ENGLISH, "en", True),
        (
            "The ANOVA (方差分析) compares group means across several groups.",
            "en",
            True,
        ),
        (ENGLISH, "zh", False),
        (CHINESE, "zh", True),
        ("P值检验", "en", False),  # a short title in the wrong script
    ],
)
def test_the_reply_script_is_checked(text, code, ok):
    assert reply_in_language(text, code) is ok


async def test_a_reply_in_the_wrong_language_is_asked_for_once_more():
    replies = iter([CHINESE, ENGLISH])
    extras = []

    async def ask(extra):
        extras.append(extra)
        return next(replies)

    assert await in_language(ask, str, "en") == ENGLISH
    assert extras[0] is None and "not in English" in extras[1]


async def test_two_replies_in_the_wrong_language_are_not_accepted():
    async def ask(_extra):
        return CHINESE

    with pytest.raises(LanguageMismatchError):
        await in_language(ask, str, "en")


async def test_a_reply_in_the_right_language_is_asked_once():
    calls = []

    async def ask(extra):
        calls.append(extra)
        return ENGLISH

    await in_language(ask, str, "en")
    assert calls == [None]
