"""The outline a model plans before any dialogue is drafted."""

from __future__ import annotations

import json
import re

import pytest

from app.podcasts.generation.prompts import plan_outline_prompt
from app.podcasts.generation.transcript.planning import Outline

pytestmark = pytest.mark.unit


@pytest.mark.parametrize(("given", "kept"), [(0, 1), (-40, 1), (0.4, 1), (350, 350)])
def test_segment_size_is_clamped_to_at_least_one_word(given, kept):
    outline = Outline.model_validate(
        {"segments": [{"title": "Opening", "target_words": given}]}
    )

    assert outline.segments[0].target_words == kept


def test_prompt_example_is_a_valid_outline_with_a_realistic_size(make_spec):
    prompt = plan_outline_prompt(
        spec=make_spec(), target_words=3000, suggested_segments=6, focus=None
    )

    example = re.search(r'\{"segments".*\}', prompt, re.DOTALL).group(0)
    outline = Outline.model_validate(json.loads(example.replace("\\\n", "")))
    assert outline.segments[0].target_words == 500
