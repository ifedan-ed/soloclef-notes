"""Report templates: seeded defaults, overridable per workspace.

The seeds are data, not UI strings: the dialog shows whatever this module (or a
workspace's saved edit) returns, and the user can rewrite a template's prompt
before generating.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Literal

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from ..persistence import NotebookReportTemplate

ReportFormat = Literal["interactive", "document"]


@dataclass(frozen=True)
class SeedTemplate:
    key: str
    format: ReportFormat
    title: str
    description: str
    prompt: str


SEED_TEMPLATES: tuple[SeedTemplate, ...] = (
    SeedTemplate(
        key="learning_overview",
        format="interactive",
        title="Learning Overview",
        description="Summarizes key concepts to help you learn the material.",
        prompt=(
            "Build a learning overview: start with the big picture, then explain "
            "each key concept in its own section with a plain-language "
            "explanation, an example and a short self-check question the reader "
            "can reveal. End with a recap of the main takeaways."
        ),
    ),
    SeedTemplate(
        key="briefing_doc",
        format="document",
        title="Briefing Doc",
        description="Overview of the sources featuring key insights and quotes.",
        prompt=(
            "Write a briefing document: an executive summary, the main themes "
            "with the most important facts and short direct quotes from the "
            "sources, and a closing section on open questions or implications."
        ),
    ),
    SeedTemplate(
        key="study_guide",
        format="document",
        title="Study Guide",
        description="Short-answer quiz, essay questions and a glossary of key terms.",
        prompt=(
            "Write a study guide: a short-answer quiz with an answer key, a few "
            "essay-format questions without answers, and a glossary of key "
            "terms with concise definitions."
        ),
    ),
    SeedTemplate(
        key="blog_post",
        format="document",
        title="Blog Post",
        description="An engaging article that makes the sources easy to read.",
        prompt=(
            "Write an engaging, well-structured blog post that explains the most "
            "interesting ideas in the sources for a general audience, with a "
            "clear headline and subheadings."
        ),
    ),
)


@dataclass(frozen=True)
class ReportTemplate:
    key: str
    format: ReportFormat
    title: str
    description: str
    prompt: str
    customized: bool


async def list_templates(
    session: AsyncSession, workspace_id: int
) -> list[ReportTemplate]:
    overrides = {
        row.template_key: row
        for row in (
            await session.scalars(
                select(NotebookReportTemplate).where(
                    NotebookReportTemplate.workspace_id == workspace_id
                )
            )
        ).all()
    }
    templates = []
    for seed in SEED_TEMPLATES:
        row = overrides.get(seed.key)
        templates.append(
            ReportTemplate(
                key=seed.key,
                format=seed.format,
                title=row.title if row else seed.title,
                description=row.description if row else seed.description,
                prompt=row.prompt if row else seed.prompt,
                customized=row is not None,
            )
        )
    return templates


def seed_for(key: str) -> SeedTemplate | None:
    return next((seed for seed in SEED_TEMPLATES if seed.key == key), None)


async def save_template(
    session: AsyncSession,
    workspace_id: int,
    key: str,
    *,
    title: str | None,
    description: str | None,
    prompt: str,
) -> None:
    seed = seed_for(key)
    if seed is None:
        raise KeyError(key)
    row = await session.scalar(
        select(NotebookReportTemplate).where(
            NotebookReportTemplate.workspace_id == workspace_id,
            NotebookReportTemplate.template_key == key,
        )
    )
    if row is None:
        row = NotebookReportTemplate(workspace_id=workspace_id, template_key=key)
        session.add(row)
    row.title = (title or "").strip() or seed.title
    row.description = (
        (description or "").strip() if description is not None else seed.description
    )
    row.prompt = prompt.strip() or seed.prompt
    await session.commit()


async def reset_template(session: AsyncSession, workspace_id: int, key: str) -> None:
    row = await session.scalar(
        select(NotebookReportTemplate).where(
            NotebookReportTemplate.workspace_id == workspace_id,
            NotebookReportTemplate.template_key == key,
        )
    )
    if row is not None:
        await session.delete(row)
        await session.commit()
