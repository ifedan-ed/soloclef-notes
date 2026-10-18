"""The stored choice's shape and which slot a notebook output belongs to."""

from __future__ import annotations

import pytest
from pydantic import ValidationError

from app.notebooks.studio.options import STUDIO_KINDS
from app.studio_models.choices import StudioChoice, StudioModelSettings
from app.studio_models.recommended import (
    DEEPSEEK_FLASH,
    LUNA,
    RECOMMENDED_DEFAULT,
    RECOMMENDED_OVERRIDES,
)
from app.studio_models.slots import STUDIO_SLOTS, slot_for

pytestmark = pytest.mark.unit


@pytest.mark.parametrize(
    ("kind", "options", "slot"),
    [
        ("report", {"format": "interactive", "file_type": "pdf"}, "report_interactive"),
        (
            "report",
            {"format": "interactive", "template_key": "learning_overview"},
            "report_interactive",
        ),
        ("report", {"format": "document", "file_type": "pdf"}, "report_pdf"),
        ("report", {"format": "document", "file_type": "docx"}, "report_docx"),
        ("report", {}, "report_pdf"),
        ("quiz", {"count": "fewer"}, "quiz"),
        ("video", {"format": "explainer"}, "video"),
        ("audio", {"format": "debate"}, "audio"),
        ("something_new", {}, None),
    ],
)
def test_slot_for_a_notebook_output(kind, options, slot):
    assert slot_for(kind, options) == slot


def test_every_studio_kind_has_a_slot():
    for kind in STUDIO_KINDS:
        assert slot_for(kind, {}) in STUDIO_SLOTS


def test_the_recommended_setup_is_the_measured_one():
    assert RECOMMENDED_DEFAULT == (LUNA, DEEPSEEK_FLASH)
    deepseek_first = {
        "slides",
        "mindmap",
        "flashcards",
        "data_table",
        "report_pdf",
        "report_docx",
        "report_interactive",
        "infographic",
    }
    assert {
        **dict.fromkeys(deepseek_first, (DEEPSEEK_FLASH, LUNA)),
        **dict.fromkeys(("audio", "video", "quiz"), (LUNA, DEEPSEEK_FLASH)),
    } == RECOMMENDED_OVERRIDES
    assert set(RECOMMENDED_OVERRIDES) == set(STUDIO_SLOTS)


@pytest.mark.parametrize(
    "bad",
    [
        {"mode": "model"},
        {"mode": "model", "primary": 3, "fallback": 3},
        {"mode": "auto", "primary": 3},
        {"mode": "recommended", "fallback": 2},
        {"mode": "sometimes"},
    ],
)
def test_a_malformed_choice_is_refused(bad):
    with pytest.raises(ValidationError):
        StudioChoice.model_validate(bad)


def test_a_zero_fallback_means_none():
    assert StudioChoice(mode="model", primary=3, fallback=0).fallback is None


def test_overrides_take_auto_or_a_model_for_a_known_type_only():
    StudioModelSettings.model_validate(
        {
            "default": {"mode": "auto"},
            "overrides": {
                "quiz": {"mode": "model", "primary": 3},
                "video": {"mode": "auto"},
            },
        }
    )
    for overrides in (
        {"quiz": {"mode": "recommended"}},
        {"podcast_ad": {"mode": "auto"}},
    ):
        with pytest.raises(ValidationError):
            StudioModelSettings.model_validate(
                {"default": {"mode": "auto"}, "overrides": overrides}
            )
