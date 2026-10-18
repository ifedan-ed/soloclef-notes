"""Run one ``agent_task`` invocation: ainvoke + auto-decision resume loop.

Around the agent turn: the step's retrieval scope (documents, web, or both)
and optional notebook scope, the notebook tools and the outcome tool, the
unattended-run brief, the credit hold (settled or released), and the honest
outcome the run is marked with.
"""

from __future__ import annotations

import time
import uuid
from typing import Any

from langchain_core.messages import HumanMessage
from langgraph.types import Command
from sqlalchemy.ext.asyncio import AsyncSession

from app.agents.chat.document_scope import CONFIG_KEY as DOCUMENT_SCOPE_KEY
from app.agents.chat.multi_agent_chat import create_multi_agent_chat_deep_agent
from app.agents.chat.retrieval_scope import RetrievalScope, SourcesOnly
from app.agents.chat.runtime.mention_resolver import (
    resolve_mentions,
    substitute_in_text,
)
from app.agents.chat.shared.context import SurfSenseContextSchema
from app.auth.context import AuthContext
from app.db import ChatVisibility, User, async_session_maker
from app.schemas.new_chat import MentionedDocumentInfo
from app.workspace_notes.automation_stamp import (
    CONFIG_KEY as NOTE_STAMP_KEY,
    note_stamp,
)

from ...types import ActionContext
from .auto_decide import build_auto_decisions
from .billing import hold_step_credit, release_step_credit, settle_step_credit
from .dependencies import build_dependencies
from .finalize import extract_final_assistant_message
from .notebook_tools import create_notebook_tools
from .outcome import create_report_outcome_tool, decide_outcome

# Cap on HITL resume iterations. The agent should not need this many turns in one
# step; treat overshoot as a runaway and fail the step.
_MAX_RESUMES = 50


def _build_connector_block(connectors: list[dict[str, Any]]) -> str | None:
    """Render the ``<mentioned_connectors>`` context block (same shape as chat).

    Mirrors ``stream_new_chat`` so the agent gets the exact connector accounts
    the user picked. Returns ``None`` when nothing renders.
    """
    lines: list[str] = []
    for connector in connectors:
        connector_id = connector.get("id")
        connector_type = connector.get("connector_type") or connector.get(
            "document_type"
        )
        account_name = connector.get("account_name") or connector.get("title")
        if connector_id is None or connector_type is None:
            continue
        lines.append(
            f'  - connector_id={connector_id}, connector_type="{connector_type}", '
            f'account_name="{account_name or ""}"'
        )
    if not lines:
        return None
    return (
        "<mentioned_connectors>\n"
        "The user selected these exact connector accounts with @. "
        "These entries are selection metadata, not retrieved connector content. "
        "When a connector-backed tool needs an account, use the matching "
        "connector_id from this list if the tool supports connector_id:\n"
        + "\n".join(lines)
        + "\n</mentioned_connectors>"
    )


async def _resolve_mention_context(
    session: AsyncSession,
    *,
    workspace_id: int,
    query: str,
    mentioned_document_ids: list[int] | None,
    mentioned_folder_ids: list[int] | None,
    mentioned_connector_ids: list[int] | None,
    mentioned_connectors: list[MentionedDocumentInfo] | None,
    mentioned_documents: list[MentionedDocumentInfo] | None,
    document_scope: list[int] | None = None,
) -> tuple[str, SurfSenseContextSchema | None]:
    """Resolve @-mentions into a rewritten query + per-invocation context.

    Automation always runs in cloud filesystem mode, so we mirror the chat
    ``new_chat`` flow: substitute ``@title`` tokens with canonical
    ``/documents/...`` paths, prepend a ``<mentioned_connectors>`` block, and
    build a ``SurfSenseContextSchema`` that the ``search_knowledge_base`` tool
    reads via ``runtime.context``. Returns ``(query, None)`` unchanged when
    there are no mentions.
    """
    has_mentions = bool(
        mentioned_document_ids
        or mentioned_folder_ids
        or mentioned_connector_ids
        or mentioned_connectors
        or mentioned_documents
    )
    if not has_mentions:
        if document_scope is None:
            return query, None
        return query, SurfSenseContextSchema(
            workspace_id=workspace_id, document_scope=list(document_scope)
        )

    resolved = await resolve_mentions(
        session,
        workspace_id=workspace_id,
        mentioned_documents=mentioned_documents,
        mentioned_document_ids=mentioned_document_ids,
        mentioned_folder_ids=mentioned_folder_ids,
    )
    agent_query = substitute_in_text(query, resolved.token_to_path)

    # ``SurfSenseContextSchema.mentioned_connectors`` is typed ``list[dict]`` and
    # the connector block reads dicts, so dump the pydantic chips once.
    connector_dicts = [c.model_dump() for c in (mentioned_connectors or [])]
    connector_block = _build_connector_block(connector_dicts)
    if connector_block:
        agent_query = f"{connector_block}\n\n<user_query>{agent_query}</user_query>"

    runtime_context = SurfSenseContextSchema(
        workspace_id=workspace_id,
        mentioned_document_ids=list(
            resolved.mentioned_document_ids or (mentioned_document_ids or [])
        ),
        mentioned_folder_ids=list(
            resolved.mentioned_folder_ids or (mentioned_folder_ids or [])
        ),
        mentioned_connector_ids=list(mentioned_connector_ids or []),
        mentioned_connectors=connector_dicts,
        document_scope=None if document_scope is None else list(document_scope),
    )
    return agent_query, runtime_context


