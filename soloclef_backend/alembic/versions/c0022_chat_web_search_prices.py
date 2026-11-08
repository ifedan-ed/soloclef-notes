"""Prices for a chat's Exa and Tavily searches.

Exa and Tavily are chat tools now (the composer's Tools menu, and web-scope
automations), run on the workspace's own key and charged per search to the
workspace's owner like the Google Search tool (``app/capabilities/
web_search_api``). This only seeds their two ``billing_action_prices`` rows,
the ``c0021`` way: frozen here, ``ON CONFLICT DO NOTHING`` so a re-run keeps
an admin's own edit, editable on the billing admin page afterwards.

The price is the provider's list price for the search the chat runs: Exa's
``auto`` search with highlights for 10 results ($5/1k searches + $1/1k pages
of highlights = $0.015) and one Tavily ``basic`` search (1 API credit,
$0.008 pay-as-you-go). A Browserbase page read needs no row: it is charged
on ``web_crawl``, like the research box's "Read a web page".

Local revision, namespaced as custom/README.md explains.

Revision ID: c0022_chat_web_search_prices
Revises: c0021_scrape_crawl_billing
"""

import json
from collections.abc import Sequence

import sqlalchemy as sa

from alembic import op

revision: str = "c0022_chat_web_search_prices"
down_revision: str | None = "c0021_scrape_crawl_billing"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None

_C = 10_000  # micros per credit (app.billing.units.MICROS_PER_CREDIT)

_PRICES: list[tuple[str, float, str, str]] = [
    ("exa_search", 1.5, "search", "Exa search in chat (per search)"),
    ("tavily_search", 0.8, "search", "Tavily search in chat (per search)"),
]


def upgrade() -> None:
    bind = op.get_bind()
    max_order = bind.execute(
        sa.text("SELECT COALESCE(MAX(sort_order), 0) FROM billing_action_prices")
    ).scalar_one()
    for offset, (action, credits, unit, label) in enumerate(_PRICES, start=1):
        bind.execute(
            sa.text(
                "INSERT INTO billing_action_prices (action, micros, unit,"
                " sort_order, content, updated_at) VALUES (:a, :m, :u, :o,"
                " CAST(:t AS jsonb), now()) ON CONFLICT (action) DO NOTHING"
            ),
            {
                "a": action,
                "m": round(credits * _C),
                "u": unit,
                "o": max_order + offset,
                "t": json.dumps({"en": {"label": label}}),
            },
        )


def downgrade() -> None:
    bind = op.get_bind()
    bind.execute(
        sa.text("DELETE FROM billing_action_prices WHERE action = ANY(:actions)"),
        {"actions": [row[0] for row in _PRICES]},
    )
