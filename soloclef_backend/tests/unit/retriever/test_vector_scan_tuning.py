"""The scan settings come from config, and a bad one is refused, not guessed.

``hnsw.iterative_scan`` takes an enum, and the value reaches Postgres inside
the SQL string because GUCs take no bind parameters. A typo must stop here
rather than abort the search transaction with a Postgres error, and an
unrecognised value must never be quietly replaced with a working one -- a
search that silently drops back to a non-iterative scan is the bug this whole
change exists to remove.
"""

from __future__ import annotations

import pytest
from sqlalchemy.sql.elements import TextClause

from app.retriever.vector_scan_tuning import tune_vector_scan

pytestmark = pytest.mark.unit


class _RecordingSession:
    def __init__(self) -> None:
        self.sql: list[str] = []

    async def execute(self, statement):
        assert isinstance(statement, TextClause)
        self.sql.append(str(statement))
        return None


@pytest.fixture
def settings(monkeypatch):
    from app.config import config

    def _set(mode, max_scan_tuples):
        monkeypatch.setattr(config, "VECTOR_SEARCH_ITERATIVE_SCAN", mode)
        monkeypatch.setattr(config, "VECTOR_SEARCH_MAX_SCAN_TUPLES", max_scan_tuples)

    return _set


async def test_the_configured_values_reach_the_transaction(settings):
    settings("strict_order", 5000)
    session = _RecordingSession()

    await tune_vector_scan(session)

    assert session.sql == [
        "SET LOCAL hnsw.iterative_scan = 'strict_order'",
        "SET LOCAL hnsw.max_scan_tuples = 5000",
    ]


async def test_settings_are_local_to_the_transaction(settings):
    """A pooled connection must not carry one request's GUCs into the next."""
    settings("relaxed_order", 20000)
    session = _RecordingSession()

    await tune_vector_scan(session)

    assert all(s.startswith("SET LOCAL ") for s in session.sql)


@pytest.mark.parametrize(
    "mode", ["relaxed", "RELAXED_ORDER'; DROP TABLE chunks --", ""]
)
async def test_an_unknown_scan_mode_is_refused(mode, settings):
    settings(mode, 20000)
    session = _RecordingSession()

    with pytest.raises(ValueError, match="VECTOR_SEARCH_ITERATIVE_SCAN"):
        await tune_vector_scan(session)

    assert session.sql == []


async def test_the_mode_is_read_case_insensitively(settings):
    settings("Relaxed_Order", 20000)
    session = _RecordingSession()

    await tune_vector_scan(session)

    assert session.sql[0] == "SET LOCAL hnsw.iterative_scan = 'relaxed_order'"


@pytest.mark.parametrize("ceiling", [0, -1])
async def test_a_nonsense_scan_ceiling_is_refused(ceiling, settings):
    settings("relaxed_order", ceiling)
    session = _RecordingSession()

    with pytest.raises(ValueError, match="VECTOR_SEARCH_MAX_SCAN_TUPLES"):
        await tune_vector_scan(session)

    assert session.sql == []
