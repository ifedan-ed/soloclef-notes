"""Transcript-drafting task: DRAFTING -> RENDERING.

The expensive, LLM-heavy step, so it runs under ``billable_call``. The API has
already moved the row to DRAFTING and stored the approved brief; this task
drafts the long-form transcript and chains straight into the render — the brief
gate is the only approval in the lifecycle.

Two things happen alongside drafting. A Studio podcast (no voice picker) has its
voices cast for the episode first. And each segment is voiced as soon as it is
drafted, into the segment cache the render reads, so the render mostly merges;
drafting itself stays one segment at a time, each seeing the ones before it.
"""

from __future__ import annotations

import asyncio
import logging

from app.celery_app import celery_app
from app.config import config as app_config
from app.podcasts.generation.transcript.graph import graph as transcript_graph
from app.podcasts.generation.transcript.state import TranscriptState
from app.podcasts.persistence import PodcastRepository
from app.podcasts.rendering import PodcastRenderer
from app.podcasts.schemas import PodcastSpec
from app.podcasts.service import PodcastService, read_spec
from app.podcasts.tts import get_text_to_speech
from app.podcasts.voices import get_voice_catalog, provider_from_service
from app.podcasts.voices.casting import cast_voices
from app.quick_tasks.llm import quick_llm
from app.services.billable_calls import (
    BillingSettlementError,
    QuotaInsufficientError,
    _resolve_agent_billing_for_workspace,
    billable_call,
)
from app.services.llm_service import get_agent_llm
from app.studio_models.fallback import run_with_fallback
from app.studio_models.llm import studio_llm_bundle
from app.studio_models.origin import plan_for_podcast
from app.studio_models.provenance import record_for_podcast
from app.tasks.celery_tasks import get_celery_session_maker, run_async_celery_task

from .render import render_audio_task, workdir_for
from .runtime import billable_session, mark_failed

logger = logging.getLogger(__name__)


@celery_app.task(name="podcast.draft_transcript", bind=True)
def draft_transcript_task(self, podcast_id: int, workspace_id: int) -> dict:
    try:
        return run_async_celery_task(
            lambda: _draft_transcript(podcast_id, workspace_id)
        )
    except Exception as exc:
        logger.error("Podcast %s drafting failed: %s", podcast_id, exc)
        message = str(exc)
        run_async_celery_task(lambda: mark_failed(podcast_id, message))
        return {"status": "failed", "podcast_id": podcast_id}


