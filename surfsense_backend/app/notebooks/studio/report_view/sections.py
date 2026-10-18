"""A Markdown report split into the sections its table of contents lists.

The web viewer splits the same Markdown the same way
(``surfsense_web/features/notebooks/lib/report-outline.ts``), so a suggestion
stored against section ``n`` sits under the section the reader sees as ``n``.
Both follow one rule: a leading ``#`` heading is the title; the table of
contents is the shallowest heading level that occurs at least twice after it
(or the shallowest one present, when none repeats); headings inside fenced
code are not headings.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field

_HEADING = re.compile(r"^(#{1,6})[ \t]+(.+?)[ \t]*#*[ \t]*$")
_FENCE = re.compile(r"^[ \t]{0,3}(`{3,}|~{3,})")
# "1. ", "2) ", "1.2 ", "IV. ": not a bare number ("2024 in review").
_NUMBERING = re.compile(r"^(?:\d+(?:\.\d+)*[.)]|\d+(?:\.\d+)+|[IVXLC]+\.)\s+")
_EMPHASIS = re.compile(r"[*_`]+")


@dataclass(frozen=True)
class Section:
    index: int
    heading: str
    # Everything from the heading line to the next heading at this level.
    text: str


@dataclass(frozen=True)
class ReportOutline:
    title: str | None
    preamble: str
    sections: list[Section] = field(default_factory=list)


def heading_label(raw: str) -> str:
    """The heading as the contents list shows it: no emphasis, no numbering."""
    text = _EMPHASIS.sub("", raw).strip()
    stripped = _NUMBERING.sub("", text).strip()
    return stripped or text


def _headings(lines: list[str]) -> list[tuple[int, int, str]]:
    """(line number, level, text) of every heading outside fenced code."""
    found: list[tuple[int, int, str]] = []
    fence: str | None = None
    for number, line in enumerate(lines):
        marker = _FENCE.match(line)
        if marker:
            token = marker.group(1)
            if fence is None:
                fence = token
            elif token[0] == fence[0] and len(token) >= len(fence):
                fence = None
            continue
        if fence is not None:
            continue
        match = _HEADING.match(line)
        if match:
            found.append((number, len(match.group(1)), match.group(2).strip()))
    return found


def split_report(markdown: str) -> ReportOutline:
    lines = (markdown or "").replace("\r\n", "\n").split("\n")
    headings = _headings(lines)
    title: str | None = None
    title_line: int | None = None
    if headings and headings[0][1] == 1:
        before = "\n".join(lines[: headings[0][0]]).strip()
        if not before:
            title_line, _, raw = headings[0]
            title = heading_label(raw)
            headings = headings[1:]
    if not headings:
        start = 0 if title_line is None else title_line + 1
        return ReportOutline(title, "\n".join(lines[start:]).strip(), [])

    counts: dict[int, int] = {}
    for _, level, _ in headings:
        counts[level] = counts.get(level, 0) + 1
    repeated = [level for level, count in counts.items() if count >= 2]
    toc_level = min(repeated) if repeated else min(counts)
    starts = [(n, text) for n, level, text in headings if level == toc_level]

    first = starts[0][0]
    start = 0 if title_line is None else title_line + 1
    preamble = "\n".join(lines[start:first]).strip()
    sections: list[Section] = []
    for index, (line_number, raw) in enumerate(starts):
        # Up to the next listed heading, so nothing between two is dropped.
        end = starts[index + 1][0] if index + 1 < len(starts) else len(lines)
        sections.append(
            Section(
                index=index,
                heading=heading_label(raw),
                text="\n".join(lines[line_number:end]).strip(),
            )
        )
    return ReportOutline(title, preamble, sections)