def resolve_retrieval_scope(
    requested: str | None,
    *,
    mentions_connector: bool,
    needs_documents: bool,
) -> RetrievalScope:
    """The scope a step runs with.

    Unset means documents. A connected account @-mentioned in the task needs
    the web side, so a documents-only step widens to ``all`` (chat does the
    same); a web-only step that also names documents or a notebook widens to
    ``all`` so those can still be read.
    """
    scope = RetrievalScope(requested) if requested else RetrievalScope.DOCUMENTS
    if mentions_connector and not scope.allows_web:
        scope = RetrievalScope.ALL
    if needs_documents and not scope.allows_documents:
        scope = RetrievalScope.ALL
    return scope


def automation_brief(
    scope: RetrievalScope,
    *,
    notebook_title: str | None = None,
    save_to_notebook_title: str | None = None,
) -> str:
    """What the agent must know about running unattended (prepended to the task)."""
    if scope.allows_web and scope.allows_documents:
        reach = (
            "This automation may read the workspace's documents, the public "
            "web and connected apps (web search, page readers such as "
            "Browserbase)."
        )
    elif scope.allows_web:
        reach = (
            "This automation may use the public web and connected apps, not "
            "the workspace's documents."
        )
    else:
        reach = (
            "This automation may read the workspace's documents only: web "
            "search and connected apps are switched off for it. If the task "
            "needs the web, report needs_attention and say that the "
            "automation's 'Can use' setting must include the web."
        )
    lines = [
        "<automation_run>",
        "This is an unattended automation run. Nobody reads questions or "
        "approves anything while it runs, so never ask: decide, or report "
        "that a person has to act.",
        reach,
        "Notebooks are not files. For anything about notebooks use "
        "list_notebooks and list_notebook_sources; files in /documents named "
        "like 'Quiz_ ...', 'Mind Map_ ...' or 'Report_ ...' are Studio "
        "outputs, not notebooks. To read a notebook's sources, give their "
        "paths from list_notebook_sources to the knowledge_base agent.",
    ]
    if notebook_title is not None:
        lines.append(
            f"This step is scoped to the notebook {notebook_title!r}: only its "
            "switched-on sources can be read."
        )
    if save_to_notebook_title is not None:
        # Without this the agent also writes the result as a file in
        # /documents when the task says "save it as a note", and the person
        # gets the same note twice, once where no notebook shows it.
        lines.append(
            "Your final answer is saved automatically as a note in the "
            f"notebook {save_to_notebook_title!r}. When the task says to save "
            "or keep the result as a note, that is what it means: do not "
            "write it to a file or folder yourself; give the whole result as "
            "your final answer."
        )
    lines.append(
        "Before your final answer call report_outcome once: 'done' if the "
        "task was completed, otherwise 'failed' or 'needs_attention' with the "
        "reason. Never present an unfinished task as done."
    )
    lines.append("</automation_run>")
    return "\n".join(lines)


