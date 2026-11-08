"""How a video is assembled is one explicit setting, never a guess."""

from __future__ import annotations

import pytest

from app.config import config as app_config
from app.deliverables.video.render_mode import FRAMES, STILLS, render_mode

pytestmark = pytest.mark.unit


def test_the_two_modes_are_named_by_what_they_render() -> None:
    assert (FRAMES, STILLS) == ("frames", "stills")


@pytest.mark.parametrize("configured", ["frames", "stills", " STILLS ", "Frames"])
def test_a_named_mode_is_accepted_however_it_is_spelled(configured: str) -> None:
    assert render_mode(configured) in {FRAMES, STILLS}


@pytest.mark.parametrize("configured", ["", "  ", "hybrid", "still", "1", "true"])
def test_anything_else_is_refused_rather_than_guessed(configured: str) -> None:
    with pytest.raises(ValueError, match="VIDEO_RENDER_MODE"):
        render_mode(configured)


def test_an_unset_setting_leaves_the_existing_path_in_place() -> None:
    assert render_mode(app_config.VIDEO_RENDER_MODE) == FRAMES
