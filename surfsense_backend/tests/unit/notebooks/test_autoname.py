"""Automatic notebook naming: the title cleanup, the prompt's language, the policy."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta
from types import SimpleNamespace

import pytest
from langchain_core.messages import AIMessage

from app.notebooks.autoname import naming
from app.notebooks.autoname.defaults import NOTEBOOK_ICONS, is_default_title
from app.notebooks.autoname.naming import (
    SourceExcerpt,
    clean_icon,
    clean_title,
    humanize_filename,
    material_language,
    parse_reply,
    sources_material,
)
from app.notebooks.autoname.policy import (
    MAX_RUNS,
    REFINE_WINDOW,
    AutoTitleState,
    decide,
    next_state,
)

pytestmark = pytest.mark.unit

NOW = datetime(2026, 9, 25, 12, 0, tzinfo=UTC)


# --- title cleanup ---------------------------------------------------------


def test_filename_becomes_words():
    assert (
        humanize_filename("complications-of-premature-delivery.pptx")
        == "complications of premature delivery"
    )
    assert humanize_filename("Relationship_between_IQ.pdf") == "Relationship between IQ"
    # A real title with spaces keeps its punctuation.
    assert humanize_filename("A study. Part 2") == "A study. Part 2"


@pytest.mark.parametrize(
    ("raw", "expected"),
    [
        ("complications of premature delivery", "Complications of Premature Delivery"),
        ("Complications Of Premature Delivery", "Complications of Premature Delivery"),
        ("The Role Of The NICU", "The Role of the NICU"),
        (
            '"Respiratory problems in premature infants."',
            "Respiratory Problems in Premature Infants",
        ),
        ("Title: **Neonatal Sepsis** 🩺", "Neonatal Sepsis"),
        ("  COVID-19 and the NICU  ", "COVID-19 and the NICU"),
        ("“IQ and Birth Weight”", "IQ and Birth Weight"),
    ],
)
def test_clean_title_english(raw, expected):
    assert clean_title(raw, language="en") == expected


def test_clean_title_keeps_other_languages_case():
    assert (
        clean_title("Complicaciones del parto prematuro.", language="es")
        == "Complicaciones del parto prematuro"
    )
    assert clean_title("「早产并发症」", language="zh") == "早产并发症"


def test_clean_title_caps_length_at_a_word():
    title = clean_title("word " * 30, language="en")
    assert title is not None and len(title) <= naming.MAX_TITLE_CHARS
    assert not title.endswith(" ")
    assert title.split(" ")[-1] == "Word"


def test_clean_title_rejects_empty_and_default():
    assert clean_title("   ", language="en") is None
    assert clean_title("🩺🧠", language="en") is None
    assert clean_title("Untitled notebook", language="en") is None


def test_default_titles_in_every_locale():
    for title in (
        "Untitled notebook",
        "Cuaderno sin título",
        "Caderno sem título",
        "बिना शीर्षक की नोटबुक",
        "제목 없는 노트북",
        "未命名笔记本",
    ):
        assert is_default_title(title)
    assert not is_default_title("Untitled notebook 2")
    assert not is_default_title("My notebook")


def test_icon_must_be_a_picker_choice():
    assert clean_icon("🩺") == "🩺"
    assert clean_icon("🗂") == "🗂️"  # without its variation selector
    assert clean_icon("🦄") is None
    assert clean_icon(None) is None
    assert set(NOTEBOOK_ICONS) >= {"📓", "🩺"}


def test_parse_reply_json_and_plain():
    parsed = parse_reply(
        'Sure! {"title": "neonatal jaundice", "icon": "🩺"}', language="en"
    )
    assert parsed is not None
    assert (parsed.title, parsed.icon) == ("Neonatal Jaundice", "🩺")
    plain = parse_reply("Neonatal Jaundice Basics\nextra line", language="en")
    assert plain is not None and plain.title == "Neonatal Jaundice Basics"
    assert plain.icon is None
    assert parse_reply('{"title": ""}', language="en") is None


def test_material_uses_the_beginning_of_each_source():
    material = sources_material(
        [SourceExcerpt(title="a-file.pdf", text="Start here. " + "x" * 5000)]
    )
    assert "<name>a file</name>" in material
    assert "Start here." in material
    assert len(material) < 2000


def test_language_follows_the_material():
    spanish = SourceExcerpt(
        title="parto.pdf",
        text="El parto prematuro se asocia con complicaciones respiratorias y "
        "neurológicas en los recién nacidos, que requieren cuidados intensivos.",
    )
    assert material_language(sources=[spanish]) == "es"
    assert material_language(question="早产儿有哪些呼吸系统问题, 请详细说明") == "zh"
    assert (
        material_language(
            question="What respiratory problems are linked to premature birth?"
        )
        == "en"
    )


async def test_suggest_name_asks_in_the_material_language(monkeypatch):
    seen = {}

    class FakeLLM:
        async def ainvoke(self, messages):
            seen["system"] = messages[0].content
            return AIMessage(
                content='{"title": "Complicaciones del parto prematuro", "icon": "🩺"}'
            )

    async def fake_llm(session, workspace_id):
        return FakeLLM()

    monkeypatch.setattr(naming, "get_quick_llm", fake_llm)
    result = await naming.suggest_name(
        SimpleNamespace(), 1, material="<source/>", language="es"
    )
    assert result is not None and result.title == "Complicaciones del parto prematuro"
    assert "Spanish" in seen["system"]


# --- policy ------------------------------------------------------------------


def _state(source="auto", runs=0, at=None, sources=0):
    return AutoTitleState(title_source=source, runs=runs, at=at, sources=sources)


def test_user_renamed_never_auto_names_again():
    for trigger in ("source", "question", "backfill"):
        assert (
            decide(_state("user"), ready_sources=3, has_question=True, trigger=trigger)
            is None
        )
    # Even straight after a first automatic name, within the refine window.
    assert (
        decide(
            _state("user", runs=1, at=NOW, sources=1),
            ready_sources=4,
            has_question=False,
            trigger="source",
            now=NOW,
        )
        is None
    )


def test_first_name_prefers_ready_sources_over_the_question():
    assert (
        decide(_state(), ready_sources=1, has_question=True, trigger="question")
        == "sources"
    )
    assert (
        decide(_state(), ready_sources=0, has_question=True, trigger="question")
        == "question"
    )
    assert (
        decide(_state(), ready_sources=0, has_question=False, trigger="source") is None
    )


def test_one_refinement_when_more_sources_arrive_soon():
    named = _state(runs=1, at=NOW, sources=1)
    assert (
        decide(
            named,
            ready_sources=3,
            has_question=False,
            trigger="source",
            now=NOW + timedelta(minutes=2),
        )
        == "sources"
    )
    # No new sources, a question, or too late: no refinement.
    assert (
        decide(named, ready_sources=1, has_question=False, trigger="source", now=NOW)
        is None
    )
    assert (
        decide(named, ready_sources=3, has_question=True, trigger="question", now=NOW)
        is None
    )
    late = NOW + REFINE_WINDOW + timedelta(seconds=1)
    assert (
        decide(named, ready_sources=3, has_question=False, trigger="source", now=late)
        is None
    )
    # A question-named notebook is refined by its first ready source.
    from_question = _state(runs=1, at=NOW, sources=0)
    assert (
        decide(
            from_question,
            ready_sources=1,
            has_question=False,
            trigger="source",
            now=NOW,
        )
        == "sources"
    )


def test_never_more_than_max_runs():
    done = _state(runs=MAX_RUNS, at=NOW, sources=1)
    assert (
        decide(done, ready_sources=9, has_question=True, trigger="source", now=NOW)
        is None
    )


def test_state_round_trip():
    state = next_state(0, basis="sources", ready_sources=2, now=NOW)
    parsed = AutoTitleState.of("auto", state)
    assert (parsed.runs, parsed.sources, parsed.at) == (1, 2, NOW)
    assert next_state(1, basis="question", ready_sources=3, now=NOW)["sources"] == 0
    assert AutoTitleState.of(None, None) == AutoTitleState(title_source="user")
    assert AutoTitleState.of("auto", {"runs": "x", "at": "nope"}).runs == 0


def test_e2e_fake_model_answers_the_naming_prompt():
    from tests.e2e.fakes import quick_llm

    reply = quick_llm.reply_for(naming._PROMPT + "\n<source/>")
    parsed = parse_reply(reply, language="en")
    assert parsed is not None
    assert (parsed.title, parsed.icon) == (
        quick_llm.NOTEBOOK_TITLE,
        quick_llm.NOTEBOOK_ICON,
    )


def test_backfill_runs_as_a_module_in_a_fresh_interpreter():
    """``python -m app.notebooks.autoname.backfill`` must import on its own."""
    import subprocess
    import sys

    result = subprocess.run(
        [sys.executable, "-c", "import app.notebooks.autoname.backfill"],
        capture_output=True,
        text=True,
        timeout=300,
    )
    assert result.returncode == 0, result.stderr[-2000:]
