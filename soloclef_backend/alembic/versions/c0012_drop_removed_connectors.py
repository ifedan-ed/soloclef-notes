"""Drop the connector and document types of the connectors removed from the code.

The owner asked for connectors that were never configured on this host to be
removed rather than hidden (2026-09-26): Notion, Slack, Discord, GitHub,
Linear, Jira, Confluence, ClickUp, Airtable, OneDrive, Teams, Dropbox,
Composio, BookStack, Elasticsearch, Luma, Circleback, Obsidian, Gmail, Google
Calendar, and the retired search-API connectors (Serper, Tavily, SearXNG,
Linkup, Baidu). What stays: Google Drive, Nextcloud, MCP servers and the web
crawler.

``searchsourceconnectortype`` and ``documenttype`` are rebuilt with only the
kept values (Postgres cannot drop an enum value in place). This runs only if no
row uses a removed value; otherwise it stops with the counts, so nothing is
ever rewritten under a live connector or document. On the live database there
were none (0 rows) when this was written.

The Obsidian plugin's two partial unique indexes name ``OBSIDIAN_CONNECTOR`` in
their predicate, so they go first; the other indexes on these columns are
rebuilt by Postgres as part of the type change.

``documents.document_type`` is in the Zero publication's column list, and
Postgres refuses to retype a published column, so the two tables leave the
publication for the type change and ``apply_publication`` restores its
canonical shape right after, in the same transaction. The column keeps its
name and text values; zero-cache may resync its replica after the DDL.

The downgrade adds the values back (at the end of each type) and recreates the
Obsidian indexes.

Revision ID: c0012_drop_removed_connectors
Revises: c0011_drop_upstream_payments
"""

from __future__ import annotations

from collections.abc import Sequence

import sqlalchemy as sa

from alembic import op
from app.zero_publication import PUBLICATION_NAME, apply_publication

revision: str = "c0012_drop_removed_connectors"
down_revision: str | None = "c0011_drop_upstream_payments"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None

KEPT_CONNECTOR_TYPES = (
    "GOOGLE_DRIVE_CONNECTOR",
    "WEBCRAWLER_CONNECTOR",
    "MCP_CONNECTOR",
    "NEXTCLOUD_CONNECTOR",
)
REMOVED_CONNECTOR_TYPES = (
    "SERPER_API",
    "TAVILY_API",
    "SEARXNG_API",
    "LINKUP_API",
    "BAIDU_SEARCH_API",
    "SLACK_CONNECTOR",
    "TEAMS_CONNECTOR",
    "ONEDRIVE_CONNECTOR",
    "NOTION_CONNECTOR",
    "GITHUB_CONNECTOR",
    "LINEAR_CONNECTOR",
    "DISCORD_CONNECTOR",
    "JIRA_CONNECTOR",
    "CONFLUENCE_CONNECTOR",
    "CLICKUP_CONNECTOR",
    "GOOGLE_CALENDAR_CONNECTOR",
    "GOOGLE_GMAIL_CONNECTOR",
    "AIRTABLE_CONNECTOR",
    "LUMA_CONNECTOR",
    "ELASTICSEARCH_CONNECTOR",
    "BOOKSTACK_CONNECTOR",
    "CIRCLEBACK_CONNECTOR",
    "OBSIDIAN_CONNECTOR",
    "DROPBOX_CONNECTOR",
    "COMPOSIO_GOOGLE_DRIVE_CONNECTOR",
    "COMPOSIO_GMAIL_CONNECTOR",
    "COMPOSIO_GOOGLE_CALENDAR_CONNECTOR",
)

KEPT_DOCUMENT_TYPES = (
    "EXTENSION",
    "CRAWLED_URL",
    "FILE",
    "YOUTUBE_VIDEO",
    "GOOGLE_DRIVE_FILE",
    "NOTE",
    "ARTIFACT",
    # Browser folder uploads (the Upload tab's folder picker).
    "LOCAL_FOLDER_FILE",
)
REMOVED_DOCUMENT_TYPES = (
    "SLACK_CONNECTOR",
    "TEAMS_CONNECTOR",
    "ONEDRIVE_FILE",
    "NOTION_CONNECTOR",
    "GITHUB_CONNECTOR",
    "LINEAR_CONNECTOR",
    "DISCORD_CONNECTOR",
    "JIRA_CONNECTOR",
    "CONFLUENCE_CONNECTOR",
    "CLICKUP_CONNECTOR",
    "GOOGLE_CALENDAR_CONNECTOR",
    "GOOGLE_GMAIL_CONNECTOR",
    "AIRTABLE_CONNECTOR",
    "LUMA_CONNECTOR",
    "ELASTICSEARCH_CONNECTOR",
    "BOOKSTACK_CONNECTOR",
    "CIRCLEBACK",
    "OBSIDIAN_CONNECTOR",
    "DROPBOX_FILE",
    "COMPOSIO_GOOGLE_DRIVE_CONNECTOR",
    "COMPOSIO_GMAIL_CONNECTOR",
    "COMPOSIO_GOOGLE_CALENDAR_CONNECTOR",
)

