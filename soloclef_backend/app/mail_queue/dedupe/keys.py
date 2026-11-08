"""The dedupe keys, and how long the queue remembers them.

Both languages spell these the same way, because either may be the one
queueing and either may be the one sending (``soloclef_mail``). The window is
a constant here and not a setting for that same reason: two sides reading two
different numbers is a duplicate nobody can see, and a deployment has nothing
to decide about it.
"""

from __future__ import annotations

import hashlib

from ..queue_store import PREFIX

# What a caller said identifies its mail, and which mail took that identity.
QUEUED = f"{PREFIX}:queued"
# Which attempt of which mail has been given to the mail server.
HANDOVER = f"{PREFIX}:handover"

# How long both are remembered. A day is longer than the whole retry ladder
# (about 45 minutes) and than any work that could be repeated and mail the same
# thing again -- a retried request, a task that ran twice, a worker restarted
# mid-run -- and short enough that the two keys per mail cost nothing. Once it
# passes, an identical mail is queued and sent again, and a message recovered
# from a run that has been down longer than that goes out again: forgetting is
# how the ledger stays small, and mail a day late is a different problem.
WINDOW_SECONDS = 86_400

# A sender has taken this attempt and is giving it to the mail server now.
CLAIMED = "claimed"
# The mail server took it. This mark is never undone inside the window.
SENT = "sent"


def queued_key(idempotency_key: str) -> str:
    """Where the mail holding ``idempotency_key`` is recorded.

    Hashed, because a caller's key may be built out of a reset token and a
    Redis key name is not a place to keep one: key names show up in
    ``SCAN``, in ``MONITOR`` and in anything that walks the keyspace.
    """
    digest = hashlib.sha256(idempotency_key.encode("utf-8")).hexdigest()
    return f"{QUEUED}:{digest}"


def handover_key(mail_id: str, attempt: int) -> str:
    """Where one attempt of one mail is recorded.

    The attempt is in the key, which is what keeps a retry sending: a
    temporary failure re-queues the same mail id at the next attempt, so a
    guard keyed on the id alone would turn every transient SMTP failure into a
    mail that never arrives and never errors.
    """
    return f"{HANDOVER}:{mail_id}:{attempt}"
