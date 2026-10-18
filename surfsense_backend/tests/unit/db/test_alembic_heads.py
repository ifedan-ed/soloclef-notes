"""The migration history has exactly one head.

Branches that each add a local ``c00NN`` revision on the same parent merge
without a textual conflict, and the stack's ``alembic upgrade head`` then
stops on "multiple heads" at deploy time. This turns that into a test failure
at merge time. Fix it by pointing the later revision's ``down_revision`` at
the other one (custom/README.md, "Local migrations").
"""

from __future__ import annotations

from pathlib import Path

import pytest
from alembic.config import Config
from alembic.script import ScriptDirectory

pytestmark = pytest.mark.unit

_BACKEND = Path(__file__).resolve().parents[3]


def test_the_history_has_one_head():
    config = Config(str(_BACKEND / "alembic.ini"))
    config.set_main_option("script_location", str(_BACKEND / "alembic"))
    heads = ScriptDirectory.from_config(config).get_heads()
    assert len(heads) == 1, (
        f"Alembic has {len(heads)} heads: {sorted(heads)}. Point the newer "
        "local revision's down_revision at the other head."
    )