# (enum type, table, column, kept values, removed values)
ENUMS = (
    (
        "searchsourceconnectortype",
        "search_source_connectors",
        "connector_type",
        KEPT_CONNECTOR_TYPES,
        REMOVED_CONNECTOR_TYPES,
    ),
    (
        "documenttype",
        "documents",
        "document_type",
        KEPT_DOCUMENT_TYPES,
        REMOVED_DOCUMENT_TYPES,
    ),
)

OBSIDIAN_INDEXES = {
    "search_source_connectors_obsidian_plugin_vault_uniq": "vault_id",
    "search_source_connectors_obsidian_plugin_fingerprint_uniq": "vault_fingerprint",
}


def _literal_list(values: Sequence[str]) -> str:
    return ", ".join(f"'{value}'" for value in values)


def _rows_using_removed_values() -> dict[str, int]:
    bind = op.get_bind()
    in_use: dict[str, int] = {}
    for _enum, table, column, _kept, removed in ENUMS:
        rows = bind.execute(
            sa.text(
                f'SELECT "{column}"::text, count(*) FROM "{table}" '
                f'WHERE "{column}"::text IN ({_literal_list(removed)}) '
                f'GROUP BY "{column}"'
            )
        ).all()
        for value, count in rows:
            in_use[f"{table}.{column}={value}"] = count
    return in_use


def _existing_values(enum: str) -> list[str]:
    return list(
        op.get_bind()
        .execute(sa.text(f"SELECT unnest(enum_range(NULL::{enum}))::text"))
        .scalars()
    )


def upgrade() -> None:
    in_use = _rows_using_removed_values()
    if in_use:
        raise RuntimeError(
            "Rows still use removed connector/document types; "
            f"nothing was changed: {in_use}"
        )

    for index in OBSIDIAN_INDEXES:
        op.execute(f'DROP INDEX IF EXISTS "{index}"')

    published = _published_tables([table for _e, table, _c, _k, _r in ENUMS])
    for table in published:
        op.execute(f'ALTER PUBLICATION "{PUBLICATION_NAME}" DROP TABLE "{table}"')

    for enum, table, column, kept, _removed in ENUMS:
        unknown = set(_existing_values(enum)) - set(kept) - set(_removed)
        if unknown:
            raise RuntimeError(
                f"{enum} has values this migration does not know: {sorted(unknown)}"
            )
        op.execute(f'ALTER TYPE "{enum}" RENAME TO "{enum}_old"')
        op.execute(f'CREATE TYPE "{enum}" AS ENUM ({_literal_list(kept)})')
        op.execute(
            f'ALTER TABLE "{table}" ALTER COLUMN "{column}" '
            f'TYPE "{enum}" USING "{column}"::text::"{enum}"'
        )
        op.execute(f'DROP TYPE "{enum}_old"')

    if published:
        apply_publication(op.get_bind())


def _published_tables(tables: list[str]) -> list[str]:
    rows = op.get_bind().execute(
        sa.text(
            "SELECT tablename FROM pg_publication_tables "
            "WHERE pubname = :name AND schemaname = current_schema()"
        ),
        {"name": PUBLICATION_NAME},
    )
    in_publication = set(rows.scalars())
    return [table for table in tables if table in in_publication]


def downgrade() -> None:
    # ADD VALUE cannot run inside a transaction block on older Postgres.
    with op.get_context().autocommit_block():
        for enum, _table, _column, _kept, removed in ENUMS:
            for value in removed:
                op.execute(f"ALTER TYPE \"{enum}\" ADD VALUE IF NOT EXISTS '{value}'")

    for index, key in OBSIDIAN_INDEXES.items():
        op.execute(
            f'CREATE UNIQUE INDEX IF NOT EXISTS "{index}" '
            f"ON search_source_connectors (user_id, ((config ->> '{key}'))) "
            "WHERE connector_type = 'OBSIDIAN_CONNECTOR'::searchsourceconnectortype "
            "AND (config ->> 'source') = 'plugin' "
            f"AND (config ->> '{key}') IS NOT NULL"
        )
