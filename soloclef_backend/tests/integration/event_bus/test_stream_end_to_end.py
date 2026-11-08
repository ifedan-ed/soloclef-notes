"""The event bus across a process boundary: real Postgres, real Redis, real Go.

What it asserts is that nothing a subscriber can see changed when the bus stopped
being a list of callables. Opt in with a throwaway Postgres and Redis (this test
empties the Redis):

    docker run -d --rm --name eb-redis-test -p 127.0.0.1:26381:6379 redis:8-alpine
    docker run -d --rm --name eb-pg-test -p 127.0.0.1:55439:5432 \
        -e POSTGRES_PASSWORD=postgres -e POSTGRES_USER=postgres \
        -e POSTGRES_DB=surfsense_test pgvector/pgvector:pg17
    EVENT_BUS_INTEGRATION=1 \
        EVENT_BUS_TEST_REDIS_URL=redis://127.0.0.1:26381/0 \
        TEST_DATABASE_URL=postgresql+asyncpg://postgres:postgres@127.0.0.1:55439/surfsense_test \
        uv run pytest tests/integration/event_bus
"""

from __future__ import annotations

import asyncio
import json
import os
import subprocess
import uuid
from pathlib import Path
from typing import Any
from urllib.parse import urlparse

import pytest
import pytest_asyncio
import redis.asyncio as aioredis
from sqlalchemy.ext.asyncio import AsyncSession

from app.automations.persistence.enums.trigger_type import TriggerType
from app.automations.persistence.models.automation import Automation
from app.automations.persistence.models.trigger import AutomationTrigger
from app.automations.triggers.builtin.event.inputs import event_runtime_inputs
from app.automations.triggers.builtin.event.selector import _eligible
from app.automations.triggers.builtin.event.source import on_event
from app.config import config
from app.db import Document, DocumentStatus, DocumentType, Folder, User, Workspace
from app.event_bus.bus import EventBus
from app.event_bus.event import Event
from app.event_bus.publish import publish
from app.event_bus.stream.dispatcher import StreamDispatcher
from app.event_bus.stream.wire import GROUP, STREAM, encode
from app.session_events import register_session_hooks

pytestmark = pytest.mark.integration

if os.getenv("EVENT_BUS_INTEGRATION") != "1":
    pytest.skip(
        "Event bus end to end; set EVENT_BUS_INTEGRATION=1 with Postgres and Redis",
        allow_module_level=True,
    )

REDIS_URL = os.environ["EVENT_BUS_TEST_REDIS_URL"]
GO_TIER = Path(__file__).resolve().parents[4] / "soloclef_api"
DEADLINE_SECONDS = 10.0

if urlparse(REDIS_URL).hostname not in {"127.0.0.1", "localhost"}:
    pytest.exit(
        "EVENT_BUS_TEST_REDIS_URL must be a local throwaway Redis", returncode=2
    )


# --- the stack ------------------------------------------------------------


@pytest.fixture(autouse=True)
def event_stream_redis(monkeypatch):
    monkeypatch.setattr(config, "REDIS_APP_URL", REDIS_URL)


@pytest_asyncio.fixture
async def redis_client():
    client = aioredis.from_url(REDIS_URL, decode_responses=True)
    await client.flushdb()
    yield client
    await client.flushdb()
    await client.aclose()


class _Received:
    """A bus with one subscriber that writes down what it was handed."""

    def __init__(self, *, raises: bool = False) -> None:
        self.bus = EventBus()
        self.events: list[Event] = []
        self.raised = 0
        if raises:
            self.bus.subscribe(self._boom)
        self.bus.subscribe(self._record)

    async def _record(self, event: Event) -> None:
        self.events.append(event)

    async def _boom(self, _event: Event) -> None:
        self.raised += 1
        raise RuntimeError("subscriber blew up")

    async def wait_for(self, count: int, dispatcher: StreamDispatcher) -> None:
        async with asyncio.timeout(DEADLINE_SECONDS):
            while len(self.events) < count:
                await dispatcher.drain_once()
                await asyncio.sleep(0.02)


@pytest_asyncio.fixture
async def dispatching(redis_client):
    """A dispatcher drained by the test, so arrival is observed not slept on."""

    async def build(received: _Received) -> StreamDispatcher:
        dispatcher = StreamDispatcher(
            redis_client, bus=received.bus, consumer="test", block_ms=None
        )
        await dispatcher.ensure_group()
        return dispatcher

    return build


# --- what a document commit publishes ------------------------------------


@pytest.fixture(scope="module", autouse=True)
def session_hooks():
    register_session_hooks()


async def _folder(session: AsyncSession, workspace: Workspace, user: User) -> Folder:
    folder = Folder(
        name="Reports", position="a0", workspace_id=workspace.id, created_by_id=user.id
    )
    session.add(folder)
    await session.flush()
    return folder


