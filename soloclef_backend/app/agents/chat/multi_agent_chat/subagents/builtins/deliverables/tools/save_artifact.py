"""Persist Markdown or sandbox-generated files as first-class artifacts."""

from __future__ import annotations

import json
import logging
import shlex
from dataclasses import asdict, replace
from pathlib import PurePosixPath

from langchain.tools import ToolRuntime
from langchain_core.tools import tool

from app.agents.chat.multi_agent_chat.shared.receipts.command import with_receipt
from app.agents.chat.multi_agent_chat.shared.receipts.receipt import make_receipt
from app.artifacts import ArtifactFileInput, ArtifactFileStreamInput, save_artifact
from app.artifacts.infographic.selection import generation_sidecar_path
from app.artifacts.service import ArtifactInputFile
from app.artifacts.verification.formats.base import FormatAdapter
from app.artifacts.verification.formats.registry import (
    get_format_adapter,
    validate_format_path,
)
from app.artifacts.verification.receipt import (
    artifact_path_lock,
    read_receipt,
    receipt_path,
    sha256_bytes,
)
from app.artifacts.verification.save_attempts import (
    clear_refused_saves,
    record_refused_save,
)
from app.capabilities.core import ActivityDescriptor
from app.config import config as app_config
from app.db import shielded_async_session
from app.deliverables.artifact.item_count import (
    ItemCount,
    ItemCountError,
    applies_to,
    enforce_item_count,
)
from app.sandbox import SandboxSession, get_registry

from .thread_resolver import resolve_root_thread_id

logger = logging.getLogger(__name__)


def _required_markdown(value: str | None) -> str:
    if value is None or not value.strip():
        raise ValueError("markdown_representation must not be empty")
    return value


async def _read_artifact_file(
    session: SandboxSession, path: str, role: str, adapter: FormatAdapter
) -> ArtifactFileInput:
    filename = PurePosixPath(path).name
    if not filename:
        raise ValueError(f"Artifact path must name a file: {path}")
    data = await session.read_file(path)
    if not data:
        raise ValueError(f"Artifact file is empty: {path}")
    if len(data) > app_config.ARTIFACT_MAX_FILE_BYTES:
        raise ValueError(
            f"Artifact file {filename} is {len(data)} bytes; limit is "
            f"{app_config.ARTIFACT_MAX_FILE_BYTES} bytes"
        )

    validate_format_path(adapter, path)
    if role == "preview" and adapter.name != "pdf":
        raise ValueError("Artifact previews must be PDF files")
    if role not in {"primary", "preview"}:
        raise ValueError(f"Unsupported artifact file role: {role}")
    mime_type = adapter.mime_type
    return ArtifactFileInput(
        data=data,
        filename=filename,
        mime_type=mime_type,
        role=role,
    )


async def _consume_verification(
    session: SandboxSession, primary_path: str, preview_path: str | None
) -> None:
    paths = [receipt_path(primary_path)]
    if preview_path:
        paths.append(preview_path)
    try:
        # Invalidate first so a failed best-effort unlink cannot replay the receipt.
        await session.write_file(receipt_path(primary_path), b"")
        result = await session.run_command(
            f"rm -f -- {' '.join(shlex.quote(path) for path in paths)}"
        )
        if not result.ok:
            logger.warning("Could not clean consumed artifact verification state")
    except Exception:
        logger.warning(
            "Could not clean consumed artifact verification state", exc_info=True
        )


async def _cleanup_video_workdir(session: SandboxSession, primary_path: str) -> None:
    """Best-effort cleanup after the streamed MP4 is durably committed."""
    sidecar_path = f"{primary_path}.segments.json"
    try:
        metadata = json.loads((await session.read_file(sidecar_path)).decode())
        workdir = PurePosixPath(metadata["render_workdir"])
        workspace = PurePosixPath("/workspace")
        if (
            workdir == workspace
            or not workdir.is_absolute()
            or not workdir.is_relative_to(workspace)
        ):
            raise ValueError("Invalid video render workdir")
        result = await session.run_command(
            f"rm -rf -- {shlex.quote(str(workdir))} "
            f"{shlex.quote(sidecar_path)} {shlex.quote(primary_path)}"
        )
        if not result.ok:
            raise RuntimeError("Sandbox cleanup command failed")
    except (FileNotFoundError, KeyError, UnicodeDecodeError, ValueError):
        logger.warning("Could not resolve video render workdir for cleanup")
    except Exception:
        logger.warning("Could not clean video render workdir", exc_info=True)


async def _cleanup_infographic_staging(
    session: SandboxSession,
    primary_path: str,
) -> None:
    markdown_path = str(PurePosixPath(primary_path).with_suffix(".md"))
    sidecar_path = generation_sidecar_path(primary_path)
    result = await session.run_command(
        "rm -f -- "
        + " ".join(
            shlex.quote(path) for path in (primary_path, markdown_path, sidecar_path)
        )
    )
    if not result.ok:
        logger.warning("Could not clean infographic staging files")


