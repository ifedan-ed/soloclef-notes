"""What stage a headless artifact run is in, and how far along it looks.

The Studio shows these phase names verbatim (the web localizes exactly these
strings), so they are a public contract. Progress only ever moves forward and
never reaches 100 while running; each observed step inside a phase nudges it
toward, but never into, the next phase's band, so a long run keeps moving.
"""

from __future__ import annotations

from dataclasses import dataclass

PREPARING = "preparing"
DRAFTING = "drafting"
RENDERING = "rendering"
VERIFYING = "verifying"
SAVING = "saving"

PHASE_ORDER: tuple[str, ...] = (PREPARING, DRAFTING, RENDERING, VERIFYING, SAVING)

# (first, ceiling): entering a phase jumps to ``first``; nudges stop at ``ceiling``.
PHASE_BANDS: dict[str, tuple[int, int]] = {
    PREPARING: (5, 14),
    DRAFTING: (15, 54),
    RENDERING: (55, 79),
    VERIFYING: (80, 89),
    SAVING: (90, 98),
}

# Tools whose start marks a phase; any other tool only nudges the current one.
TOOL_PHASES: dict[str, str] = {
    "load_artifact_instructions": PREPARING,
    "load_source_document": PREPARING,
    "load_artifact_for_revision": PREPARING,
    "execute": RENDERING,
    "generate_image": RENDERING,
    "verify_artifact": VERIFYING,
    "save_artifact": SAVING,
}

# A share of the room left in the band, so early steps move visibly and later
# ones still move without ever reaching the ceiling in a few steps.
_NUDGE_SHARE = 0.15
# Streamed tokens are far more frequent than steps; this many count as one.
TOKENS_PER_NUDGE = 200


@dataclass(frozen=True)
class ProgressUpdate:
    phase: str
    progress: int


class ArtifactProgressTracker:
    """Folds run events into a monotonic (phase, progress) sequence.

    Every method returns the new state when it moved and ``None`` otherwise.
    """

    def __init__(self) -> None:
        self._phase = PREPARING
        self._progress = PHASE_BANDS[PREPARING][0]
        self._tools_finished = 0
        self._tokens = 0

    @property
    def current(self) -> ProgressUpdate:
        return ProgressUpdate(self._phase, self._progress)

    def advance_to(self, phase: str) -> ProgressUpdate | None:
        """Enter ``phase`` if it lies ahead; a phase already passed only nudges."""
        if PHASE_ORDER.index(phase) <= PHASE_ORDER.index(self._phase):
            return self.nudge()
        self._phase = phase
        self._progress = max(self._progress, PHASE_BANDS[phase][0])
        return self.current

    def nudge(self) -> ProgressUpdate | None:
        ceiling = PHASE_BANDS[self._phase][1]
        if self._progress >= ceiling:
            return None
        step = max(1, round((ceiling - self._progress) * _NUDGE_SHARE))
        self._progress = min(ceiling, self._progress + step)
        return self.current

    def model_step(self) -> ProgressUpdate | None:
        """A top-level model call: the model writing once it has its material.

        The first call only plans which instructions to load, so drafting
        begins after at least one tool has come back.
        """
        self._tokens = 0
        if self._phase == PREPARING and self._tools_finished > 0:
            return self.advance_to(DRAFTING)
        return self.nudge()

    def model_tokens(self, count: int = 1) -> ProgressUpdate | None:
        self._tokens += count
        if self._tokens < TOKENS_PER_NUDGE:
            return None
        self._tokens = 0
        return self.nudge()

    def tool_started(self, name: str) -> ProgressUpdate | None:
        phase = TOOL_PHASES.get(name)
        if phase is None:
            return self.nudge()
        return self.advance_to(phase)

    def tool_finished(self) -> ProgressUpdate | None:
        self._tools_finished += 1
        return self.nudge()