async def _document(
    session: AsyncSession, workspace: Workspace, user: User, *, title: str
) -> Document:
    uid = uuid.uuid4().hex
    document = Document(
        title=title,
        document_type=DocumentType.NOTE,
        content="body text",
        content_hash=uid,
        unique_identifier_hash=uid,
        workspace_id=workspace.id,
        created_by_id=user.id,
        folder_id=None,
        status=DocumentStatus.ready(),
    )
    session.add(document)
    await session.flush()
    return document


async def test_a_folder_arrival_committed_reaches_the_subscribers(
    db_session, db_workspace, db_user, dispatching
):
    received = _Received()
    dispatcher = await dispatching(received)
    folder = await _folder(db_session, db_workspace, db_user)
    document = await _document(db_session, db_workspace, db_user, title="Q3 report")
    await db_session.commit()

    document.folder_id = folder.id
    await db_session.commit()

    await received.wait_for(1, dispatcher)
    event = received.events[0]
    assert event.event_type == "document.entered_folder"
    assert event.workspace_id == db_workspace.id
    assert event.payload == {
        "document_id": document.id,
        "folder_id": folder.id,
        "previous_folder_id": None,
        "document_type": "NOTE",
        "title": "Q3 report",
        "connector_id": None,
        "created_by_id": str(db_user.id),
    }


async def test_a_folder_arrival_rolled_back_never_arrives(
    db_session, db_workspace, db_user, dispatching, redis_client
):
    """The flush collected it; the rollback dropped it. Nothing was published."""
    received = _Received()
    dispatcher = await dispatching(received)
    folder = await _folder(db_session, db_workspace, db_user)
    document = await _document(db_session, db_workspace, db_user, title="Doomed")
    await db_session.commit()

    document.folder_id = folder.id
    await db_session.flush()
    await db_session.rollback()

    for _ in range(5):
        await dispatcher.drain_once()
        await asyncio.sleep(0.05)
    assert received.events == []
    assert await redis_client.xlen(STREAM) == 0


# --- the properties the subscribers depend on ----------------------------


async def test_order_holds_when_two_producers_publish(dispatching):
    """Two producers interleave; the stream decides one order and keeps it."""
    received = _Received()
    dispatcher = await dispatching(received)

    async def producer(name: str) -> None:
        for n in range(20):
            await publish("x.happened", {"who": name, "n": n}, workspace_id=1)

    await asyncio.gather(producer("a"), producer("b"))
    await received.wait_for(40, dispatcher)

    for name in ("a", "b"):
        assert [
            e.payload["n"] for e in received.events if e.payload["who"] == name
        ] == (list(range(20)))


async def test_a_subscriber_that_raises_does_not_cost_the_others(dispatching):
    received = _Received(raises=True)
    dispatcher = await dispatching(received)

    await publish("x.happened", {"k": "v"}, workspace_id=1)
    await received.wait_for(1, dispatcher)

    assert received.raised == 1
    assert received.events[0].payload == {"k": "v"}


async def test_a_subscriber_that_raises_does_not_get_the_event_again(
    dispatching, redis_client
):
    """The entry is acknowledged before dispatch, so a failure is not a retry."""
    received = _Received(raises=True)
    dispatcher = await dispatching(received)

    await publish("x.happened", workspace_id=1)
    await received.wait_for(1, dispatcher)
    for _ in range(3):
        await dispatcher.drain_once()

    assert received.raised == 1
    pending = await redis_client.xpending(STREAM, GROUP)
    assert pending["pending"] == 0


async def test_the_same_entry_twice_is_delivered_twice(dispatching, redis_client):
    """A duplicate is a second fact to a subscriber: it has no memory of ids.

    Nothing in this deployment produces one — the stream is appended to once per
    publish and acknowledged once per read — and this is here to record what it
    would cost if anything ever did.
    """
    received = _Received()
    dispatcher = await dispatching(received)
    event = Event(event_type="x.happened", payload={"k": "v"}, workspace_id=1)

    await redis_client.xadd(STREAM, encode(event))
    await redis_client.xadd(STREAM, encode(event))
    await received.wait_for(2, dispatcher)

    assert [e.event_id for e in received.events] == [event.event_id] * 2


# --- what the automation trigger sees ------------------------------------


@pytest_asyncio.fixture
async def event_trigger(db_session, db_workspace, db_user) -> AutomationTrigger:
    automation = Automation(
        workspace_id=db_workspace.id,
        created_by_user_id=db_user.id,
        name="On arrival",
        definition={},
    )
    db_session.add(automation)
    await db_session.flush()
    trigger = AutomationTrigger(
        automation_id=automation.id,
        type=TriggerType.EVENT,
        params={
            "event_type": "document.entered_folder",
            "filter": {"document_type": "NOTE"},
        },
        enabled=True,
    )
    db_session.add(trigger)
    await db_session.flush()
    return trigger


