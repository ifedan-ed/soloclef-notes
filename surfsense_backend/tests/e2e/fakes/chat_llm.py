from __future__ import annotations

import json
import re
from collections.abc import AsyncIterator
from typing import Any, Self

from langchain_core.callbacks import (
    AsyncCallbackManagerForLLMRun,
    CallbackManagerForLLMRun,
)
from langchain_core.language_models import BaseChatModel
from langchain_core.messages import AIMessage, AIMessageChunk, BaseMessage
from langchain_core.outputs import ChatGeneration, ChatGenerationChunk, ChatResult

DRIVE_CANARY_TOKEN = "SURFSENSE_E2E_CANARY_TOKEN_DRIVE_001"
DRIVE_PDF_CANARY_TOKEN = "SURFSENSE_E2E_CANARY_TOKEN_DRIVE_PDF_001"
DRIVE_PDF_CANARY_FILE = "e2e-canary.pdf"
MANUAL_UPLOAD_MD_CANARY_TOKEN = "E2E-MANUAL-UPLOAD-MD-CANARY-7f3a"
MANUAL_UPLOAD_MD_CANARY_FILE = "canary.md"
MANUAL_UPLOAD_PDF_CANARY_TOKEN = "E2E-MANUAL-UPLOAD-PDF-CANARY-9d2b"
MANUAL_UPLOAD_PDF_CANARY_FILE = "canary.pdf"
NO_RELEVANT_CONTENT_SENTINEL = "No relevant indexed content found."
NO_RELEVANT_CONTENT_QUERY = "E2E_NO_RELEVANT_CONTENT_SMOKE"
WEB_ANSWER_QUERY = "E2E_ANSWER"
WEB_ANSWER_TEXT = "E2E notebook answer: the seeded source explains the canary topic."
# A Studio viewer's "Explain" (flashcard, quiz, report passage): the English
# templates' closing lines (surfsense_web/messages/en.json "explain").
EXPLAIN_ENDINGS = (
    "Explain this topic in more detail.",
    "Help me understand more about this topic.",
    "Explain this in more detail.",
)
EXPLAIN_ANSWER_TEXT = "E2E explain answer: the notebook sources cover this in depth."
# Notebook scope: the main agent delegates to the knowledge_base subagent,
# which runs the real search_knowledge_base; the answer lists every SCOPE-*
# canary the search returned, so a spec can see what was reachable.
KB_SCOPE_QUERY = "E2E_KB_SCOPE"
KB_SCOPE_ANSWER = "E2E scope answer:"
_KB_SCOPE_CANARY = re.compile(r"SCOPE-[A-Z]+-\d+")
# Sources only: the main agent tries to delegate to the google_search
# specialist anyway, and the answer says whether the turn let it. A notebook
# chat must report "blocked" whatever scope the client sent; a Tools-menu pick
# of Google Search for that message must report "ran".
WEB_PROBE_QUERY = "E2E_WEB_PROBE"
WEB_PROBE_BLOCKED = "E2E web probe: web blocked"
WEB_PROBE_RAN = "E2E web probe: web ran"
WEB_PROBE_SPECIALIST = "E2E web probe specialist reply"
# Automations: the main agent lists the workspace's notebooks with the
# automation-only list_notebooks tool, reports its outcome, then answers with
# the titles. The "needs web" run reports needs_attention; the "asks" run
# ends with a question and reports nothing.
AUTOMATION_NOTEBOOKS_QUERY = "E2E_AUTOMATION_NOTEBOOKS"
AUTOMATION_NOTEBOOKS_ANSWER = "E2E automation notebooks:"
AUTOMATION_NEEDS_WEB_QUERY = "E2E_AUTOMATION_NEEDS_WEB"
AUTOMATION_NEEDS_WEB_REASON = (
    "This task needs the web, and this automation may only read documents."
)
AUTOMATION_ASKS_QUERY = "E2E_AUTOMATION_ASKS"
AUTOMATION_ASKS_ANSWER = "I can't reach the web from here. Would you like me to try?"