async def _verified_markdown(
    session: SandboxSession,
    verification,
    offered: str | None,
    *,
    adapter_name: str,
) -> str:
    """The Markdown that was verified, whatever the model retyped.

    The receipt binds the hash of the Markdown file as verified. Models asked
    to pass that file's text again drop a trailing newline, re-escape quotes
    or change a dash, and every such save was refused, which is how a mind
    map looped thirteen times. The verified file itself is authoritative.
    """
    expected = verification.markdown_representation_sha256
    if offered is not None and sha256_bytes(offered.encode("utf-8")) == expected:
        return offered
    if verification.markdown_path is not None:
        data = await session.read_file(verification.markdown_path)
        if sha256_bytes(data) == expected:
            if offered is not None:
                logger.info(
                    "[save_artifact] saved the verified %s Markdown; the model's "
                    "copy differed from it",
                    adapter_name,
                )
            return data.decode("utf-8")
    raise ValueError(
        f"The {adapter_name} Markdown changed after verification. "
        "Verify both files again, then save."
    )


def _text_check_warning(provenance: dict | None) -> str | None:
    """A visible warning when an infographic's text did not match its brief."""
    check = (provenance or {}).get("text_check")
    if not isinstance(check, dict) or check.get("passed", True):
        return None
    problems = [*check.get("missing", []), *check.get("garbled", [])][:8]
    score = check.get("score")
    share = f"{score:.0%}" if isinstance(score, int | float) else "some"
    return (
        f"Only {share} of the planned text reads correctly in the image after "
        "one regeneration. Check: " + "; ".join(str(p) for p in problems)
    )


def _is_refusal(exc: Exception) -> bool:
    """A save refused for the file itself, which another verify could repeat."""
    if isinstance(exc, ItemCountError):
        return True
    if not isinstance(exc, ValueError):
        return False
    text = str(exc).lower()
    return any(
        marker in text
        for marker in ("after verification", "verification receipt", "verify this file")
    )


def _public_error(exc: Exception) -> str:
    if isinstance(exc, FileNotFoundError):
        return (
            "The artifact file is missing. Generate it again, then verify and save it."
        )
    if isinstance(exc, PermissionError):
        return "The artifact file could not be accessed in the sandbox."
    if isinstance(exc, TimeoutError):
        return "The sandbox timed out while saving the artifact. Please try again."
    if isinstance(exc, ValueError):
        if "verification receipt" in str(exc).lower():
            return "Verify this file again before presenting it"
        return str(exc)
    return "The artifact could not be saved. Please try again."


