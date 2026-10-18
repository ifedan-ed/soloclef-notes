"""``AgentTaskActionParams`` — params for the ``agent_task`` action type."""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, ConfigDict, Field

from app.schemas.new_chat import MentionedDocumentInfo


class AgentTaskActionParams(BaseModel):
    """Run a multi_agent_chat turn from an automation step."""

    model_config = ConfigDict(extra="forbid")

    query: str = Field(
        ...,
        min_length=1,
        description="User query for the agent; rendered at execute time.",
    )
    auto_approve_all: bool = Field(
        default=False,
        description="If true, every HITL approval is auto-approved; otherwise rejected.",
    )

    # @-mention references chosen in the task input. Mirror the ``new_chat``
    # request fields (minus SurfSense product docs) so the run can scope
    # retrieval to the user's selected files / folders / connectors. All
    # optional and additive; a task with no mentions behaves as before.
    mentioned_document_ids: list[int] | None = Field(
        default=None,
        description="Knowledge-base document IDs the task references with @.",
    )
    mentioned_folder_ids: list[int] | None = Field(
        default=None,
        description="Knowledge-base folder IDs the task references with @.",
    )
    mentioned_connector_ids: list[int] | None = Field(
        default=None,
        description="Concrete connector account IDs the task references with @.",
    )
    mentioned_connectors: list[MentionedDocumentInfo] | None = Field(
        default=None,
        description="Display/context metadata for the @-mentioned connector accounts.",
    )
    mentioned_documents: list[MentionedDocumentInfo] | None = Field(
        default=None,
        description=(
            "Chip metadata (id, title, kind, ...) for every @-mention so the "
            "run can resolve titles to virtual paths and substitute them in "
            "the query."
        ),
    )

    # What the run may read. ``documents`` (the default) is the workspace's
    # documents only; ``web`` adds the public web and connected apps (Tavily,
    # Exa, Browserbase...) and ``all`` is both. @-mentioning a connected
    # account widens a documents-only step to ``all``, as in chat.
    retrieval_scope: Literal["documents", "web", "all"] | None = Field(
        default=None,
        description="documents | web | all; unset means documents.",
    )
    # Scope the step to one notebook: it reads only that notebook's switched-on
    # sources, the way the notebook's own chat does.
    notebook_id: int | None = Field(
        default=None,
        description="Notebook whose sources this step answers from.",
    )

    # A separate field from ``notebook_id`` on purpose: scoping retrieval to a
    # notebook and saving the output to a notebook are different choices (a
    # step might read notebook A and file the answer in notebook B, or save
    # to a notebook without scoping to one at all), and every automation that
    # already sets ``notebook_id`` for retrieval would otherwise start
    # writing notes it never did before.
    save_to_notebook_id: int | None = Field(
        default=None,
        description=(
            "Notebook to save this step's final output to, as a note "
            "titled from the automation's name and the run's local date. "
            "Unset: unchanged, nothing is saved."
        ),
    )
