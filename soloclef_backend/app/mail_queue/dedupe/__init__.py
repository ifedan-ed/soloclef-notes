"""Not sending the same email twice.

Two failures, two guards, and they are not interchangeable:

- The same mail handed to the queue twice -- a request that was retried, a
  task that ran again -- is refused at the push (``queued_once``), against the
  key its caller said identifies it.
- A message the mail server already took, whose sender died before it could
  say so, is put back in line at the next start; ``handover_guard`` stops that
  copy going out again.

Neither can swallow a retry. A failed job comes back as the same mail id at
the next attempt: it never passes the push again, and the handover key carries
the attempt.

See ``custom/docs/mail-queue.md``.
"""

from .handover_guard import HandoverGuard
from .keys import CLAIMED, SENT, WINDOW_SECONDS, handover_key, queued_key
from .queued_once import QueuedOnce

__all__ = [
    "CLAIMED",
    "SENT",
    "WINDOW_SECONDS",
    "HandoverGuard",
    "QueuedOnce",
    "handover_key",
    "queued_key",
]
