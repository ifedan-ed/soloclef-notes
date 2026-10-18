"""Hold a Studio output's price when it starts.

Each kind is its own action (``studio_report``, ``studio_video``, ...). The
plan must include the kind and, for video, allow some length; the plan's
maximum length then caps the video itself. Settling happens when the job
ends (``app.billing.jobs``).
"""

from __future__ import annotations

from uuid import UUID

from . import ledger, meter
from .entitlements import Requirement, video_seconds_cap
from .persistence.models import BillingPlan


def studio_action(kind: str) -> str:
    return f"studio_{kind}"


async def begin_studio(
    user_id: UUID,
    kind: str,
    *,
    workspace_id: int,
    notebook_id: int | None,
    session_factory=None,
) -> tuple[meter.Charge, int | None]:
    """Hold the price (raises ``BillingDenied`` under live billing) and return
    the video length cap to apply, if any."""
    need = Requirement(studio_kind=kind, video_seconds=1 if kind == "video" else None)
    charge = await meter.begin(
        user_id,
        studio_action(kind),
        need=need,
        workspace_id=workspace_id,
        notebook_id=notebook_id,
        details={"kind": kind},
        session_factory=session_factory,
    )
    cap = None
    if kind == "video" and charge.mode == "live":
        cap = await plan_video_cap(user_id, session_factory=session_factory)
    return charge, cap


async def plan_video_cap(user_id: UUID, *, session_factory=None) -> int | None:
    factory = session_factory or meter._default_factory()
    async with factory() as session:
        account, _user = await ledger.lock_account(session, user_id)
        plan = await session.get(BillingPlan, account.plan_id)
        await session.commit()
    cap = video_seconds_cap(plan.entitlements if plan else None)
    return cap or None


def job_kind(job) -> str:
    """The Studio kind a deliverable job produces (``video`` or its artifact output)."""
    if getattr(job, "kind", None) == "video":
        return "video"
    request = getattr(job, "request", None) or {}
    output = request.get("output") if isinstance(request, dict) else None
    return str(output or "report")


async def begin_job_retry(user_id: UUID, job, *, session_factory=None) -> meter.Charge:
    from .jobs import deliverable_reference

    kind = job_kind(job)
    return await meter.begin(
        user_id,
        studio_action(kind),
        need=Requirement(
            studio_kind=kind, video_seconds=1 if kind == "video" else None
        ),
        workspace_id=job.workspace_id,
        thread_id=job.thread_id,
        reference=deliverable_reference(job.id),
        details={"kind": kind, "retry": True},
        session_factory=session_factory,
    )