@pytest.fixture
def enqueued(monkeypatch) -> list[dict[str, Any]]:
    """What the bus subscriber handed to Celery, instead of handing it."""
    from app import celery_app as celery_module

    sent: list[dict[str, Any]] = []
    monkeypatch.setattr(
        celery_module.celery_app,
        "send_task",
        lambda name, kwargs=None, **_: sent.append({"name": name, "kwargs": kwargs}),
    )
    return sent


async def test_the_automations_trigger_fires_with_the_same_payload(
    db_session, db_workspace, db_user, dispatching, event_trigger, enqueued
):
    """The real subscriber, the real selector query, the real run inputs."""
    received = _Received()
    received.bus.subscribe(on_event)
    dispatcher = await dispatching(received)
    folder = await _folder(db_session, db_workspace, db_user)
    document = await _document(db_session, db_workspace, db_user, title="Q3 report")
    await db_session.commit()

    document.folder_id = folder.id
    await db_session.commit()
    await received.wait_for(1, dispatcher)

    assert [job["name"] for job in enqueued] == ["automation_event_select"]
    queued = Event.model_validate(enqueued[0]["kwargs"]["event"])
    assert queued == received.events[0]

    assert await _eligible(db_session, event=queued) == [event_trigger]
    inputs = event_runtime_inputs(queued)
    assert inputs["document_id"] == document.id
    assert inputs["folder_id"] == folder.id
    assert inputs["event_type"] == "document.entered_folder"
    assert inputs["event_id"] == queued.event_id
    assert inputs["occurred_at"] == queued.occurred_at.isoformat()


# --- the same event, stated by Go ----------------------------------------


@pytest.fixture(scope="module")
def go_publisher(tmp_path_factory) -> Path:
    """Build the Go tier's publisher once, so each test pays only for a run."""
    binary = tmp_path_factory.mktemp("go") / "publish-event"
    subprocess.run(
        ["go", "build", "-o", str(binary), "./cmd/publish-event"],
        cwd=GO_TIER,
        check=True,
        timeout=300,
    )
    return binary


def _publish_from_go(
    binary: Path, *, event_type: str, workspace_id: int, payload: dict
) -> str:
    completed = subprocess.run(
        [
            str(binary),
            "-redis-url",
            REDIS_URL,
            "-event-type",
            event_type,
            "-workspace-id",
            str(workspace_id),
            "-payload",
            json.dumps(payload),
        ],
        capture_output=True,
        text=True,
        timeout=30,
    )
    assert completed.returncode == 0, completed.stderr
    return completed.stdout.strip()


async def test_a_go_published_event_reaches_the_python_subscribers(
    dispatching, db_workspace, go_publisher
):
    received = _Received()
    dispatcher = await dispatching(received)
    payload = {
        "document_id": 42,
        "folder_id": 7,
        "previous_folder_id": None,
        "document_type": "NOTE",
        "title": "Q3 report",
        "connector_id": None,
        "created_by_id": None,
    }

    entry_id = _publish_from_go(
        go_publisher,
        event_type="document.entered_folder",
        workspace_id=db_workspace.id,
        payload=payload,
    )
    await received.wait_for(1, dispatcher)

    assert entry_id
    event = received.events[0]
    assert event.event_type == "document.entered_folder"
    assert event.workspace_id == db_workspace.id
    assert event.payload == payload


async def test_a_go_published_event_is_indistinguishable_from_a_python_one(
    dispatching, db_workspace, event_trigger, enqueued, db_session, go_publisher
):
    """Same type, same payload, same shape of identity — and the same trigger."""
    received = _Received()
    received.bus.subscribe(on_event)
    dispatcher = await dispatching(received)
    payload = {
        "document_id": 42,
        "folder_id": 7,
        "previous_folder_id": None,
        "document_type": "NOTE",
        "title": "Q3 report",
        "connector_id": None,
        "created_by_id": None,
    }

    _publish_from_go(
        go_publisher,
        event_type="document.entered_folder",
        workspace_id=db_workspace.id,
        payload=payload,
    )
    await publish("document.entered_folder", payload, workspace_id=db_workspace.id)
    await received.wait_for(2, dispatcher)

    from_go, from_python = received.events
    ignore = {"event_id", "occurred_at"}
    assert from_go.model_dump(exclude=ignore) == from_python.model_dump(exclude=ignore)
    assert len(from_go.event_id) == len(from_python.event_id) == 32
    assert from_go.occurred_at.tzinfo is not None

    queued = [Event.model_validate(job["kwargs"]["event"]) for job in enqueued]
    assert queued == [from_go, from_python]
    for event in queued:
        assert await _eligible(db_session, event=event) == [event_trigger]
        assert event_runtime_inputs(event)["document_id"] == 42
