"""Plan rules, as pure decisions."""

from __future__ import annotations

import pytest

from app.billing.defaults import PLANS
from app.billing.entitlements import Requirement, denial

pytestmark = pytest.mark.unit

FREE, PLUS, PRO = (p["entitlements"] for p in PLANS)


@pytest.mark.parametrize(
    ("ent", "need", "code"),
    [
        (FREE, Requirement(parser="cloud"), "PLAN_PARSER_NOT_INCLUDED"),
        (FREE, Requirement(parser="local"), None),
        (FREE, Requirement(vision=True), "PLAN_VISION_NOT_INCLUDED"),
        (FREE, Requirement(model_tier="premium"), "PLAN_MODEL_TIER_NOT_INCLUDED"),
        (
            FREE,
            Requirement(studio_kind="video", video_seconds=1),
            "PLAN_STUDIO_KIND_NOT_INCLUDED",
        ),
        (FREE, Requirement(studio_kind="report"), None),
        (FREE, Requirement(pages=51), "PLAN_TOO_MANY_PAGES"),
        (FREE, Requirement(running_jobs=1), "PLAN_TOO_MANY_JOBS"),
        (FREE, Requirement(running_jobs=0), None),
        (PLUS, Requirement(parser="cloud", vision=True, model_tier="premium"), None),
        (
            PLUS,
            Requirement(studio_kind="video", video_seconds=301),
            "PLAN_VIDEO_TOO_LONG",
        ),
        (PRO, Requirement(studio_kind="video", video_seconds=600), None),
        (PRO, Requirement(pages=1000), None),
        (
            {},
            Requirement(parser="cloud"),
            "PLAN_PARSER_NOT_INCLUDED",
        ),  # missing keys read as Free
        ({"max_pages_per_upload": 0}, Requirement(pages=10_000), None),  # 0 = unlimited
    ],
)
def test_plan_rules(ent, need, code):
    assert denial(ent, need) == code


def test_video_cap_is_clamped_to_the_engine_maximum():
    from app.billing.entitlements import video_seconds_cap
    from app.deliverables.jobs.policy import VIDEO_SPEC

    engine = VIDEO_SPEC.max_duration_seconds
    above = {**PRO, "max_video_seconds": engine * 3}
    assert video_seconds_cap(above) == engine
    assert video_seconds_cap(PLUS) == 300
    assert video_seconds_cap(FREE) == 0
    assert video_seconds_cap({"max_video_seconds": "junk"}) == 0
    assert video_seconds_cap({"max_video_seconds": -5}) == 0
    # A plan set above the engine cannot promise a length the executor refuses.
    need = Requirement(studio_kind="video", video_seconds=engine + 1)
    assert denial(above, need) == "PLAN_VIDEO_TOO_LONG"
    assert denial(above, Requirement(studio_kind="video", video_seconds=engine)) is None


@pytest.mark.asyncio
async def test_plan_video_cap_is_clamped(monkeypatch):
    from contextlib import asynccontextmanager
    from types import SimpleNamespace
    from uuid import uuid4

    from app.billing import ledger, studio
    from app.deliverables.jobs.policy import VIDEO_SPEC

    plan = SimpleNamespace(
        entitlements={**PRO, "max_video_seconds": VIDEO_SPEC.max_duration_seconds + 900}
    )

    class _Session:
        async def get(self, _model, _id):
            return plan

        async def commit(self):
            return None

    @asynccontextmanager
    async def factory():
        yield _Session()

    async def lock_account(_session, _user_id):
        return SimpleNamespace(plan_id=1), None

    monkeypatch.setattr(ledger, "lock_account", lock_account)
    cap = await studio.plan_video_cap(uuid4(), session_factory=factory)
    assert cap == VIDEO_SPEC.max_duration_seconds
