"""Scraping/crawling onto the main billing meter.

``app/services/wallet_credit.py`` and its two callers (web crawl, platform
scrape) debited ``User.credit_micros_balance`` directly, bypassing
``app.billing.meter`` entirely: no plan rules, no ``billing_usage`` audit
rows, no workspace-owner payer. ``app/capabilities/core/billing.py`` now
routes these through ``meter.check``/``meter.record`` like every other
billable action (``custom/WORKSPACE_CREDITS.md``'s "known gap" is closed).

This migration only seeds the new actions' prices into
``billing_action_prices`` -- the same owner-approved-defaults-as-editable-rows
pattern ``c0009_billing_plans`` established. Each price is the exact micro-USD
default its old ``*_MICROS_PER_*`` config knob already had (frozen here, same
as ``c0009`` froze its own copy of ``app.billing.defaults``), so moving onto
this meter changes nobody's cost; an admin edits them from the billing admin
page from here on, same as any other action. ``ON CONFLICT DO NOTHING`` makes
re-running safe and leaves an admin's own edit alone.

Local revision, namespaced as custom/README.md explains.

Revision ID: c0021_scrape_crawl_billing
Revises: c0020_account_connections
"""

import json
from collections.abc import Sequence

import sqlalchemy as sa

from alembic import op

revision: str = "c0021_scrape_crawl_billing"
down_revision: str | None = "c0020_account_connections"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None

_C = 10_000  # micros per credit (app.billing.units.MICROS_PER_CREDIT)

# (action, credits, unit, label) -- credits * _C == the old config default's
# micro-USD value exactly.
_PRICES: list[tuple[str, float, str, str]] = [
    ("web_crawl", 0.2, "page", "Web crawl (per page)"),
    ("web_crawl_captcha", 0.3, "solve", "Web crawl captcha solve"),
    ("reddit_item", 0.35, "item", "Reddit scrape (per item)"),
    ("google_search_serp", 0.55, "SERP", "Google search (per page of results)"),
    ("google_maps_place", 0.35, "place", "Google Maps scrape (per place)"),
    ("google_maps_review", 0.15, "review", "Google Maps scrape (per review)"),
    ("amazon_product", 0.35, "product", "Amazon scrape (per product)"),
    ("youtube_video", 0.25, "video", "YouTube scrape (per video)"),
    ("youtube_comment", 0.15, "comment", "YouTube scrape (per comment)"),
    ("instagram_item", 0.35, "item", "Instagram scrape (per item)"),
    ("instagram_comment", 0.15, "comment", "Instagram scrape (per comment)"),
    ("tiktok_video", 0.35, "video", "TikTok scrape (per video)"),
    ("tiktok_user", 0.25, "profile", "TikTok scrape (per profile)"),
    ("tiktok_comment", 0.15, "comment", "TikTok scrape (per comment)"),
    ("indeed_job", 0.35, "job", "Indeed scrape (per job)"),
    ("walmart_product", 0.35, "product", "Walmart scrape (per product)"),
    ("walmart_review", 0.15, "review", "Walmart scrape (per review)"),
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
                " sort_order, content) VALUES (:a, :m, :u, :o,"
                " CAST(:t AS jsonb)) ON CONFLICT (action) DO NOTHING"
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
    actions = [row[0] for row in _PRICES]
    bind.execute(
        sa.text("DELETE FROM billing_action_prices WHERE action = ANY(:actions)"),
        {"actions": actions},
    )
