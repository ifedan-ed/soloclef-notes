"""Propose a podcast brief with the dialog's language and format applied."""

from __future__ import annotations

from sqlalchemy.ext.asyncio import AsyncSession

from app.podcasts.generation.brief.graph import graph as brief_graph
from app.podcasts.generation.brief.state import BriefState
from app.podcasts.persistence import PodcastRepository
from app.podcasts.schemas import PodcastSpec
from app.podcasts.service import preferences_from

from .briefs import AudioPlan


async def propose_audio_spec(
    session: AsyncSession,
    *,
    workspace_id: int,
    plan: AudioPlan,
    language: str | None,
) -> PodcastSpec:
    """The same deterministic brief the chat tool proposes, with overrides.

    Studio has no voice picker, so it does not inherit the last podcast's
    voices: the configured hosts (``TTS_PODCAST_VOICES``, coral and ash) speak.
    The brief graph falls back to a supported language on its own.
    """
    last_language, _last_voices = preferences_from(
        await PodcastRepository(session).latest_with_spec(workspace_id)
    )
    wanted = language or last_language
    voices: list[str] = []
    result = await brief_graph.ainvoke(
        BriefState(),
        config={
            "configurable": {
                "speaker_count": plan.speaker_count,
                "min_seconds": plan.min_seconds,
                "max_seconds": plan.max_seconds,
                "focus": plan.focus,
                "last_used_language": wanted,
                "last_used_voices": voices,
            }
        },
    )
    spec: PodcastSpec = result["spec"]
    return spec.model_copy(update={"style": plan.style})
