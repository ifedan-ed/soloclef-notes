"""The owner-approved starting catalog: plans, per-action prices and packs.

These are seeded into the database by migration ``c0009_billing_plans`` and are
what the admin "reset to defaults" action restores. After seeding, the database
is the source of truth: nothing reads these at request time.
"""

from __future__ import annotations

from typing import Any

from .units import credits_to_micros

STUDIO_KINDS = (
    "quiz",
    "flashcards",
    "mindmap",
    "slides",
    "data_table",
    "infographic",
    "audio",
    "report",
    "video",
)
PARSERS = ("local", "cloud")
MODEL_TIERS = ("standard", "premium")

# action -> (credits, unit, English label)
ACTION_PRICES: dict[str, tuple[float, str, str]] = {
    "chat_standard": (1, "turn", "Chat message (standard model)"),
    "chat_premium": (5, "turn", "Chat message (premium model)"),
    "cloud_page": (2, "page", "Cloud parsing"),
    "local_page": (0, "page", "Local parsing"),
    "vision_image": (1, "image", "Image understanding (vision)"),
    "image_generation": (4, "image", "Image generation"),
    "studio_quiz": (5, "output", "Quiz"),
    "studio_flashcards": (5, "output", "Flashcards"),
    "studio_mindmap": (10, "output", "Mind map"),
    "studio_slides": (10, "output", "Slides"),
    "studio_data_table": (10, "output", "Data table"),
    "studio_infographic": (15, "output", "Infographic"),
    "studio_audio": (15, "output", "Audio overview"),
    "studio_report": (20, "output", "Report"),
    "studio_video": (40, "output", "Video overview"),
    "embedding": (0, "document", "Embeddings (search index)"),
    # Scraping/crawling, on the same meter as everything else (c0021). Prices
    # match the ``*_MICROS_PER_*`` config knobs' own defaults exactly, so
    # moving onto this meter changes nobody's cost; an admin edits them here
    # from now on, same as any other action.
    "web_crawl": (0.2, "page", "Web crawl (per page)"),
    "web_crawl_captcha": (0.3, "solve", "Web crawl captcha solve"),
    "reddit_item": (0.35, "item", "Reddit scrape (per item)"),
    "google_search_serp": (0.55, "SERP", "Google search (per page of results)"),
    "google_maps_place": (0.35, "place", "Google Maps scrape (per place)"),
    "google_maps_review": (0.15, "review", "Google Maps scrape (per review)"),
    "amazon_product": (0.35, "product", "Amazon scrape (per product)"),
    "youtube_video": (0.25, "video", "YouTube scrape (per video)"),
    "youtube_comment": (0.15, "comment", "YouTube scrape (per comment)"),
    "instagram_item": (0.35, "item", "Instagram scrape (per item)"),
    "instagram_comment": (0.15, "comment", "Instagram scrape (per comment)"),
    "tiktok_video": (0.35, "video", "TikTok scrape (per video)"),
    "tiktok_user": (0.25, "profile", "TikTok scrape (per profile)"),
    "tiktok_comment": (0.15, "comment", "TikTok scrape (per comment)"),
    "indeed_job": (0.35, "job", "Indeed scrape (per job)"),
    "walmart_product": (0.35, "product", "Walmart scrape (per product)"),
    "walmart_review": (0.15, "review", "Walmart scrape (per review)"),
    # A chat's Exa / Tavily search on the workspace's own key (c0022), at the
    # provider's own list price for the search the chat runs: Exa's auto
    # search with highlights for 10 results ($5 + 10 x $1 per 1k) and one
    # Tavily basic search (1 API credit, $0.008 pay-as-you-go). The research
    # box's Exa / Tavily searches stay free of credits.
    "exa_search": (1.5, "search", "Exa search in chat (per search)"),
    "tavily_search": (0.8, "search", "Tavily search in chat (per search)"),
}

# Entitlement keys every plan carries. A missing key reads as the Free value.
FREE_ENTITLEMENTS: dict[str, Any] = {
    "parsers": ["local"],
    "vision": False,
    "model_tiers": ["standard"],
    "studio_kinds": [k for k in STUDIO_KINDS if k != "video"],
    "max_video_seconds": 0,
    "max_pages_per_upload": 50,
    "max_concurrent_jobs": 1,
    "max_notebooks": 0,
    "max_sources_per_notebook": 0,
}

