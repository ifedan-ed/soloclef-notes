"""Live progress reaches the job without disturbing the run that produces it."""

from __future__ import annotations

from contextlib import asynccontextmanager
from types import SimpleNamespace
from uuid import uuid4

import pytest
from langchain.agents import create_agent
from langchain_core.language_models.fake_chat_models import GenericFakeChatModel
from langchain_core.messages import AIMessage
from langchain_core.tools import tool

from app.agents.chat.multi_agent_chat.subagents.builtins.deliverables import (
    run as deliverables_run,
)
from app.deliverables.artifact import executor
from app.deliverables.artifact.progress import (
    ArtifactProgressObserver,
    ArtifactProgressTracker,
    JobProgressHeartbeat,
    ProgressUpdate,
    job_heartbeat as heartbeat_module,
)
from app.deliverables.jobs.policy import ARTIFACT_KIND

pytestmark = pytest.mark.unit


def _job() -> SimpleNamespace:
    return SimpleNamespace(
        id=21,
        kind=ARTIFACT_KIND,
        workspace_id=3,
        thread_id=55,
        created_by_id=None,
        request={
            "version": 1,
            "output": "quiz",
            "prompt": "Create a quiz.",
            "root_thread_id": 55,
        },
    )


class _ToolCallingFake(GenericFakeChatModel):
    """Replays scripted replies; tool binding is irrelevant to a script."""

    def bind_tools(self, tools, **kwargs):
        return self


def _call(name: str, **args) -> AIMessage:
    return AIMessage(
        content="",
        tool_calls=[{"name": name, "args": args, "id": f"call-{uuid4().hex[:8]}"}],
    )


async def test_the_observer_follows_a_real_agent_loop_through_every_phase():
    inner_model = GenericFakeChatModel(messages=iter([AIMessage(content="img")]))

    @tool
    async def load_artifact_instructions(artifact_type: str) -> str:
        """Load instructions."""
        return "write json, verify, save"

    @tool
    async def execute(code_or_command: str) -> str:
        """Run code."""
        # A model call made inside a tool belongs to the tool's phase.
        await inner_model.ainvoke("render")
        return "exit 0"

    @tool
    async def verify_artifact(path: str) -> str:
        """Verify."""
        return "clean"

    @tool
    async def save_artifact(title: str) -> str:
        """Save."""
        return '{"artifact_id": 7}'

    model = _ToolCallingFake(
        messages=iter(
            [
                _call("load_artifact_instructions", artifact_type="quiz"),
                _call("execute", code_or_command="print(1)"),
                _call("verify_artifact", path="/tmp/q.json"),
                _call("save_artifact", title="Quiz"),
                AIMessage(content="Saved."),
            ]
        )
    )
    agent = create_agent(
        model,
        tools=[load_artifact_instructions, execute, verify_artifact, save_artifact],
    )
    reported: list[ProgressUpdate] = []

    async def report(update):
        reported.append(update)

    observer = ArtifactProgressObserver(ArtifactProgressTracker(), report)
    await agent.ainvoke(
        {"messages": [("user", "make a quiz")]}, config={"callbacks": [observer]}
    )

    phases = [u.phase for u in reported]
    distinct = [p for i, p in enumerate(phases) if i == 0 or phases[i - 1] != p]
    assert distinct == ["preparing", "drafting", "rendering", "verifying", "saving"]
    values = [u.progress for u in reported]
    assert values == sorted(values) and max(values) < 100


async def test_a_failing_reporter_does_not_fail_the_run():
    async def report(_update):
        raise RuntimeError("database down")

    observer = ArtifactProgressObserver(ArtifactProgressTracker(), report)

    await observer.on_tool_start({"name": "execute"}, "", run_id=uuid4())


class _Sessions:
    """A session maker that records every short-lived session it hands out."""

    def __init__(self):
        self.opened = 0
        self.commits = 0
        self.rollbacks = 0

    def __call__(self):
        sessions = self

        @asynccontextmanager
        async def session():
            sessions.opened += 1

            async def commit():
                sessions.commits += 1

            async def rollback():
                sessions.rollbacks += 1

            yield SimpleNamespace(commit=commit, rollback=rollback)

        return session()


def _fake_heartbeat(monkeypatch, result="job"):
    writes = []

    async def heartbeat(_session, job_id, *, phase, progress, task_id):
        writes.append((job_id, phase, progress, task_id))
        if isinstance(result, Exception):
            raise result
        return result

    monkeypatch.setattr(heartbeat_module, "heartbeat_deliverable_job", heartbeat)
    return writes


