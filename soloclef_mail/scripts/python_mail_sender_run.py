"""Run the app's own mail senders for a few seconds against a throwaway Redis.

This is the other half of the side-by-side proof: mail queued by the Go
producer is drained here, by app/mail_queue's own service, into a throwaway
Mailpit. The settings arrive on stdin as JSON. app.config is stood in for
because the real one loads the embedding stack at import time.
"""

import asyncio
import json
import sys
import types

case = json.load(sys.stdin)

stub = types.ModuleType("app.config")
stub.config = types.SimpleNamespace(
    SMTP_ENABLED=True,
    MAIL_TRANSPORT="smtp",
    MAIL_SENDERS=str(case["senders"]),
    SMTP_HOST=case["smtp_host"],
    SMTP_PORT=case["smtp_port"],
    SMTP_SECURITY="none",
    SMTP_USERNAME="",
    SMTP_PASSWORD="",
    SMTP_TIMEOUT_SECONDS=10,
    SMTP_FROM=case["default_sender"],
    REDIS_APP_URL=case["redis_url"],
)
sys.modules["app.config"] = stub

import redis.asyncio as aioredis

from app.mail_queue import service


async def main():
    client = aioredis.from_url(case["redis_url"], decode_responses=True)
    stop = asyncio.Event()
    task = asyncio.create_task(service.run_senders(client, case["senders"], stop))
    await asyncio.sleep(case["seconds"])
    stop.set()
    await task
    await client.aclose()


asyncio.run(main())
print("drained")