PLANS: list[dict[str, Any]] = [
    {
        "slug": "free",
        "sort_order": 0,
        "is_default": True,
        "price_cents": 0,
        "monthly_credits": 100,
        "entitlements": dict(FREE_ENTITLEMENTS),
        "content": {
            "en": {
                "name": "Free",
                "tagline": "Try notebooks with your own documents.",
                "features": [
                    "100 credits every month",
                    "Local document parsing",
                    "Standard models",
                    "Every Studio output except video",
                ],
            }
        },
    },
    {
        "slug": "plus",
        "sort_order": 1,
        "is_default": False,
        "price_cents": 900,
        "monthly_credits": 1500,
        "entitlements": {
            "parsers": ["local", "cloud"],
            "vision": True,
            "model_tiers": ["standard", "premium"],
            "studio_kinds": list(STUDIO_KINDS),
            "max_video_seconds": 300,
            "max_pages_per_upload": 300,
            "max_concurrent_jobs": 2,
            "max_notebooks": 0,
            "max_sources_per_notebook": 0,
        },
        "content": {
            "en": {
                "name": "Plus",
                "tagline": "For regular study and research.",
                "features": [
                    "1,500 credits every month",
                    "Cloud parsing for scanned and complex files",
                    "Image understanding",
                    "Premium models",
                    "All Studio outputs",
                ],
            }
        },
    },
    {
        "slug": "pro",
        "sort_order": 2,
        "is_default": False,
        "price_cents": 2400,
        "monthly_credits": 5000,
        "entitlements": {
            "parsers": ["local", "cloud"],
            "vision": True,
            "model_tiers": ["standard", "premium"],
            "studio_kinds": list(STUDIO_KINDS),
            "max_video_seconds": 600,
            "max_pages_per_upload": 1000,
            "max_concurrent_jobs": 4,
            "max_notebooks": 0,
            "max_sources_per_notebook": 0,
        },
        "content": {
            "en": {
                "name": "Pro",
                "tagline": "For heavy use and long videos.",
                "features": [
                    "5,000 credits every month",
                    "Everything in Plus",
                    "Videos up to 10 minutes",
                    "Larger uploads and more jobs at once",
                ],
            }
        },
    },
]

PACKS: list[dict[str, Any]] = [
    {
        "slug": "credits-500",
        "sort_order": 0,
        "credits": 500,
        "price_cents": 500,
        "content": {"en": {"name": "500 credits"}},
    },
    {
        "slug": "credits-1200",
        "sort_order": 1,
        "credits": 1200,
        "price_cents": 1000,
        "content": {"en": {"name": "1,200 credits"}},
    },
]

# Non-secret settings and their defaults. Secrets (payment keys) have
# no default: unset means "use the environment variable, if any".
SETTINGS: dict[str, Any] = {
    # off: nothing is metered. shadow: every action is metered and recorded
    # with what it would have cost, but nothing is refused or debited.
    # live: entitlements and balances are enforced.
    "mode": "shadow",
    "signup_bonus_micros": 0,
    "stripe_enabled": False,
    "stripe_publishable_key": "",
    # Crypto: the operator's own BTCPay Server (Bitcoin, and USDT on Tron and
    # Polygon through the Tether plugin). Buys credit packs and prepaid plan
    # months; there are no recurring crypto subscriptions. Funds go to the
    # wallets connected to the operator's BTCPay store.
    "crypto_enabled": False,
    "crypto_provider": "btcpay",
    # No server of its own: BTCPAY_URL or the admin page names it, and until
    # one does crypto stays "pending" (payment_methods.py) and is never offered.
    "btcpay_url": "",
    "btcpay_store_id": "",
    "crypto_payment_methods": ["BTC-CHAIN", "USDT-TRON", "USDT-POLYGON"],
    "crypto_month_options": [1, 3, 12],
    # Percent off the monthly price when paying for this many months by crypto.
    "crypto_discounts": {"12": 10},
    "stale_hold_hours": 6,
    # model id -> "standard" | "premium" (see app/billing/model_tiers.py)
    "model_tiers": {},
    "low_balance_credits": 20,
    # Where "Report Issue" mails go (app.support_contact); shown to everyone.
    # None until SUPPORT_EMAIL or the admin page publishes one.
    "support_email": "",
}


def seed_plan_rows() -> list[dict[str, Any]]:
    return [
        {
            "slug": plan["slug"],
            "sort_order": plan["sort_order"],
            "is_default": plan["is_default"],
            "price_cents": plan["price_cents"],
            "monthly_credits_micros": credits_to_micros(plan["monthly_credits"]),
            "entitlements": plan["entitlements"],
            "content": plan["content"],
        }
        for plan in PLANS
    ]


def seed_price_rows() -> list[dict[str, Any]]:
    return [
        {
            "action": action,
            "micros": credits_to_micros(credits),
            "unit": unit,
            "sort_order": index,
            "content": {"en": {"label": label}},
        }
        for index, (action, (credits, unit, label)) in enumerate(ACTION_PRICES.items())
    ]


def seed_pack_rows() -> list[dict[str, Any]]:
    return [
        {
            "slug": pack["slug"],
            "sort_order": pack["sort_order"],
            "credits_micros": credits_to_micros(pack["credits"]),
            "price_cents": pack["price_cents"],
            "content": pack["content"],
        }
        for pack in PACKS
    ]