async def test_the_heartbeat_writes_throttled_updates_in_their_own_sessions(
    monkeypatch,
):
    writes = _fake_heartbeat(monkeypatch)
    sessions = _Sessions()
    now = [0.0]
    beat = JobProgressHeartbeat(
        sessions,
        21,
        task_id="t1",
        clock=lambda: now[0],
        last_written=ProgressUpdate("preparing", 5),
    )

    await beat(ProgressUpdate("preparing", 6))  # too small a move
    await beat(ProgressUpdate("drafting", 15))  # a phase change always lands
    now[0] = 1.0
    await beat(ProgressUpdate("drafting", 21))  # too soon
    now[0] = 3.0
    await beat(ProgressUpdate("drafting", 21))

    assert writes == [(21, "drafting", 15, "t1"), (21, "drafting", 21, "t1")]
    assert sessions.opened == sessions.commits == 2


async def test_a_job_that_stopped_running_gets_no_more_writes(monkeypatch):
    writes = _fake_heartbeat(monkeypatch, result=None)
    sessions = _Sessions()
    beat = JobProgressHeartbeat(sessions, 21, task_id="t1")

    await beat(ProgressUpdate("drafting", 15))
    await beat(ProgressUpdate("rendering", 55))

    assert len(writes) == 1
    assert sessions.rollbacks == 1 and sessions.commits == 0


async def test_a_failed_heartbeat_write_is_swallowed(monkeypatch):
    _fake_heartbeat(monkeypatch, result=RuntimeError("connection reset"))

    await JobProgressHeartbeat(_Sessions(), 21, task_id=None)(
        ProgressUpdate("drafting", 15)
    )


async def test_the_executor_hands_the_run_an_observer_and_finishes_in_saving():
    reported: list[ProgressUpdate] = []

    async def report(update):
        reported.append(update)

    async def run(**kwargs):
        (observer,) = kwargs["callbacks"]
        tool_run = uuid4()
        await observer.on_tool_start({"name": "verify_artifact"}, "", run_id=tool_run)
        await observer.on_tool_end("clean", run_id=tool_run)
        return SimpleNamespace(
            artifacts=[SimpleNamespace(artifact_id=7, format="quiz")], message=""
        )

    await executor.execute_artifact_deliverable(
        object(), _job(), "llm", run_subagent=run, report_progress=report
    )

    assert reported[0].phase == "verifying"
    assert reported[-1].phase == "saving"


async def test_without_a_reporter_the_run_gets_no_callbacks():
    seen = {}

    async def run(**kwargs):
        seen.update(kwargs)
        return SimpleNamespace(
            artifacts=[SimpleNamespace(artifact_id=7, format="quiz")], message=""
        )

    await executor.execute_artifact_deliverable(
        object(), _job(), "llm", run_subagent=run
    )

    assert "callbacks" not in seen


async def _run_subagent_capturing_config(monkeypatch, **extra):
    seen = {}

    class Agent:
        async def ainvoke(self, _input, config):
            seen["config"] = config
            return {"messages": [AIMessage(content="done")]}

    monkeypatch.setattr(
        deliverables_run,
        "build_deliverables_subagent",
        lambda **_k: SimpleNamespace(
            spec={"model": None, "system_prompt": "", "tools": [], "name": "d"}
        ),
    )
    monkeypatch.setattr(deliverables_run, "create_agent", lambda *_a, **_k: Agent())

    async def commit(**_k):
        return None

    from app.agents.chat.multi_agent_chat.main_agent.middleware import (
        knowledge_store_persistence,
    )

    monkeypatch.setattr(knowledge_store_persistence, "commit_turn_working_copy", commit)
    await deliverables_run.run_deliverable_subagent(
        session=object(),
        workspace_id=3,
        thread_id=55,
        prompt="p",
        llm=object(),
        checkpointer=None,
        **extra,
    )
    return seen["config"]


async def test_run_deliverable_subagent_puts_callbacks_in_the_run_config(monkeypatch):
    observer = ArtifactProgressObserver(ArtifactProgressTracker(), lambda _u: None)

    config = await _run_subagent_capturing_config(monkeypatch, callbacks=[observer])

    assert config["callbacks"] == [observer]


async def test_chat_callers_run_with_an_unchanged_config(monkeypatch):
    config = await _run_subagent_capturing_config(monkeypatch)

    assert "callbacks" not in config
