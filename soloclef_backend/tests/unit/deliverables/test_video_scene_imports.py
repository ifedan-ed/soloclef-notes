"""The author prompt states the stagger import the sandbox harness resolves.

DeepSeek's first draft guessed the helper's path and every scene failed
preflight. The prompt now carries one exact line; these tests pin it to the
harness layout it must match.
"""

from __future__ import annotations

import re
from pathlib import Path, PurePosixPath

import pytest

from app.deliverables.video import executor
from app.deliverables.video.scene_imports import (
    SCENE_MODULE_DIR,
    STAGGER_IMPORT,
    STAGGER_MODULE,
)

pytestmark = pytest.mark.unit

_HARNESS = Path(__file__).resolve().parents[4] / "docker" / "sandbox" / "remotion"


def test_author_and_repair_prompts_state_the_exact_import_line() -> None:
    assert STAGGER_IMPORT == 'import {stagger} from "../../stagger";'
    assert STAGGER_IMPORT in executor._AUTHOR_PROMPT
    assert STAGGER_IMPORT in executor._REPAIR_PROMPT
    assert "__STAGGER" not in executor._AUTHOR_PROMPT + executor._REPAIR_PROMPT


def test_import_path_resolves_from_a_scene_module_to_the_helper() -> None:
    specifier = re.search(r'from "([^"]+)"', STAGGER_IMPORT).group(1)
    scene = PurePosixPath(SCENE_MODULE_DIR) / "scene-0.tsx"
    resolved = PurePosixPath(*_normalize(scene.parent / specifier))
    assert resolved == PurePosixPath(STAGGER_MODULE)


@pytest.mark.skipif(not _HARNESS.is_dir(), reason="sandbox harness not in checkout")
def test_harness_writes_scenes_where_the_import_expects_and_exports_stagger() -> None:
    render_utils = (_HARNESS / "render-utils.mjs").read_text()
    parts = SCENE_MODULE_DIR.split("/")
    joined = ", ".join(f'"{part}"' for part in parts)
    assert f"path.join(rootDir, {joined})" in render_utils
    assert "scene-${index}.tsx" in render_utils
    helper = (_HARNESS / f"{STAGGER_MODULE}.ts").read_text()
    assert re.search(r"^export (const|function) stagger\b", helper, re.MULTILINE)


def _normalize(path: PurePosixPath) -> list[str]:
    parts: list[str] = []
    for part in path.parts:
        if part == "..":
            parts.pop()
        elif part != ".":
            parts.append(part)
    return parts


@pytest.mark.skipif(not _HARNESS.is_dir(), reason="sandbox harness not in checkout")
def test_stagger_never_fades_content_out() -> None:
    """The helper once assumed a 10 s scene and faded every staggered element
    out by second 10, so a 30 s narrated scene showed its heading and little
    else for two thirds of its length, and its middle and last stills were
    reviewed as blank."""
    helper = (_HARNESS / f"{STAGGER_MODULE}.ts").read_text()
    exported = helper[helper.index("export function stagger") :]
    assert "exit" not in exported
    assert "createStagger(300)" not in helper.replace(
        "the old default was createStagger(300)", ""
    )


def test_author_and_repair_prompts_carry_the_frame_filling_rules() -> None:
    for prompt in (executor._AUTHOR_PROMPT, executor._REPAIR_PROMPT):
        assert "1920x1080" in prompt
        assert "headings 72-110 px" in prompt
        assert "never fade anything out" in prompt
        assert "__LAYOUT" not in prompt
