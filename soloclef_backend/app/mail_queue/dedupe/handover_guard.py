"""One handover per attempt: what has been given to the mail server.

The mark goes down **after** the send returns, not before. A process that dies
mid-send then leaves no mark and its message goes out again, which is the trade
taken deliberately: twice-sent is a second email in an inbox, never-sent is a
locked-out account waiting for a password reset that will not come. What is
left is a window of one Redis ``SET`` between the mail server's acceptance and
the mark, in place of the whole SMTP conversation.

A claim without a mark means a sender is giving the message to the mail server
right now -- or was, when it died. The two cannot be told apart from here, so
the run that recovers held mail drops those claims (``recovery.py``) and the
message is offered again.
"""

from __future__ import annotations

import redis.asyncio as aioredis

from .keys import CLAIMED, SENT, WINDOW_SECONDS, handover_key


class HandoverGuard:
    def __init__(self, client: aioredis.Redis) -> None:
        self._redis = client

    async def claim(self, mail_id: str, attempt: int) -> str | None:
        """Take this attempt to send it.

        Returns None when this sender may send. Otherwise it returns
        ``CLAIMED`` (another sender has it) or ``SENT`` (the mail server
        already took it), and the caller must not send.
        """
        return await self._redis.set(
            handover_key(mail_id, attempt),
            CLAIMED,
            nx=True,
            get=True,
            ex=WINDOW_SECONDS,
        )

    async def mark_sent(self, mail_id: str, attempt: int) -> None:
        """Record that the mail server took it, whatever happens next."""
        await self._redis.set(handover_key(mail_id, attempt), SENT, ex=WINDOW_SECONDS)

    async def release(self, mail_id: str, attempt: int) -> None:
        """Drop a claim whose sender is gone; never a mark that it was sent.

        Read and delete are two commands rather than one script because this
        runs only at the start of a run, before any sender of this run has
        taken a message, and under the one-sender-process rule nothing else is
        touching the key.
        """
        key = handover_key(mail_id, attempt)
        if await self._redis.get(key) == CLAIMED:
            await self._redis.delete(key)
