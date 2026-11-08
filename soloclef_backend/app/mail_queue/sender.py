"""One mail sender: take a message, deliver it, record what happened.

A sender owns one mailer, so its SMTP session (or HTTP client) stays open
from one message to the next.

Before a message is given to the mail server the sender claims the attempt,
and once the mail server has taken it the sender marks it sent
(``dedupe/handover_guard.py``). That is what stops a message put back by a
restart from going out twice, and the attempt in the key is what keeps a
retry sending.
"""

from __future__ import annotations

import asyncio
import logging
import time
from collections.abc import Callable

from app.mailer.protocol import (
    Mailer,
    MailerMisconfiguredError,
    MailerRejectedError,
    MailerUnavailableError,
)

from .dedupe.handover_guard import HandoverGuard
from .job import MailJob
from .queue_store import QueueStore
from .retry_policy import retry_delay

logger = logging.getLogger(__name__)

# How long one wait for new mail blocks, so a stop request is seen promptly.
TAKE_WAIT_SECONDS = 1.0


class MailSender:
    def __init__(
        self,
        index: int,
        store: QueueStore,
        mailer: Mailer,
        guard: HandoverGuard,
        clock: Callable[[], float] = time.time,
    ) -> None:
        self.index = index
        self._store = store
        self._mailer = mailer
        self._guard = guard
        self._clock = clock

    async def run(self, stop: asyncio.Event) -> None:
        try:
            while not stop.is_set():
                raw = await self._store.take(self.index, TAKE_WAIT_SECONDS)
                if raw is not None:
                    await self.handle(raw)
        finally:
            await self._mailer.close()

    async def handle(self, raw: str) -> str:
        """Deliver one queued message; returns the outcome that was logged."""
        try:
            job = MailJob.from_json(raw)
        except (ValueError, KeyError, TypeError):
            logger.error("Mail sender %s dropped an unreadable queue entry", self.index)
            await self._store.done(self.index, raw)
            return "unreadable"

        tag = f"Mail {job.id} ({job.kind}, ref {job.ref}, try {job.attempt + 1})"
        if job.expired(self._clock()):
            logger.warning("%s dropped unsent: its link or code has expired", tag)
            await self._store.done(self.index, raw)
            return "expired"

        handed_over = await self._guard.claim(job.id, job.attempt)
        if handed_over is not None:
            logger.warning(
                "%s not sent again: this attempt was already %s", tag, handed_over
            )
            await self._store.done(self.index, raw)
            return "duplicate"

        started = time.monotonic()
        try:
            await self._mailer.send(job.message)
        except (MailerRejectedError, MailerMisconfiguredError) as exc:
            logger.error("%s failed permanently, not retried: %s", tag, exc)
            await self._store.done(self.index, raw)
            return "failed"
        except MailerUnavailableError as exc:
            return await self._retry_or_give_up(job, raw, tag, exc)
        except Exception as exc:
            # A bug, not the mail server: retrying the same input would only
            # repeat it. The traceback names no message content.
            logger.exception(
                "%s failed unexpectedly, not retried: %s", tag, type(exc).__name__
            )
            await self._store.done(self.index, raw)
            return "failed"

        # Marked before the queue entry is dropped, so a death between the
        # two leaves a message that is put back and then refused, rather than
        # one put back and sent again.
        await self._guard.mark_sent(job.id, job.attempt)
        await self._store.done(self.index, raw)
        logger.info("%s sent in %.0f ms", tag, (time.monotonic() - started) * 1000)
        return "sent"

    async def _retry_or_give_up(
        self, job: MailJob, raw: str, tag: str, exc: Exception
    ) -> str:
        delay = retry_delay(job.attempt + 1)
        due = self._clock() + (delay or 0)
        if delay is None:
            logger.error("%s failed temporarily and is out of tries: %s", tag, exc)
            await self._store.done(self.index, raw)
            return "gave_up"
        if job.not_after is not None and due >= job.not_after:
            logger.warning(
                "%s failed temporarily (%s); dropped: it expires before the retry",
                tag,
                exc,
            )
            await self._store.done(self.index, raw)
            return "expired"
        logger.warning("%s failed temporarily, retry in %.0f s: %s", tag, delay, exc)
        await self._store.retry_later(
            self.index, raw, job.next_attempt().to_json(), due
        )
        return "retry"