async def run_agent_task(
    *,
    ctx: ActionContext,
    query: str,
    auto_approve_all: bool,
    mentioned_document_ids: list[int] | None = None,
    mentioned_folder_ids: list[int] | None = None,
    mentioned_connector_ids: list[int] | None = None,
    mentioned_connectors: list[MentionedDocumentInfo] | None = None,
    mentioned_documents: list[MentionedDocumentInfo] | None = None,
    retrieval_scope: str | None = None,
    notebook_id: int | None = None,
    save_to_notebook_id: int | None = None,
) -> dict[str, Any]:
    """Invoke multi_agent_chat for one rendered query and return its outcome.

    Opens its own DB session so the executor's bookkeeping session isn't tied
    up for the entire invocation. The LangGraph ``thread_id`` (a fresh UUID)
    is returned as ``agent_session_id`` for later inspection.

    @-mentions (files / folders / connectors) chosen in the task input are
    resolved the same way the chat flow does and forwarded to the agent via the
    per-invocation ``context`` so they actually scope retrieval.

    The result carries ``outcome`` (``succeeded`` / ``failed`` /
    ``needs_attention`` and a reason); the step runner marks the step by it.
    """
    agent_session_id = str(uuid.uuid4())
    user_id = str(ctx.creator_user_id) if ctx.creator_user_id else None
    decision = "approve" if auto_approve_all else "reject"

    async with async_session_maker() as agent_session:
        auth_context = None
        if ctx.creator_user_id:
            user = await agent_session.get(User, ctx.creator_user_id)
            if user is not None:
                auth_context = AuthContext.system(user, source="automation")

        pins = None
        if notebook_id is not None:
            from app.notebooks.chat.pins import load_notebook_pins_by_id

            pins = await load_notebook_pins_by_id(
                agent_session, notebook_id=notebook_id, workspace_id=ctx.workspace_id
            )
            if pins is None:
                raise ValueError(
                    f"The notebook this step is scoped to ({notebook_id}) no "
                    "longer exists. Edit the automation and pick another."
                )

        save_to_notebook_title = None
        if save_to_notebook_id is not None:
            from .save_to_notebook import load_save_target

            save_to_notebook_title = (
                await load_save_target(
                    agent_session,
                    notebook_id=save_to_notebook_id,
                    workspace_id=ctx.workspace_id,
                )
            ).title

        scope = resolve_retrieval_scope(
            retrieval_scope,
            mentions_connector=bool(mentioned_connector_ids or mentioned_connectors),
            needs_documents=bool(
                pins is not None
                or mentioned_document_ids
                or mentioned_folder_ids
                or mentioned_documents
            ),
        )
        # A notebook step without the web reads its sources only (no web
        # specialist is built); with the web, the documents side stays limited
        # to the notebook by the document scope below.
        sources_only = (
            SourcesOnly() if pins is not None and not scope.allows_web else None
        )
        document_scope = pins.scope_document_ids if pins is not None else None

        deps = await build_dependencies(
            session=agent_session,
            workspace_id=ctx.workspace_id,
            chat_model_id=ctx.chat_model_id,
            image_gen_model_id=ctx.image_gen_model_id,
            vision_model_id=ctx.vision_model_id,
            user_id=ctx.creator_user_id,
            seed=ctx.workspace_id,
        )

        # Priced like a chat message on this model; refused before any model
        # call when live billing says no.
        reservation = await hold_step_credit(
            agent_config=deps.agent_config,
            user_id=user_id,
            workspace_id=ctx.workspace_id,
        )
        try:
            result, final_message, resumes, declined = await _invoke(
                ctx=ctx,
                agent_session=agent_session,
                deps=deps,
                user_id=user_id,
                auth_context=auth_context,
                decision=decision,
                agent_session_id=agent_session_id,
                query=query,
                scope=scope,
                sources_only=sources_only,
                document_scope=document_scope,
                notebook_title=pins.title if pins is not None else None,
                save_to_notebook_title=save_to_notebook_title,
                mentioned_document_ids=mentioned_document_ids,
                mentioned_folder_ids=mentioned_folder_ids,
                mentioned_connector_ids=mentioned_connector_ids,
                mentioned_connectors=mentioned_connectors,
                mentioned_documents=mentioned_documents,
            )
            if save_to_notebook_id is not None and final_message:
                from .save_to_notebook import save_run_output_as_note

                await save_run_output_as_note(
                    agent_session,
                    notebook_id=save_to_notebook_id,
                    workspace_id=ctx.workspace_id,
                    automation_name=ctx.automation_name or "Automation",
                    content=final_message,
                    timezone=ctx.run_timezone,
                    created_by_id=ctx.creator_user_id,
                    automation_id=ctx.automation_id,
                )
                await agent_session.commit()
        except BaseException as exc:
            # Raised, timed out (cancelled by the step timeout) or refused:
            # no answer was delivered, so the hold goes back.
            await release_step_credit(
                reservation, reason=f"automation step ended: {type(exc).__name__}"
            )
            raise

    outcome = decide_outcome(result, final_message, declined_actions=declined)
    await settle_step_credit(
        reservation,
        details={
            "source": "automation",
            "automation_run_id": ctx.run_id,
            "step_id": ctx.step_id,
            "outcome": outcome.status,
        },
    )
    return {
        "agent_session_id": agent_session_id,
        "final_message": final_message,
        "resumes": resumes,
        "retrieval_scope": scope.value,
        "outcome": outcome.as_dict(),
    }


