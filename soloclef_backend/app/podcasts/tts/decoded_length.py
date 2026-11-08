"""How much audio a segment's bytes actually decode to.

A proxy hands back whatever the provider sent, and nothing in the bytes says
whether the transfer finished. Downstream cannot tell either: the renderer
concatenates what it is given and the video holds each slide for its
narration's measured length, so a body that stopped mid-transfer, or an error
page read as raw samples, becomes a slide of silence in a finished video
rather than a failed job.

Decoding is the only honest check. The length is measured the way the video
path measures narration -- by decoding -- rather than trusted from a header,
and a segment shorter than any real utterance is treated as a failed transfer.
"""

from __future__ import annotations

import asyncio

# 16-bit mono at 24 kHz: two bytes per sample.
_DECODE_RATE = 24000
_BYTES_PER_SECOND = _DECODE_RATE * 2

# No spoken line is this short, so anything under it is a transfer that stopped
# or bytes that were never speech, whatever they decoded to.
MINIMUM_AUDIBLE_SECONDS = 0.05


async def audio_seconds(data: bytes, *, headerless_pcm: bool) -> float:
    """Seconds of audio in what the provider sent, measured before transcoding.

    Headerless samples need no decoder -- their length is arithmetic -- and
    measuring them as they arrived is what distinguishes a few bytes of an
    error page read as samples from a real, if short, segment. Re-encoding
    first would hide it, because an encoder pads its last frame.
    """
    if headerless_pcm:
        return len(data) / _BYTES_PER_SECOND
    return await decoded_seconds(data)


async def decoded_seconds(data: bytes) -> float:
    """Seconds of audio ``data`` decodes to, or 0.0 if it does not decode."""
    process = await asyncio.create_subprocess_exec(
        "ffmpeg",
        "-hide_banner",
        "-loglevel",
        "error",
        "-i",
        "pipe:0",
        "-f",
        "s16le",
        "-ar",
        str(_DECODE_RATE),
        "-ac",
        "1",
        "pipe:1",
        stdin=asyncio.subprocess.PIPE,
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )
    out, _err = await process.communicate(data)
    if process.returncode != 0:
        return 0.0
    return len(out) / _BYTES_PER_SECOND
