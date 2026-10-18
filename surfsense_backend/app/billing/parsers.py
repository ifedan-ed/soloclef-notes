"""Pick a parser the person's plan includes, for imports nobody chose one for.

Research imports and other automatic reads use the server default parser
(Cloud here). Under live billing a plan without Cloud parsing gets the local
parser instead of a refusal: the import still works, just parsed locally.
In shadow mode the default is kept and the would-be refusal is recorded.
"""

from __future__ import annotations

from uuid import UUID

from app.etl_pipeline.etl_document import DocumentParser

from . import ledger
from .entitlements import normalized
from .persistence.models import BillingPlan
from .settings_store import load_settings


async def parser_within_plan(
    user_id: UUID | str, preferred: DocumentParser, *, session_factory=None
) -> DocumentParser:
    if preferred != DocumentParser.CLOUD:
        return preferred
    from .meter import _default_factory

    factory = session_factory or _default_factory()
    async with factory() as session:
        if (await load_settings(session)).mode != "live":
            return preferred
        account, user = await ledger.lock_account(session, UUID(str(user_id)))
        plan = await session.get(BillingPlan, account.plan_id)
        exempt = account.exempt or ledger.is_config_admin(user)
        await session.commit()
    if exempt or "cloud" in normalized(plan.entitlements if plan else None)["parsers"]:
        return preferred
    return DocumentParser.LOCAL