async def _invoke(
    *,
    ctx: ActionContext,
    agent_session: AsyncSession,
    deps: Any,
    user_id: str | None,
    auth_context: AuthContext | None,
    decision: str,
    agent_session_id: str,
    query: str,
    scope: RetrievalScope,
    sources_only: SourcesOnly | None,
    document_scope: list[int] | None,
    notebook_title: str | None,
    save_to_notebook_title: str | None,
    mentioned_document_ids: list[int] | None,
    mentioned_folder_ids: list[int] | None,
    mentioned_connector_ids: list[int] | None,
    mentioned_connectors: list[MentionedDocumentInfo] | None,
    mentioned_documents: list[MentionedDocumentInfo] | None,
) -> tuple[Any, str | None, int, int]:
    """The agent turn plus its approval loop: ``(result, final message,
    resumes, actions declined)``."""
    agent = await create_multi_agent_chat_deep_agent(
        llm=deps.llm,
        workspace_id=ctx.workspace_id,
        db_session=agent_session,
        connector_service=deps.connector_service,
        checkpointer=deps.checkpointer,
        user_id=user_id,
        thread_id=None,
        agent_config=deps.agent_config,
        thread_visibility=ChatVisibility.PRIVATE,
        mentioned_document_ids=mentioned_document_ids,
        image_gen_model_id=ctx.image_gen_model_id,
        auth_context=auth_context,
        additional_tools=[
            *create_notebook_tools(workspace_id=ctx.workspace_id),
            create_report_outcome_tool(),
        ],
        retrieval_scope=scope,
        sources_only=sources_only,
    )

    agent_query, runtime_context = await _resolve_mention_context(
        agent_session,
        workspace_id=ctx.workspace_id,
        query=query,
        mentioned_document_ids=mentioned_document_ids,
        mentioned_folder_ids=mentioned_folder_ids,
        mentioned_connector_ids=mentioned_connector_ids,
        mentioned_connectors=mentioned_connectors,
        mentioned_documents=mentioned_documents,
        document_scope=document_scope,
    )
    brief = automation_brief(
        scope,
        notebook_title=notebook_title,
        save_to_notebook_title=save_to_notebook_title,
    )
    agent_query = f"{brief}\n\n{agent_query}"

    request_id = f"automation:{ctx.run_id}:{ctx.step_id}"
    turn_id = f"{request_id}:{int(time.time() * 1000)}"
    input_state: dict[str, Any] = {
        "messages": [HumanMessage(content=agent_query)],
        "workspace_id": ctx.workspace_id,
        "request_id": request_id,
        "turn_id": turn_id,
    }
    configurable: dict[str, Any] = {
        "thread_id": agent_session_id,
        "request_id": request_id,
        "turn_id": turn_id,
    }
    if document_scope is not None:
        # Every nested subagent inherits the parent config, so the notebook
        # scope reaches the knowledge-base agent too (see document_scope).
        configurable[DOCUMENT_SCOPE_KEY] = sorted(document_scope)
    if ctx.automation_id is not None:
        # The note files this run's agent writes say which automation and run
        # wrote them (the Notes page's "By <automation>"); stamped when the
        # turn persists them, so nothing else written meanwhile is claimed.
        configurable[NOTE_STAMP_KEY] = note_stamp(
            automation_id=ctx.automation_id,
            automation_name=ctx.automation_name or "Automation",
            run_id=ctx.run_id,
        )
    config: dict[str, Any] = {
        "configurable": configurable,
        "recursion_limit": 10_000,
    }
    if runtime_context is not None:
        runtime_context.request_id = request_id
        runtime_context.turn_id = turn_id

    # The compiled graph declares ``context_schema=SurfSenseContextSchema``;
    # mentions only reach the ``search_knowledge_base`` tool via ``context=``.
    invoke_kwargs: dict[str, Any] = {"config": config}
    if runtime_context is not None:
        invoke_kwargs["context"] = runtime_context

    result = await agent.ainvoke(input_state, **invoke_kwargs)

    resumes = 0
    declined = 0
    while True:
        state = await agent.aget_state(config)
        if not getattr(state, "interrupts", None):
            break
        if resumes >= _MAX_RESUMES:
            raise RuntimeError(
                f"agent_task exceeded {_MAX_RESUMES} HITL resume iterations"
            )
        lg_resume_map, routed = build_auto_decisions(state, decision)
        if decision == "reject":
            declined += sum(len(v["decisions"]) for v in lg_resume_map.values())
        config["configurable"]["surfsense_resume_value"] = routed
        result = await agent.ainvoke(Command(resume=lg_resume_map), **invoke_kwargs)
        resumes += 1

    return result, extract_final_assistant_message(result), resumes, declined