async def _draft_transcript(podcast_id: int, workspace_id: int) -> dict:
    async with get_celery_session_maker()() as session:
        repo = PodcastRepository(session)
        service = PodcastService(session)
        podcast = await repo.get(podcast_id)
        if podcast is None:
            raise ValueError(f"podcast {podcast_id} not found")

        spec = read_spec(podcast)
        if spec is None:
            raise ValueError(f"podcast {podcast_id} has no approved brief")

        owner_id, tier, base_model = await _resolve_agent_billing_for_workspace(
            session, workspace_id, thread_id=podcast.thread_id
        )

        plan = await plan_for_podcast(session, podcast_id)
        source_content = podcast.source_content or ""

        if plan is not None:
            spec = await _cast(session, service, podcast, spec, workspace_id)

        renderer = PodcastRenderer(tts=get_text_to_speech(), catalog=get_voice_catalog())
        synthesizer = renderer.synthesizer(workdir_for(podcast_id))
        voicing: list[asyncio.Task] = []

        def voice_segment(turns) -> None:
            voicing.append(
                asyncio.ensure_future(
                    renderer.voice_ahead(synthesizer, spec=spec, turns=turns)
                )
            )

        async def attempt(model):
            # Studio on Auto resolves the chat model here to record its name;
            # a chat-started podcast (no plan) leaves it to the graph as before.
            llm = None
            if model is not None:
                llm, _ = await studio_llm_bundle(session, workspace_id, model)
            elif plan is not None:
                llm = await get_agent_llm(session, workspace_id)
            result = await transcript_graph.ainvoke(
                TranscriptState(db_session=session, source_content=source_content),
                config={
                    "configurable": {
                        "workspace_id": workspace_id,
                        "spec": spec,
                        "focus": spec.focus,
                        "llm": llm,
                        "on_segment": voice_segment,
                    }
                },
            )
            served = (
                model.model_id if model else str(getattr(llm, "model", None) or "auto")
            )
            return result, served.split("/", 1)[-1]

        async def reset_for_fallback() -> None:
            # The next model drafts different lines; stop voicing the old ones.
            _stop(voicing)
            await session.rollback()
            await session.refresh(podcast)

        produced_by = None
        try:
            async with billable_call(
                user_id=owner_id,
                workspace_id=workspace_id,
                billing_tier=tier,
                base_model=base_model,
                quota_reserve_micros_override=app_config.QUOTA_DEFAULT_PODCAST_RESERVE_MICROS,
                usage_type="podcast_generation",
                call_details={"podcast_id": podcast_id, "title": podcast.title},
                billable_session_factory=billable_session,
            ):
                # Chat-started podcasts have no Studio plan and keep the chat model.
                if plan is None:
                    result, _ = await attempt(None)
                else:
                    result, produced_by = await run_with_fallback(
                        plan, attempt, before_fallback=reset_for_fallback
                    )
        except QuotaInsufficientError:
            _stop(voicing)
            await service.fail(podcast, "premium quota exhausted")
            await session.commit()
            return {"status": "failed", "podcast_id": podcast_id, "reason": "quota"}
        except BillingSettlementError:
            _stop(voicing)
            await service.fail(podcast, "billing settlement failed")
            await session.commit()
            return {"status": "failed", "podcast_id": podcast_id, "reason": "billing"}
        except BaseException:
            _stop(voicing)
            raise
        await _finish_voicing(podcast_id, voicing)

        await service.attach_transcript(podcast, result["transcript"])
        if produced_by is not None:
            await record_for_podcast(session, podcast_id, produced_by)
        await session.commit()

    # Enqueue only after the transaction is committed, so the render worker can
    # never pick up a row whose transcript isn't visible yet.
    render_audio_task.delay(podcast_id)
    return {"status": "rendering", "podcast_id": podcast_id}


async def _cast(
    session, service: PodcastService, podcast, spec: PodcastSpec, workspace_id: int
) -> PodcastSpec:
    """Cast the episode's voices with the quick-tasks model; keep them on failure."""
    llm = await quick_llm(session, workspace_id) or await get_agent_llm(
        session, workspace_id
    )
    cast = await cast_voices(
        llm,
        spec,
        title=podcast.title or "",
        source_content=podcast.source_content or "",
        catalog=get_voice_catalog(),
        provider=provider_from_service(app_config.TTS_SERVICE),
    )
    if cast is not spec:
        await service.cast_voices(podcast, cast)
        await session.commit()
        logger.info(
            "Podcast %s cast: %s",
            podcast.id,
            ", ".join(f"{s.name}={s.voice_id}" for s in cast.speakers),
        )
    return cast


def _stop(voicing: list[asyncio.Task]) -> None:
    for task in voicing:
        task.cancel()
    voicing.clear()


async def _finish_voicing(podcast_id: int, voicing: list[asyncio.Task]) -> None:
    """Wait for segments voiced during drafting; the render redoes any that failed."""
    if not voicing:
        return
    outcomes = await asyncio.gather(*voicing, return_exceptions=True)
    failed = [o for o in outcomes if isinstance(o, BaseException)]
    if failed:
        logger.warning(
            "Podcast %s: %d of %d segments failed to voice during drafting; "
            "the render will retry them (%s)",
            podcast_id,
            len(failed),
            len(outcomes),
            failed[0],
        )
