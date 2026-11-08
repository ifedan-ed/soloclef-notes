"""A headless artifact run's events fold into forward-only Studio phases."""

from __future__ import annotations

import pytest

from app.deliverables.artifact.progress.phases import (
    PHASE_BANDS,
    PHASE_ORDER,
    TOKENS_PER_NUDGE,
    ArtifactProgressTracker,
    ProgressUpdate,
)
from app.deliverables.artifact.progress.throttle import HeartbeatThrottle

pytestmark = pytest.mark.unit


def _replay(events) -> list[ProgressUpdate]:
    tracker = ArtifactProgressTracker()
    out = [tracker.current]
    for event in events:
        kind, *args = event
        update = {
            "model": tracker.model_step,
            "tool": tracker.tool_started,
            "done": tracker.tool_finished,
            "tokens": tracker.model_tokens,
            "phase": tracker.advance_to,
        }[kind](*args)
        if update is not None:
            out.append(update)
    return out


# The shape of a quiz run: plan, load the skill, write, run, verify, repair
# once, reverify, save.
QUIZ_RUN = [
    ("model",),
    ("tool", "load_artifact_instructions"),
    ("done",),
    ("model",),
    ("tool", "execute"),
    ("done",),
    ("model",),
    ("tool", "verify_artifact"),
    ("done",),
    ("model",),
    ("tool", "execute"),
    ("done",),
    ("model",),
    ("tool", "verify_artifact"),
    ("done",),
    ("model",),
    ("tool", "save_artifact"),
    ("done",),
    ("model",),
]


def test_a_quiz_run_walks_every_phase_in_order():
    updates = _replay(QUIZ_RUN)

    phases = [u.phase for u in updates]
    assert [p for i, p in enumerate(phases) if i == 0 or phases[i - 1] != p] == list(
        PHASE_ORDER
    )


def test_progress_never_goes_back_and_stays_below_done():
    updates = _replay(QUIZ_RUN * 3)

    values = [u.progress for u in updates]
    assert values == sorted(values)
    assert all(0 <= v <= 99 for v in values)


def test_every_update_stays_inside_its_phase_band():
    for update in _replay(QUIZ_RUN + [("model",)] * 50):
        first, ceiling = PHASE_BANDS[update.phase]
        assert first <= update.progress <= ceiling


def test_the_planning_call_before_any_tool_is_still_preparing():
    updates = _replay([("model",), ("model",)])

    assert {u.phase for u in updates} == {"preparing"}
    assert updates[-1].progress > updates[0].progress


def test_a_long_phase_keeps_moving_but_never_crosses_its_ceiling():
    tracker = ArtifactProgressTracker()
    tracker.tool_started("load_artifact_instructions")
    tracker.tool_finished()
    tracker.model_step()
    seen = [tracker.current.progress]
    for _ in range(100):
        tracker.model_step()
        seen.append(tracker.current.progress)

    assert tracker.current.phase == "drafting"
    assert seen[1] > seen[0] and seen[5] > seen[1]
    assert max(seen) == PHASE_BANDS["drafting"][1]
    # At the ceiling a step reports nothing rather than a repeat.
    assert tracker.model_step() is None


def test_a_repair_after_verification_does_not_fall_back_to_rendering():
    tracker = ArtifactProgressTracker()
    tracker.advance_to("verifying")
    before = tracker.current.progress

    update = tracker.tool_started("execute")

    assert update is not None and update.phase == "verifying"
    assert update.progress > before


def test_unknown_tools_only_nudge():
    tracker = ArtifactProgressTracker()
    tracker.advance_to("rendering")

    update = tracker.tool_started("read_sandbox_file")

    assert update is not None and update.phase == "rendering"


def test_streamed_tokens_nudge_once_per_batch():
    tracker = ArtifactProgressTracker()
    tracker.advance_to("drafting")

    moved = [tracker.model_tokens() for _ in range(TOKENS_PER_NUDGE * 2)]

    assert sum(u is not None for u in moved) == 2


def test_the_throttle_writes_phase_changes_at_once():
    throttle = HeartbeatThrottle()
    throttle.written(ProgressUpdate("preparing", 5), now=0.0)

    assert throttle.should_write(ProgressUpdate("drafting", 15), now=0.1)


def test_the_throttle_waits_for_a_visible_move_and_a_quiet_interval():
    throttle = HeartbeatThrottle(min_delta=3, min_interval=2.0)
    throttle.written(ProgressUpdate("drafting", 15), now=0.0)

    assert not throttle.should_write(ProgressUpdate("drafting", 17), now=10.0)
    assert not throttle.should_write(ProgressUpdate("drafting", 20), now=1.0)
    assert throttle.should_write(ProgressUpdate("drafting", 20), now=2.5)


def test_the_throttle_writes_the_first_update():
    assert HeartbeatThrottle().should_write(ProgressUpdate("preparing", 5), now=0.0)
