"""How long a finished audio or video file plays, for list meta lines.

Measured with ffprobe from the file itself, never estimated from a script, so
the number shown is the one the player will show. Every probe is best effort:
a file that cannot be read yields ``None`` and the caller shows no duration
rather than failing the generation that produced it.
"""

from __future__ import annotations

import asyncio
import logging
import shlex
import tempfile
from collections.abc import AsyncIterator
from pathlib import Path

from app.sandbox import SandboxSession

logger = logging.getLogger(__name__)

# The artifact metadata key; the studio list and the backfill read the same one.
DURATION_KEY = "duration_seconds"
_PROBE_TIMEOUT_SECONDS = 30
_PROBE_ARGS = (
    "-v",
    "error",
    "-show_entries",
    "format=duration",
    "-of",
    "default=noprint_wrappers=1:nokey=1",
)


def parse_probe_duration(output: str) -> float | None:
    """ffprobe prints the container duration in seconds, or ``N/A``."""
    try:
        value = float(output.strip().splitlines()[0])
    except (IndexError, ValueError):
        return None
    if value != value or value <= 0 or value == float("inf"):
        return None
    return round(value, 1)


async def probe_file_duration(path: Path) -> float | None:
    try:
        process = await asyncio.create_subprocess_exec(
            "ffprobe",
            *_PROBE_ARGS,
            str(path),
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.DEVNULL,
        )
        stdout, _ = await asyncio.wait_for(
            process.communicate(), timeout=_PROBE_TIMEOUT_SECONDS
        )
    except (OSError, TimeoutError):
        logger.warning("Could not probe media duration of %s", path, exc_info=True)
        return None
    if process.returncode != 0:
        return None
    return parse_probe_duration(stdout.decode(errors="replace"))


async def probe_bytes_duration(data: bytes, *, suffix: str) -> float | None:
    with tempfile.NamedTemporaryFile(suffix=suffix) as handle:
        handle.write(data)
        handle.flush()
        return await probe_file_duration(Path(handle.name))


async def probe_stream_duration(
    chunks: AsyncIterator[bytes], *, suffix: str
) -> float | None:
    """Spool a stored file to disk and probe it; MP4 metadata may sit at its end."""
    with tempfile.NamedTemporaryFile(suffix=suffix) as handle:
        async for chunk in chunks:
            handle.write(chunk)
        handle.flush()
        return await probe_file_duration(Path(handle.name))


async def probe_sandbox_duration(session: SandboxSession, path: str) -> float | None:
    try:
        result = await session.run_command(
            "ffprobe " + " ".join(_PROBE_ARGS) + f" {shlex.quote(path)}"
        )
    except Exception:
        logger.warning("Could not probe sandbox media duration", exc_info=True)
        return None
    if result.exit_code != 0:
        return None
    return parse_probe_duration(result.output or "")
