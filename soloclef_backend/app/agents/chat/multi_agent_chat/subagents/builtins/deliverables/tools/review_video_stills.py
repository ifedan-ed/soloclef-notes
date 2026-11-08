"""Bounded visual review for Remotion stills."""

from __future__ import annotations

import base64
import io
import json
import logging
from pathlib import PurePosixPath
from typing import Annotated, Any, Literal

from langchain_core.messages import HumanMessage
from pydantic import (
    AliasChoices,
    BaseModel,
    ConfigDict,
    Field,
    field_validator,
    model_validator,
)

from app.sandbox import SandboxSession
from app.utils.structured_output import (
    StructuredOutputError,
    flatten_to_text,
    invoke_json,
)

logger = logging.getLogger(__name__)

_MAX_STILLS = 37
_MAX_IMAGE_BYTES = 5 * 1024 * 1024
_MAX_TOTAL_IMAGE_BYTES = 50 * 1024 * 1024
_MAX_EVIDENCE = 3
_MAX_EVIDENCE_CHARS = 240
_MAX_SUMMARY_CHARS = 500
# Anthropic refuses any image over 2000 px on a side once a request carries
# many images, and the renders produce stills up to 2576 px wide; job 7 failed
# on exactly that at its 31st still. 1568 px is the long edge Anthropic
# recommends anyway: past it the image is downscaled server-side, so larger
# stills only cost upload time and tokens.
_MAX_REVIEW_EDGE_PX = 1568
_VISION_TIMEOUT_SECONDS = 120
_CRITERIA = (
    "clipping",
    "overflow",
    "contrast",
    "hierarchy",
    "blank_frames",
    "safe_margins",
)
_VERDICTS = ("pass", "warning", "blocking")
# Near-misses a reviewer has used for the three verdicts.
_VERDICT_SYNONYMS = {
    "fail": "blocking",
    "failed": "blocking",
    "error": "blocking",
    "ok": "pass",
    "good": "pass",
    "passed": "pass",
    "caution": "warning",
    "warn": "warning",
}
# Keys a reviewer has used for the prose when it answered with an object.
_SUMMARY_KEYS = ("summary", "overall", "text", "message", "notes", "comment")

# Built from the schema's own limits so the prompt and the validator cannot
# drift apart: a reply the prompt allows is a reply the schema accepts.
_EXAMPLE_REPLY = json.dumps(
    {
        **{name: {"verdict": "pass", "evidence": []} for name in _CRITERIA},
        "summary": "One or two plain sentences.",
    }
)
_REVIEW_PROMPT = f"""Review these video stills as one presentation.
Return one strict JSON object and nothing else, in exactly this shape:
{_EXAMPLE_REPLY}
Each of the six criteria ({", ".join(_CRITERIA)}) is an object with:
- "verdict": exactly one of {", ".join(_VERDICTS)};
- "evidence": an array of at most {_MAX_EVIDENCE} short, frame-specific strings,
  each at most {_MAX_EVIDENCE_CHARS} characters. Plain strings, not objects.
"summary" is ONE plain string of at most {_MAX_SUMMARY_CHARS} characters: not an
object, not a list, and no verdict of its own.
Use blocking only when the video is unusable or materially incomplete. Do not
suggest a fixed layout or template."""


class VideoCriterionReview(BaseModel):
    # An extra key a reviewer volunteers is dropped, not fatal: nothing reads it.
    model_config = ConfigDict(extra="ignore")

    verdict: Literal["pass", "warning", "blocking"] = Field(
        validation_alias=AliasChoices("verdict", "status")
    )
    evidence: Annotated[
        list[Annotated[str, Field(max_length=_MAX_EVIDENCE_CHARS)]],
        Field(max_length=_MAX_EVIDENCE),
    ] = Field(default_factory=list)

    @model_validator(mode="before")
    @classmethod
    def _readable_verdict(cls, value: Any) -> Any:
        """Map a near-miss verdict; downgrade an unknown one to a noted warning.

        The verdict is what the executor acts on. A synonym is unambiguous; an
        unrecognised word is not, so it neither blocks the video nor passes
        silently: it becomes a warning that says what the reviewer wrote.
        """
        if isinstance(value, str):
            value = {"verdict": value}
        if not isinstance(value, dict):
            return value
        key = next((k for k in ("verdict", "status") if k in value), None)
        if key is None:
            return value
        raw = value[key]
        verdict = _VERDICT_SYNONYMS.get(_normal_word(raw), _normal_word(raw))
        if verdict in _VERDICTS:
            return {**value, key: verdict}
        evidence = value.get("evidence")
        evidence = evidence if isinstance(evidence, list) else [evidence]
        note = f"Reviewer gave an unrecognised verdict: {flatten_to_text(raw)!r}"
        return {**value, key: "warning", "evidence": [note, *evidence]}

    @field_validator("evidence", mode="before")
    @classmethod
    def _bounded_evidence(cls, value: Any) -> Any:
        """Surplus or over-long evidence is trimmed rather than refused.

        A thorough reviewer used to cost the whole review, and with it the six
        verdicts that are the part anything acts on.
        """
        if value is None:
            return []
        if not isinstance(value, list):
            value = [value]
        texts = [text for item in value if (text := flatten_to_text(item))]
        return [text[:_MAX_EVIDENCE_CHARS] for text in texts[:_MAX_EVIDENCE]]


def _normal_word(value: Any) -> str:
    return value.strip().lower() if isinstance(value, str) else ""


