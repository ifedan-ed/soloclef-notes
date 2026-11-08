"""Putting back what a stopped run's senders were holding.

A message is moved onto its sender's held list in the same operation that takes
it, so nothing is ever on no list and a sender that dies mid-send loses
nothing. The price was that a message already handed to the mail server went
out again on the next start; the send guard is what stops that now, and this is
where the two meet.

A claim with no "sent" mark means a sender was giving the message to the mail
server -- or was, when it died. From here the two cannot be told apart, and the
answer to the ambiguity is to send again: never-sent is worse than twice-sent.
So every claim belonging to recovered mail is dropped, and the mark that the
mail server took it is the one thing recovery leaves alone.
"""

from __future__ import annotations

import logging
from typing import Protocol

from .dedupe.handover_guard import HandoverGuard
from .job import MailJob

logger = logging.getLogger(__name__)


class HeldMail(Protocol):
    """The moves recovery makes, as ``QueueStore`` makes them."""

    async def held_keys(self) -> list[str]: ...

    async def newest_held(self, key: str) -> str | None: ...

    async def put_back(self, key: str) -> str | None: ...


async def requeue_held(queue: HeldMail, guard: HandoverGuard) -> int:
    """Offer every held message again; returns how many went back in line."""
    moved = 0
    for key in await queue.held_keys():
        while True:
            raw = await queue.newest_held(key)
            if raw is None:
                break
            await _drop_stale_claim(guard, raw)
            if await queue.put_back(key) is None:
                break
            moved += 1
    return moved


async def _drop_stale_claim(guard: HandoverGuard, raw: str) -> None:
    try:
        job = MailJob.from_json(raw)
    except (ValueError, KeyError, TypeError):
        # Unreadable, so it holds no claim worth dropping. It goes back in
        # line and a sender logs and drops it, as it always did.
        return
    await guard.release(job.id, job.attempt)