def _content_to_text(content: Any) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return " ".join(_content_to_text(item) for item in content)
    if isinstance(content, dict):
        text = content.get("text") or content.get("content")
        if isinstance(text, str):
            return text
        return json.dumps(content, sort_keys=True)
    if content is None:
        return ""
    return str(content)


def _messages_to_text(messages: list[BaseMessage]) -> str:
    return "\n".join(_content_to_text(message.content) for message in messages)


def _contains_any(text: str, needles: tuple[str, ...]) -> bool:
    lowered = text.lower()
    return any(needle.lower() in lowered for needle in needles)


def _latest_tool_message(messages: list[BaseMessage]) -> BaseMessage | None:
    return next(
        (message for message in reversed(messages) if message.type == "tool"), None
    )


def _tool_result(messages: list[BaseMessage], name: str) -> BaseMessage | None:
    """This turn's latest result from the tool called ``name``."""
    for message in reversed(messages):
        if message.type == "human":
            return None
        if message.type == "tool" and getattr(message, "name", None) == name:
            return message
    return None


def _call(name: str, args: dict[str, Any], call_id: str) -> AIMessage:
    return AIMessage(
        content="", tool_calls=[{"name": name, "args": args, "id": call_id}]
    )


def _this_turns_tool_message(messages: list[BaseMessage]) -> BaseMessage | None:
    """The latest tool result after the latest user message (not an earlier turn's)."""
    for message in reversed(messages):
        if message.type == "tool":
            return message
        if message.type == "human":
            return None
    return None