class VideoStillReview(BaseModel):
    # As above: an unknown top-level key is dropped. The six criteria stay
    # required; the summary is prose nothing branches on, so it may be absent.
    model_config = ConfigDict(extra="ignore")

    clipping: VideoCriterionReview
    overflow: VideoCriterionReview
    contrast: VideoCriterionReview
    hierarchy: VideoCriterionReview = Field(
        validation_alias=AliasChoices(
            "hierarchy",
            "visual_hierarchy",
            "visualHierarchy",
        )
    )
    blank_frames: VideoCriterionReview = Field(
        validation_alias=AliasChoices("blank_frames", "blankFrames")
    )
    safe_margins: VideoCriterionReview = Field(
        validation_alias=AliasChoices("safe_margins", "safeMargins")
    )
    summary: Annotated[str, Field(max_length=_MAX_SUMMARY_CHARS)] = ""

    @field_validator("summary", mode="before")
    @classmethod
    def normalize_summary(cls, value: Any) -> Any:
        """Any summary becomes one string within the cap.

        An over-long sentence loses its tail, not the review; an object keeps
        its prose field if it has one, else is flattened to readable text.
        """
        if isinstance(value, dict):
            text = next(
                (value[k] for k in _SUMMARY_KEYS if isinstance(value.get(k), str)),
                None,
            )
            if text is None and isinstance(value.get("evidence"), list):
                text = flatten_to_text(value["evidence"])
            value = text if text is not None else value
        return flatten_to_text(value)[:_MAX_SUMMARY_CHARS]


def _still_path(workdir: PurePosixPath, relative_path: str) -> PurePosixPath:
    candidate = PurePosixPath(relative_path)
    if (
        candidate.is_absolute()
        or ".." in candidate.parts
        or candidate.suffix.lower() not in {".jpg", ".jpeg", ".png"}
    ):
        raise ValueError("still paths must be relative JPEG or PNG paths")
    resolved = workdir / candidate
    if not resolved.is_relative_to(workdir):
        raise ValueError("still path escapes the queued job workdir")
    return resolved


def _validated_still_paths(
    stills: list[str], workdir: PurePosixPath
) -> list[PurePosixPath]:
    if not 1 <= len(stills) <= _MAX_STILLS:
        raise ValueError(f"stills must contain between 1 and {_MAX_STILLS} paths")
    paths = [_still_path(workdir, path) for path in stills]
    if len(set(paths)) != len(paths):
        raise ValueError("still paths must be unique")
    return paths


def _fit_for_review(path: PurePosixPath, data: bytes) -> tuple[str, bytes]:
    """Return ``(media type, bytes)`` no larger than the review edge limit.

    Stills already within the limit pass through untouched. Larger ones are
    downscaled with their aspect ratio kept and re-encoded as JPEG, which the
    reviewer reads as well as PNG at a fraction of the size.
    """
    from PIL import Image, UnidentifiedImageError

    media_type = "png" if path.suffix.lower() == ".png" else "jpeg"
    try:
        with Image.open(io.BytesIO(data)) as image:
            if max(image.size) <= _MAX_REVIEW_EDGE_PX:
                return media_type, data
            image.thumbnail((_MAX_REVIEW_EDGE_PX, _MAX_REVIEW_EDGE_PX))
            out = io.BytesIO()
            image.convert("RGB").save(out, format="JPEG", quality=85)
    except (UnidentifiedImageError, OSError):
        # Undecodable here is not proof it is undecodable to the reviewer;
        # send it as it came rather than fail an advisory step.
        return media_type, data
    return "jpeg", out.getvalue()


async def review_video_stills(
    stills: list[str],
    *,
    session: SandboxSession,
    vision_llm,
    workdir: PurePosixPath,
) -> dict[str, Any]:
    """Review explicit job-owned still paths with an explicit vision model."""
    paths = _validated_still_paths(stills, workdir)

    images = [(path, await session.read_file(str(path))) for path in paths]
    if any(len(data) > _MAX_IMAGE_BYTES for _, data in images):
        raise ValueError("a video still exceeds the 5 MiB review limit")
    if sum(len(data) for _, data in images) > _MAX_TOTAL_IMAGE_BYTES:
        raise ValueError("video stills exceed the 50 MiB review limit")
    if vision_llm is None:
        return {
            "status": "unavailable",
            "reason": "No vision-capable model is configured for this workspace",
        }

    content: list[dict[str, Any]] = [{"type": "text", "text": _REVIEW_PROMPT}]
    for path, data in images:
        media_type, data = _fit_for_review(path, data)
        content.extend(
            (
                {"type": "text", "text": f"Frame: {path.name}"},
                {
                    "type": "image_url",
                    "image_url": {
                        "url": (
                            f"data:image/{media_type};base64,"
                            + base64.b64encode(data).decode("ascii")
                        )
                    },
                },
            )
        )
    try:
        verdict = await invoke_json(
            vision_llm,
            [HumanMessage(content=content)],
            VideoStillReview,
            attempt_timeout=_VISION_TIMEOUT_SECONDS,
        )
    except (StructuredOutputError, TimeoutError) as exc:
        # The review is advisory and runs after the deck is authored and the
        # stills rendered; the caller ships on any status but "reviewed". An
        # unreadable or absent reply must not throw that work away. Provider
        # errors (credit, auth, connection) still propagate: that is the
        # platform failing, and the job's failure classification routes it.
        reason = f"{type(exc).__name__}: {exc}" if str(exc) else type(exc).__name__
        logger.warning("Video still review skipped: %s", reason)
        return {
            "status": "unavailable",
            "reason": f"The visual review could not be read ({reason})"[:500],
        }
    return {"status": "reviewed", "review": verdict.model_dump(mode="json")}
