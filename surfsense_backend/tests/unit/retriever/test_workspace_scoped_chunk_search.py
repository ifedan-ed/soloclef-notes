"""Chunk search must filter on ``chunks.workspace_id``, not on the joined document.

Postgres cannot combine an HNSW index scan on ``chunks.embedding`` with a
predicate that lives on another table: it walks the index throwing other
workspaces' rows away, and stops at the index's candidate budget. The query
then returns *fewer* than ``top_k`` rows -- search quality degrades with no
error anywhere. Both the filter and the index have to sit on ``chunks``, and
the scan has to be allowed to iterate until the limit is satisfied.

These tests read the SQL the retriever emits, plus the GUCs it sets for the
search transaction, because that is the whole fix: a workspace predicate on
the indexed table and an iterative scan behind it.
"""

from __future__ import annotations

import pytest
from sqlalchemy.dialects import postgresql
from sqlalchemy.sql.elements import TextClause

from app.retriever.chunks_hybrid_search import ChucksHybridSearchRetriever

pytestmark = pytest.mark.unit

WORKSPACE_ID = 7


class _RecordingSession:
    """An AsyncSession stand-in that records statements and returns no rows."""

    def __init__(self) -> None:
        self.statements: list = []

    async def execute(self, statement, *args, **kwargs):
        self.statements.append(statement)
        return _EmptyResult()

    def sql(self) -> list[str]:
        rendered = []
        for statement in self.statements:
            if isinstance(statement, TextClause):
                rendered.append(str(statement))
            else:
                rendered.append(str(statement.compile(dialect=postgresql.dialect())))
        return rendered

    def settings(self) -> list[str]:
        return [s for s in self.sql() if s.upper().startswith("SET LOCAL")]

    def queries(self) -> list[str]:
        return [s for s in self.sql() if not s.upper().startswith("SET LOCAL")]


class _EmptyResult:
    def all(self):
        return []

    def scalars(self):
        return self

    def unique(self):
        return self


@pytest.fixture
def stub_embedding(monkeypatch):
    from app.config import config

    class _Model:
        dimension = config.EMBEDDING_DIMENSION

        def embed(self, _text):
            return [0.0] * self.dimension

    monkeypatch.setattr(config, "query_embedding_model_instance", _Model())


async def _run(method: str, stub_embedding) -> _RecordingSession:
    session = _RecordingSession()
    retriever = ChucksHybridSearchRetriever(session)
    await getattr(retriever, method)(
        query_text="anything", top_k=5, workspace_id=WORKSPACE_ID
    )
    return session


@pytest.mark.parametrize(
    "method", ["vector_search", "full_text_search", "hybrid_search"]
)
async def test_chunk_search_filters_on_the_chunk_table(method, stub_embedding):
    session = await _run(method, stub_embedding)

    assert session.queries(), f"{method} issued no query"
    for sql in session.queries():
        assert "chunks.workspace_id = " in sql, (
            f"{method} does not filter chunks.workspace_id; the indexes it "
            f"scans and the workspace predicate must sit on one table.\n{sql}"
        )
        assert "documents.workspace_id = " not in sql, (
            f"{method} still scopes by the joined document's workspace.\n{sql}"
        )


async def test_undated_vector_search_joins_documents_only_to_load_them(
    stub_embedding,
):
    """No date window means nothing on ``documents`` to filter by.

    Eager-loading the document for the returned rows is a LEFT OUTER JOIN and
    stays; an inner join exists only to filter, and there is nothing left to
    filter there.
    """
    session = await _run("vector_search", stub_embedding)

    for sql in session.queries():
        assert sql.count("JOIN documents") == sql.count("LEFT OUTER JOIN documents"), (
            sql
        )


async def test_search_enables_iterative_scan_for_the_transaction(stub_embedding):
    session = await _run("vector_search", stub_embedding)

    settings = " ".join(session.settings())
    assert "hnsw.iterative_scan" in settings, (
        "without an iterative scan a filtered HNSW search silently returns "
        f"fewer than top_k rows; settings issued: {session.settings()}"
    )
    assert "hnsw.max_scan_tuples" in settings, session.settings()


async def test_tuning_is_applied_before_the_search_runs(stub_embedding):
    session = await _run("vector_search", stub_embedding)

    first = session.sql()[0]
    assert first.upper().startswith("SET LOCAL"), (
        f"GUCs must be set before the query they tune, got: {first}"
    )
