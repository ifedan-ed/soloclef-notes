"""Read a YouTube video as its description plus transcript, via ``youtube.scrape``."""

from __future__ import annotations

import re

from app.capabilities.youtube.scrape.definition import YOUTUBE_SCRAPE
from app.capabilities.youtube.scrape.schemas import ScrapeInput

from ..capability_runner import CapabilityRunner
from .fetched import FetchedContent

_SRT_NOISE = re.compile(r"^\d+$|-->")


def transcript_text(srt: str | None) -> str:
    """SRT cues as running text: sequence numbers and timestamps dropped."""
    lines = [
        line.strip()
        for line in (srt or "").splitlines()
        if line.strip() and not _SRT_NOISE.search(line.strip())
    ]
    return " ".join(lines)


def video_markdown(video) -> str:
    parts = [f"# {video.title or 'YouTube video'}"]
    facts = [
        f"Channel: {video.channelName}" if video.channelName else None,
        f"Published: {video.date}" if video.date else None,
        f"Duration: {video.duration}" if video.duration else None,
        f"URL: {video.url}" if video.url else None,
    ]
    parts.append("\n".join(f"- {fact}" for fact in facts if fact))
    if video.text:
        parts.append("## Description\n\n" + video.text.strip())
    transcript = " ".join(
        transcript_text(track.srt) for track in video.subtitles or [] if track.srt
    ).strip()
    if transcript:
        parts.append("## Transcript\n\n" + transcript)
    return "\n\n".join(part for part in parts if part)


async def fetch_youtube_video(
    runner: CapabilityRunner, url: str
) -> FetchedContent | None:
    output = await runner.run(
        YOUTUBE_SCRAPE,
        ScrapeInput(urls=[url], max_results=1, download_subtitles=True),
    )
    video = next((item for item in output.items if item.title or item.text), None)
    if video is None:
        return None
    metadata = {
        key: value
        for key, value in {
            "video_id": video.id,
            "video_title": video.title,
            "channel_name": video.channelName,
            "thumbnail_url": video.thumbnailUrl,
        }.items()
        if value
    }
    return FetchedContent(
        markdown=video_markdown(video), title=video.title, metadata=metadata
    )
