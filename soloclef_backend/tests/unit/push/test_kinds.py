"""Every kind is on until the person turns it off; unknown keys never count."""

from __future__ import annotations

import pytest

from app.push.kinds import (
    KIND_NAMES,
    KINDS,
    default_choices,
    is_kind,
    merged_choices,
    wants,
)

pytestmark = pytest.mark.unit


def test_the_plans_events_each_have_a_kind_with_words_for_the_switch() -> None:
    assert set(KIND_NAMES) == {
        "answer",
        "approval",
        "output",
        "automation",
        "source",
        "mention",
        "announcement",
        "invite",
        "credits",
    }
    for kind in KINDS:
        assert kind.label and kind.description


def test_everything_is_on_by_default() -> None:
    assert default_choices() == dict.fromkeys(KIND_NAMES, True)
    assert all(wants(kind, None) for kind in KIND_NAMES)


def test_saved_choices_cover_the_defaults_and_junk_is_dropped() -> None:
    merged = merged_choices({"answer": False, "nope": False, "mention": "yes"})
    assert merged["answer"] is False
    assert merged["mention"] is True
    assert "nope" not in merged
    assert wants("answer", {"answer": False}) is False
    assert wants("output", {"answer": False}) is True


def test_is_kind() -> None:
    assert is_kind("answer")
    assert not is_kind("Answer")
    assert not is_kind(None)
    assert not is_kind(3)
