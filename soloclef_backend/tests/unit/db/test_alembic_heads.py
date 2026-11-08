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


def test_every_revision_id_fits_the_version_column():
    """``alembic_version.version_num`` is varchar(32).

    A longer id passes every local check -- the file imports, the head stays
    single, the SQL is valid -- and then fails on deploy at the very last
    statement of the migration, the UPDATE of alembic_version, with
    ``value too long for type character varying(32)``. The DDL rolls back with
    it, so the database is left clean and the deploy is not: `migrations` exits
    1 and the app containers never start.
    """
    config = Config(str(_BACKEND / "alembic.ini"))
    config.set_main_option("script_location", str(_BACKEND / "alembic"))
    scripts = ScriptDirectory.from_config(config)
    too_long = {
        script.revision: len(script.revision)
        for script in scripts.walk_revisions()
        if len(script.revision) > 32
    }
    assert not too_long, (
        "revision ids longer than alembic_version.version_num allows: "
        f"{too_long}. Shorten the `revision` string; the filename may stay long."
    )
