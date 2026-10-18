"""Every alembic revision id fits the database's alembic_version column.

alembic_version.version_num is VARCHAR(32) (alembic's default). A longer id
fails only when the migration finishes and alembic writes the new version,
inside the migration's own transaction, so everything it did is rolled back
and the deploy stops with "value too long for type character varying(32)".
On 2026-10-11 the release's c0029 (41 characters) did exactly that on the
live server; the unit and integration tests had passed, because none of them
wrote the version to a real alembic_version table.
"""

import ast
from pathlib import Path

import pytest

VERSIONS = Path(__file__).resolve().parents[2] / "alembic" / "versions"
VERSION_NUM_MAX = 32


def _revision_ids() -> list[tuple[str, str]]:
    found = []
    for path in sorted(VERSIONS.glob("*.py")):
        for node in ast.parse(path.read_text()).body:
            target = None
            if isinstance(node, ast.Assign) and len(node.targets) == 1:
                target = node.targets[0]
            elif isinstance(node, ast.AnnAssign):
                target = node.target
            if isinstance(target, ast.Name) and target.id == "revision":
                value = node.value
                if isinstance(value, ast.Constant) and isinstance(value.value, str):
                    found.append((path.name, value.value))
    return found


@pytest.mark.unit
def test_every_revision_id_fits_alembic_version():
    ids = _revision_ids()
    assert ids, "no alembic revisions found"
    too_long = [(name, rev, len(rev)) for name, rev in ids if len(rev) > VERSION_NUM_MAX]
    assert too_long == []


@pytest.mark.unit
def test_revision_ids_are_unique():
    revs = [rev for _, rev in _revision_ids()]
    assert len(revs) == len(set(revs))