def create_save_artifact_tool(workspace_id: int, item_count: ItemCount | None = None):
    """Create the artifact tool with workspace dependencies injected.

    ``item_count`` is set by headless study runs: a flashcards deck or quiz is
    trimmed to its target at save, or refused below its minimum.
    """

    @tool
    async def save_artifact_tool(
        title: str,
        runtime: ToolRuntime,
        markdown_representation: str | None = None,
        path: str | None = None,
        artifact_id: int | None = None,
        expected_generation: int | None = None,
        description: str | None = None,
    ):
        """Save a durable deliverable, or revise an existing generated artifact.

        For Markdown-only work, omit path and pass markdown_representation.
        Generated files must first pass verify_artifact for the exact output
        bytes at path; the backend owns any preview. To revise an artifact, use
        the artifact_id and expected_generation returned by
        load_artifact_for_revision. Changing the title, filename, or design does
        not make a new artifact. Omit artifact_id only for a genuinely new
        deliverable or an explicitly requested separate copy.
        """
        del description
        root_thread_id = resolve_root_thread_id(runtime)
        session_id: str | None = None
        try:
            files: list[ArtifactInputFile] = []
            extra_metadata = None
            if path is not None:
                session = await (await get_registry()).get_session(
                    root_thread_id, workspace_id
                )
                session_id = session.session_id
                lock = artifact_path_lock(session.session_id, path)
                async with lock:
                    verification = await read_receipt(
                        session,
                        app_config.SECRET_KEY,
                        workspace_id=workspace_id,
                        primary_path=path,
                    )
                    primary_adapter = get_format_adapter(verification.format)
                    validate_format_path(primary_adapter, path)
                    reads_verified_markdown = (
                        primary_adapter.requires_markdown_binding
                        and verification.markdown_path is not None
                    )
                    if (
                        primary_adapter.markdown_projection is None
                        and not reads_verified_markdown
                    ):
                        markdown_representation = _required_markdown(
                            markdown_representation
                        )
                    if verification.primary_path != path:
                        raise ValueError(
                            "The artifact changed after verification. Verify it "
                            "again, then save."
                        )
                    if primary_adapter.requires_markdown_binding:
                        expected_markdown_hash = (
                            verification.markdown_representation_sha256
                        )
                        if expected_markdown_hash is None:
                            raise ValueError(
                                "The verification receipt does not bind the "
                                f"{primary_adapter.name} Markdown"
                            )
                        markdown_representation = await _verified_markdown(
                            session,
                            verification,
                            markdown_representation,
                            adapter_name=primary_adapter.name,
                        )
                    primary: ArtifactInputFile
                    if primary_adapter.name == "video":
                        filename = PurePosixPath(path).name
                        if not filename:
                            raise ValueError(f"Artifact path must name a file: {path}")
                        primary = ArtifactFileStreamInput(
                            chunks=session.read_file_stream(path),
                            filename=filename,
                            mime_type=primary_adapter.mime_type,
                            expected_sha256=verification.primary_sha256,
                        )
                    else:
                        primary = await _read_artifact_file(
                            session, path, "primary", primary_adapter
                        )
                        if verification.primary_sha256 != sha256_bytes(primary.data):
                            raise ValueError(
                                "The artifact changed after verification. Verify it "
                                "again, then save."
                            )
                        if item_count is not None and applies_to(primary_adapter.name):
                            primary = replace(
                                primary,
                                data=enforce_item_count(
                                    primary_adapter.name, primary.data, item_count
                                ),
                            )
                        if primary_adapter.markdown_projection is not None:
                            projected_markdown = primary_adapter.markdown_projection(
                                primary.data
                            )
                            if (
                                markdown_representation is not None
                                and markdown_representation != projected_markdown
                            ):
                                raise ValueError(
                                    "markdown_representation is derived from the "
                                    "verified artifact and cannot be overridden"
                                )
                            markdown_representation = projected_markdown
                    preview = (
                        await _read_artifact_file(
                            session,
                            verification.preview_path,
                            "preview",
                            get_format_adapter("pdf"),
                        )
                        if verification.preview_path is not None
                        else None
                    )
                    if preview is not None and (
                        verification.preview_sha256 != sha256_bytes(preview.data)
                    ):
                        raise ValueError(
                            "The preview changed after verification. Verify the "
                            "artifact again, then save."
                        )
                    extra_metadata = {
                        "verification": {
                            "verified": verification.visual != "unavailable",
                            "reason": verification.unavailable_reason,
                        },
                        **(
                            {"generation": verification.provenance}
                            if verification.provenance is not None
                            else {}
                        ),
                    }
                    warning = _text_check_warning(verification.provenance)
                    if warning:
                        extra_metadata["verification"]["warnings"] = [warning]
                    files.append(primary)
                    if preview is not None:
                        files.append(preview)
                    async with shielded_async_session() as db_session:
                        saved = await save_artifact(
                            db_session,
                            workspace_id=workspace_id,
                            thread_id=root_thread_id,
                            tool_call_id=runtime.tool_call_id,
                            title=title,
                            markdown_representation=markdown_representation,
                            files=files,
                            artifact_id=artifact_id,
                            expected_generation=expected_generation,
                            extra_metadata=extra_metadata,
                            format=verification.format,
                            committed_by_turn=True,
                        )
                    await _consume_verification(
                        session, path, verification.preview_path
                    )
                    clear_refused_saves(session.session_id, path)
                    if primary_adapter.name == "video":
                        await _cleanup_video_workdir(session, path)
                    elif primary_adapter.name == "infographic":
                        await _cleanup_infographic_staging(session, path)
            else:
                markdown_representation = _required_markdown(markdown_representation)
                async with shielded_async_session() as db_session:
                    saved = await save_artifact(
                        db_session,
                        workspace_id=workspace_id,
                        thread_id=root_thread_id,
                        tool_call_id=runtime.tool_call_id,
                        title=title,
                        markdown_representation=markdown_representation,
                        files=files,
                        artifact_id=artifact_id,
                        expected_generation=expected_generation,
                        extra_metadata=extra_metadata,
                        committed_by_turn=True,
                    )
            return with_receipt(
                payload=asdict(saved),
                receipt=make_receipt(
                    route="deliverables",
                    type="artifact",
                    operation="generate",
                    status="success",
                    external_id=str(saved.artifact_id),
                    preview=saved.title,
                ),
                tool_call_id=runtime.tool_call_id,
            )
        except Exception as exc:
            error = _public_error(exc)
            logger.exception("[save_artifact] %s", error)
            if path is not None and session_id is not None and _is_refusal(exc):
                # Raises once the same file was refused too often.
                record_refused_save(session_id, path, error)
            return with_receipt(
                payload={"status": "failed", "error": error},
                receipt=make_receipt(
                    route="deliverables",
                    type="artifact",
                    operation="generate",
                    status="failed",
                    error=error,
                ),
                tool_call_id=runtime.tool_call_id,
            )

    # Keep the public tool name frozen even though the Python symbol avoids
    # shadowing the service imported above.
    save_artifact_tool.name = "save_artifact"
    save_artifact_tool.metadata = {
        "activity_descriptor": ActivityDescriptor(
            active_title="Preparing the file",
            completed_title="Presented file",
            category="artifact",
            icon_key="file-output",
            kind="save_artifact",
        ).as_metadata()
    }
    return save_artifact_tool
