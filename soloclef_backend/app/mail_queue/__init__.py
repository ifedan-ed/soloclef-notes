"""Email leaves through a queue, never from the request that asked for it.

A feature calls ``enqueue_email``: the message is written to Redis (which
persists it, ``appendonly yes``) and the request answers at once. The
``mail_sender`` service runs ``MAIL_SENDERS`` senders that take messages off
the queue and deliver them, each over its own kept-open connection.

- A caller that says what identifies its mail (``idempotency_key``) gets one
  email per identity, and a message the mail server already took is never
  handed over twice (``dedupe/``). A retry always goes through.
- A message carrying a link or code is dropped unsent once that has expired.
- Temporary failures are retried with growing delays (``retry_policy.py``);
  permanent ones (address refused, our credentials refused) are not.
- Every outcome is logged with the message's id and kind. Addresses, links,
  codes and tokens are never logged.

See ``custom/docs/mail-queue.md``.
"""

from .enqueue import MailQueueUnavailableError, enqueue_email

__all__ = ["MailQueueUnavailableError", "enqueue_email"]
