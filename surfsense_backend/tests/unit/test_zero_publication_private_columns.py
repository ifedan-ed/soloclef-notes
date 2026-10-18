"""What Zero may send, and that every column a client asks for is sent.

zero-cache sends every published column of a row to whoever syncs it, and
refuses a client (``SchemaVersionNotSupported``, the whole connection) whose
schema names a column the publication does not have. So a column list is
both the secrecy boundary and a contract with the web's (and the phone
app's copy of the web's) Zero schema.
"""

from __future__ import annotations

import re
from pathlib import Path

import pytest

from app import zero_publication

pytestmark = pytest.mark.unit

_REPO = Path(__file__).resolve().parents[3]
_WEB_SCHEMA = _REPO / "surfsense_web" / "zero" / "schema"
_MIGRATION = (
    Path(__file__).resolve().parents[2]
    / "alembic"
    / "versions"
    / "c0029_zero_config_thread_access.py"
)

_TABLE = re.compile(r'table\("([^"]+)"\)\s*\.columns\(\{(.*?)\}\)', re.S)
_COLUMN = re.compile(r"^\s*(\w+):\s*\w+(?:<[^>]*>)?\(\)(.*)$", re.M)
_FROM = re.compile(r'\.from\("([^"]+)"\)')


def _web_schema_columns() -> dict[str, set[str]]:
    """Each table the website's Zero schema names, and its server-side columns."""
    tables: dict[str, set[str]] = {}
    for path in sorted(_WEB_SCHEMA.glob("*.ts")):
        for name, body in _TABLE.findall(path.read_text()):
            columns = set()
            for key, rest in _COLUMN.findall(body):
                renamed = _FROM.search(rest)
                columns.add(renamed.group(1) if renamed else key)
            tables[name] = columns
    return tables


def _entry(table: str, actual: set[str], shape=zero_publication.ZERO_PUBLICATION):
    class _Conn:
        pass

    original = zero_publication._table_columns
    zero_publication._table_columns = lambda _conn, name: (
        actual if name == table else set()
    )
    try:
        return zero_publication._format_table_entry(_Conn(), table, shape)
    finally:
        zero_publication._table_columns = original


CONNECTOR_COLUMNS = {
    "id",
    "name",
    "connector_type",
    "is_indexable",
    "last_indexed_at",
    "config",
    "enable_vision_llm",
    "periodic_indexing_enabled",
    "indexing_frequency_minutes",
    "next_scheduled_at",
    "workspace_id",
    "user_id",
    "created_at",
}


def test_connector_config_is_never_published() -> None:
    entry = _entry("search_source_connectors", CONNECTOR_COLUMNS)
    assert entry is not None
    assert '"config"' not in entry
    assert '"enable_vision_llm"' not in entry
    for column in ("id", "name", "workspace_id", "periodic_indexing_enabled"):
        assert f'"{column}"' in entry


def test_threads_publish_who_may_read_them() -> None:
    entry = _entry(
        "new_chat_threads",
        {"id", "workspace_id", "visibility", "created_by_id", "title", "archived"},
    )
    assert entry is not None
    assert '"visibility"' in entry and '"created_by_id"' in entry
    # Still not the tab title: that stays on REST.
    assert '"title"' not in entry


def test_every_column_the_web_schema_names_is_published() -> None:
    # A column named by the client schema but not published refuses the whole
    # Zero connection of every client built from it (web and phone app).
    web = _web_schema_columns()
    assert "search_source_connectors" in web and "new_chat_threads" in web
    for table, columns in web.items():
        assert table in zero_publication.ZERO_PUBLICATION, table
        published = zero_publication.ZERO_PUBLICATION[table]
        if published is None:
            continue
        missing = columns - set(published)
        assert not missing, f"{table}: web schema names unpublished {missing}"


def test_the_web_schema_no_longer_names_connector_config() -> None:
    assert "config" not in _web_schema_columns()["search_source_connectors"]


def test_migration_c0029_reconciles_and_its_downgrade_restores_the_old_shape() -> None:
    text = _MIGRATION.read_text()
    assert 'revision: str = "c0029_zero_config_thread_access"' in text
    assert 'down_revision: str | None = "c0028_announcement_cleared_reads"' in text
    assert "apply_publication(op.get_bind())" in text

    namespace: dict = {}
    exec(  # the module's constant only, without alembic's op proxy
        compile(
            text.split("def upgrade")[0].replace("from alembic import op\n", ""),
            str(_MIGRATION),
            "exec",
        ),
        namespace,
    )
    previous = {**zero_publication.ZERO_PUBLICATION, **namespace["PREVIOUS_SHAPE"]}
    restored = _entry("search_source_connectors", CONNECTOR_COLUMNS, previous)
    assert restored == '"search_source_connectors"'
    threads = _entry("new_chat_threads", {"id", "workspace_id", "visibility"}, previous)
    assert threads == '"new_chat_threads" ("id", "workspace_id")'