class FakeChatLLM(BaseChatModel):
    @property
    def _llm_type(self) -> str:
        return "e2e-fake-chat"

    # Names of the tools the current agent bound, so a scripted flow can tell
    # the main agent (``task``) from the knowledge-base subagent.
    bound_tool_names: tuple[str, ...] = ()

    def bind_tools(self, tools: Any, **kwargs: Any) -> Self:
        names: list[str] = []
        for tool in tools or []:
            name = getattr(tool, "name", None)
            if name is None and isinstance(tool, dict):
                name = tool.get("name") or (tool.get("function") or {}).get("name")
            if isinstance(name, str):
                names.append(name)
        return self.model_copy(update={"bound_tool_names": tuple(names)})

    def _response_for(self, messages: list[BaseMessage]) -> str:
        latest_human = next(
            (
                _content_to_text(message.content)
                for message in reversed(messages)
                if message.type == "human"
            ),
            "",
        )
        if NO_RELEVANT_CONTENT_QUERY in latest_human:
            return NO_RELEVANT_CONTENT_SENTINEL
        if AUTOMATION_NOTEBOOKS_QUERY in latest_human:
            listed = _tool_result(messages, "list_notebooks")
            titles: list[str] = []
            if listed is not None:
                try:
                    data = json.loads(_content_to_text(listed.content))
                    titles = [n["title"] for n in data.get("notebooks", [])]
                except (ValueError, TypeError, KeyError):
                    titles = []
            return f"{AUTOMATION_NOTEBOOKS_ANSWER} {'; '.join(titles) or 'none'}"
        if AUTOMATION_NEEDS_WEB_QUERY in latest_human:
            return f"I could not finish: {AUTOMATION_NEEDS_WEB_REASON}"
        if AUTOMATION_ASKS_QUERY in latest_human:
            return AUTOMATION_ASKS_ANSWER
        if WEB_PROBE_QUERY in latest_human:
            latest_tool = _this_turns_tool_message(messages)
            if latest_tool is None:
                # The google_search specialist itself (it never scrapes here).
                return WEB_PROBE_SPECIALIST
            tool_text = _content_to_text(latest_tool.content)
            return (
                WEB_PROBE_RAN
                if WEB_PROBE_SPECIALIST in tool_text
                else WEB_PROBE_BLOCKED
            )
        if KB_SCOPE_QUERY in latest_human:
            latest_tool = _latest_tool_message(messages)
            found = sorted(
                set(
                    _KB_SCOPE_CANARY.findall(
                        _content_to_text(latest_tool.content) if latest_tool else ""
                    )
                )
            )
            return f"{KB_SCOPE_ANSWER} {' '.join(found) or 'none'}"
        # Web suite (notebook chat): a cited answer for a chunk id the spec
        # read from the API, or a plain streamed answer.
        cite = re.search(r"E2E_CITE:(\d+)", latest_human)
        if cite:
            return (
                f"{WEB_ANSWER_TEXT} Grounded in the notebook source "
                f"[citation:{cite.group(1)}]."
            )
        if WEB_ANSWER_QUERY in latest_human:
            return WEB_ANSWER_TEXT
        if any(ending in latest_human for ending in EXPLAIN_ENDINGS):
            return EXPLAIN_ANSWER_TEXT

        prompt_text = _messages_to_text(messages)

        wants_drive = _contains_any(
            latest_human,
            ("drive", "file", "e2e-canary.txt"),
        )
        wants_drive_pdf = _contains_any(
            latest_human,
            ("drive pdf", DRIVE_PDF_CANARY_FILE, DRIVE_PDF_CANARY_TOKEN),
        ) or (wants_drive and "pdf" in latest_human.lower())
        wants_manual_upload = _contains_any(
            latest_human,
            (
                "uploaded",
                "manual upload",
                MANUAL_UPLOAD_MD_CANARY_FILE,
                MANUAL_UPLOAD_PDF_CANARY_FILE,
                MANUAL_UPLOAD_MD_CANARY_TOKEN,
                MANUAL_UPLOAD_PDF_CANARY_TOKEN,
            ),
        )
        wants_manual_upload_pdf = wants_manual_upload and _contains_any(
            latest_human,
            ("pdf", MANUAL_UPLOAD_PDF_CANARY_FILE, MANUAL_UPLOAD_PDF_CANARY_TOKEN),
        )
        wants_manual_upload_md = wants_manual_upload and _contains_any(
            latest_human,
            (
                "markdown",
                ".md",
                MANUAL_UPLOAD_MD_CANARY_FILE,
                MANUAL_UPLOAD_MD_CANARY_TOKEN,
            ),
        )
        has_drive_evidence = (
            "e2e-canary.txt" in prompt_text
            or "fake-file-canary" in prompt_text
            or DRIVE_CANARY_TOKEN in prompt_text
        )
        has_drive_pdf_evidence = (
            DRIVE_PDF_CANARY_FILE in prompt_text
            or "fake-file-pdf-native" in prompt_text
            or DRIVE_PDF_CANARY_TOKEN in prompt_text
        )
        has_manual_upload_md_evidence = (
            MANUAL_UPLOAD_MD_CANARY_FILE in prompt_text
            or MANUAL_UPLOAD_MD_CANARY_TOKEN in prompt_text
        )
        has_manual_upload_pdf_evidence = (
            MANUAL_UPLOAD_PDF_CANARY_FILE in prompt_text
            or MANUAL_UPLOAD_PDF_CANARY_TOKEN in prompt_text
        )

        if wants_drive_pdf and has_drive_pdf_evidence:
            return f"Drive PDF content found: {DRIVE_PDF_CANARY_TOKEN}"
        if wants_drive and has_drive_evidence:
            return f"Drive content found: {DRIVE_CANARY_TOKEN}"
        if wants_manual_upload_pdf and has_manual_upload_pdf_evidence:
            return f"Manual upload PDF content found: {MANUAL_UPLOAD_PDF_CANARY_TOKEN}"
        if wants_manual_upload_md and has_manual_upload_md_evidence:
            return f"Manual upload MD content found: {MANUAL_UPLOAD_MD_CANARY_TOKEN}"
        if has_drive_evidence:
            return f"Drive content found: {DRIVE_CANARY_TOKEN}"
        if has_manual_upload_pdf_evidence:
            return f"Manual upload PDF content found: {MANUAL_UPLOAD_PDF_CANARY_TOKEN}"
        if has_manual_upload_md_evidence:
            return f"Manual upload MD content found: {MANUAL_UPLOAD_MD_CANARY_TOKEN}"
        return NO_RELEVANT_CONTENT_SENTINEL

    def _tool_call_message_for(self, messages: list[BaseMessage]) -> AIMessage | None:
        latest_human = next(
            (
                _content_to_text(message.content)
                for message in reversed(messages)
                if message.type == "human"
            ),
            "",
        )
        latest_tool = _latest_tool_message(messages)

        if (
            AUTOMATION_NOTEBOOKS_QUERY in latest_human
            and "list_notebooks" in self.bound_tool_names
        ):
            if _tool_result(messages, "list_notebooks") is None:
                return _call("list_notebooks", {}, "call_e2e_list_notebooks")
            if _tool_result(messages, "report_outcome") is None:
                return _call(
                    "report_outcome",
                    {"status": "done", "reason": "Listed the notebooks."},
                    "call_e2e_report_done",
                )
        if (
            AUTOMATION_NEEDS_WEB_QUERY in latest_human
            and "report_outcome" in self.bound_tool_names
            and _tool_result(messages, "report_outcome") is None
        ):
            return _call(
                "report_outcome",
                {"status": "needs_attention", "reason": AUTOMATION_NEEDS_WEB_REASON},
                "call_e2e_report_needs_web",
            )

        if (
            WEB_PROBE_QUERY in latest_human
            and _this_turns_tool_message(messages) is None
            and "task" in self.bound_tool_names
        ):
            return AIMessage(
                content="",
                tool_calls=[
                    {
                        "name": "task",
                        "args": {
                            "subagent_type": "google_search",
                            "description": WEB_PROBE_QUERY,
                        },
                        "id": "call_e2e_web_probe_task",
                    }
                ],
            )

        if KB_SCOPE_QUERY in latest_human and latest_tool is None:
            if "search_knowledge_base" in self.bound_tool_names:
                return AIMessage(
                    content="",
                    tool_calls=[
                        {
                            "name": "search_knowledge_base",
                            "args": {"query": "SCOPE canary", "top_k": 10},
                            "id": "call_e2e_kb_scope_search",
                        }
                    ],
                )
            if "task" in self.bound_tool_names:
                return AIMessage(
                    content="",
                    tool_calls=[
                        {
                            "name": "task",
                            "args": {
                                "subagent_type": "knowledge_base",
                                "description": f"{KB_SCOPE_QUERY}: list the SCOPE canaries",
                            },
                            "id": "call_e2e_kb_scope_task",
                        }
                    ],
                )

        return None

    def _generate(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: CallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> ChatResult:
        del stop, run_manager, kwargs
        message = self._tool_call_message_for(messages) or AIMessage(
            content=self._response_for(messages), tool_calls=[]
        )
        return ChatResult(generations=[ChatGeneration(message=message)])

    async def _astream(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: AsyncCallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> AsyncIterator[ChatGenerationChunk]:
        del stop, run_manager, kwargs
        tool_call_message = self._tool_call_message_for(messages)
        if tool_call_message:
            for tool_call in tool_call_message.tool_calls:
                yield ChatGenerationChunk(
                    message=AIMessageChunk(
                        content="",
                        tool_call_chunks=[
                            {
                                "name": tool_call["name"],
                                "args": json.dumps(tool_call["args"]),
                                "id": tool_call["id"],
                                "index": 0,
                            }
                        ],
                    )
                )
            return

        yield ChatGenerationChunk(
            message=AIMessageChunk(content=self._response_for(messages))
        )


def fake_create_chat_litellm_from_agent_config(
    *args: Any, **kwargs: Any
) -> FakeChatLLM:
    del args, kwargs
    return FakeChatLLM()


def fake_create_chat_litellm_from_config(*args: Any, **kwargs: Any) -> FakeChatLLM:
    del args, kwargs
    return FakeChatLLM()


def fake_build_profiled_llm(*args: Any, **kwargs: Any) -> FakeChatLLM:
    """Stands in for llm_bundle._build_profiled_llm (the chat stream's model)."""
    del args, kwargs
    return FakeChatLLM()
