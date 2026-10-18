"""Parse a model's reply into a Pydantic shape, tolerating chatty output.

Agent LLMs return JSON wrapped in prose, markdown fences, or reasoning blocks,
so a plain ``model_validate_json`` is unreliable. Centralising the tolerant
parse here keeps every node that asks a model for structured data validating
replies the same way, instead of each one growing its own brace-scanning
fallback and its own idea of what a parse failure looks like.

A reply that still fails is re-asked once, with the model's own reply and the
validation errors handed back: Pydantic's error is already a precise
instruction, and a model shown it usually fixes the reply at once. One retry,
not a loop: a model that cannot satisfy the schema when shown exactly what it
broke is not helped by a third ask.
"""

from __future__ import annotations

import asyncio
import json
import logging
from typing import TYPE_CHECKING, Any, TypeVar

from langchain_core.messages import AIMessage, HumanMessage
from pydantic import BaseModel, ValidationError

from app.utils.content_utils import extract_text_content, strip_markdown_fences

if TYPE_CHECKING:
    from collections.abc import Mapping

    from langchain_core.messages import BaseMessage

logger = logging.getLogger(__name__)

T = TypeVar("T", bound=BaseModel)

# How much of the raw reply to include in logs when a parse fails, so the actual
# malformation is diagnosable without dumping an entire episode's worth of text.
_LOG_SNIPPET_CHARS = 2000
# Validation errors handed back to the model and carried on the exception. The
# offending input is left out on purpose: it can be a whole authored video, and
# echoing it adds tokens without adding information.
_MAX_REPORTED_ERRORS = 12
_MAX_ERROR_CHARS = 300

_CORRECTION_PROMPT = """Your previous reply could not be accepted:

{errors}

Reply again with the corrected JSON object only: no prose, no code fence, no \
commentary. Keep every field that was already valid."""


class StructuredOutputError(RuntimeError):
    """The model reply could not be parsed into the expected shape."""


class _RejectedReplyError(Exception):
    """One reply that did not parse; ``errors`` is written for the model."""

    def __init__(self, errors: str) -> None:
        super().__init__(errors)
        self.errors = errors


async def invoke_json[T: BaseModel](
    llm,
    messages: list[BaseMessage],
    model: type[T],
    *,
    validation_context: Mapping[str, Any] | None = None,
    attempt_timeout: float | None = None,
) -> T:
    """Invoke ``llm`` and validate its reply as ``model``, re-asking once.

    ``validation_context`` reaches the model's validators, so a caller-specific
    rule (say, "exactly as many scenes as were supplied") fails inside
    validation and is retried like any other schema error. ``attempt_timeout``
    bounds each ask separately, so a retry is not starved by a slow first ask.
    A provider error or a timeout propagates untouched: those are the platform
    failing, not the model answering badly.
    """
    context = dict(validation_context) if validation_context is not None else None
    raw = await _ask(llm, messages, attempt_timeout)
    try:
        return _parse(raw, model, context)
    except _RejectedReplyError as first:
        logger.info(
            "Re-asking for %s after a rejected reply: %s",
            model.__name__,
            first.errors[:_LOG_SNIPPET_CHARS],
        )
        retry_messages = [
            *messages,
            AIMessage(content=raw),
            HumanMessage(content=_CORRECTION_PROMPT.format(errors=first.errors)),
        ]

    raw = await _ask(llm, retry_messages, attempt_timeout)
    try:
        return _parse(raw, model, context)
    except _RejectedReplyError as second:
        logger.error(
            "Failed to parse %s after one retry: %s\nRaw reply: %s",
            model.__name__,
            second.errors,
            raw[:_LOG_SNIPPET_CHARS],
        )
        raise StructuredOutputError(
            f"could not parse {model.__name__} from model reply after one retry: "
            f"{second.errors}"
        ) from None


async def _ask(llm, messages: list[BaseMessage], timeout: float | None) -> str:
    call = llm.ainvoke(messages)
    response = await (asyncio.wait_for(call, timeout) if timeout else call)
    return extract_text_content(response.content)


def _parse[T: BaseModel](raw: str, model: type[T], context: dict[str, Any] | None) -> T:
    content = strip_markdown_fences(raw)
    try:
        return model.model_validate_json(content, context=context)
    except (ValidationError, ValueError):
        pass

    start = content.find("{")
    end = content.rfind("}") + 1
    if not 0 <= start < end:
        raise _RejectedReplyError(f"no JSON object was found for {model.__name__}")
    candidate = content[start:end]
    try:
        return model.model_validate_json(candidate, context=context)
    except (ValidationError, ValueError):
        pass
    try:
        # Models occasionally place literal newlines or tabs inside large
        # source-code strings. They are valid string content but must be
        # escaped in strict JSON; decode those controls before validation.
        return model.model_validate(
            json.loads(candidate, strict=False), context=context
        )
    except ValidationError as exc:
        raise _RejectedReplyError(_describe_validation_error(exc)) from exc
    except ValueError as exc:
        raise _RejectedReplyError(f"the reply is not valid JSON: {exc}") from exc


def _describe_validation_error(exc: ValidationError) -> str:
    """One line per error, located by field path, without the offending input."""
    lines = []
    for error in exc.errors(include_url=False, include_input=False)[
        :_MAX_REPORTED_ERRORS
    ]:
        location = ".".join(str(part) for part in error["loc"]) or "(whole reply)"
        lines.append(f"- {location}: {error['msg']}"[:_MAX_ERROR_CHARS])
    hidden = exc.error_count() - len(lines)
    if hidden > 0:
        lines.append(f"- and {hidden} more")
    return "\n".join(lines)


def flatten_to_text(value: Any) -> str:
    """Render a model-supplied value as one line of prose.

    Models asked for a string sometimes answer with an object or a list
    (``{"issue": "Footer clipped", "page": 3}``). The text is meant for a
    person, so keep all of it readable rather than refusing the reply.
    """
    if value is None:
        return ""
    if isinstance(value, str):
        return value
    if isinstance(value, dict):
        parts = [
            f"{key}: {text}"
            for key, item in value.items()
            if (text := flatten_to_text(item))
        ]
        return "; ".join(parts)
    if isinstance(value, list | tuple):
        return "; ".join(text for item in value if (text := flatten_to_text(item)))
    return str(value)
