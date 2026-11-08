"""Every dedupe key app/mail_queue/dedupe writes, for the Go comparison.

Run from soloclef_mail; needs only the repository:

    python3 -I scripts/python_dedupe_keys.py > dedupe/testdata/python_dedupe_keys.json

Both senders drain the same Redis, so a key spelled differently on one side is
a mail that goes out twice and nothing says why. The fixture comes out of the
app's own code rather than being written by hand, as every other comparison in
this module does.
"""

import json
import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
from load_real_code import dedupe_keys

keys = dedupe_keys()

CALLER_KEYS = [
    "password_reset:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
    "invite:7:1762608000.0",
    "automation_result:42:succeeded",
    "",
    "a key with a space and — a dash",
]

MAIL_IDS = ["aaaabbbbccccddddeeeeffff00001111", "ffffffffffffffffffffffffffffffff"]

print(
    json.dumps(
        {
            "window_seconds": keys.WINDOW_SECONDS,
            "claimed": keys.CLAIMED,
            "sent": keys.SENT,
            "queued": [
                {"idempotency_key": k, "key": keys.queued_key(k)} for k in CALLER_KEYS
            ],
            "handover": [
                {
                    "mail_id": mail_id,
                    "attempt": attempt,
                    "key": keys.handover_key(mail_id, attempt),
                }
                for mail_id in MAIL_IDS
                for attempt in (0, 1, 6)
            ],
        },
        indent=2,
        ensure_ascii=False,
    )
)
