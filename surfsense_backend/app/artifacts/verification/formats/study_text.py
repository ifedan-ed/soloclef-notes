"""Shared plain-text and bounded LaTeX contract for study artifacts."""

from __future__ import annotations

from typing import Literal

# Plain text's characters Markdown would read as syntax, backslash-escaped.
# Brackets and parentheses are not backslash-escaped: the website and the app
# read "\(" "\)" "\[" "\]" as maths (the very form
# ``study_text_to_markdown`` writes its maths in), so "(0.39 AU)" escaped that
# way showed as an equation. "[" alone opens a link, an image, a footnote or a
# task box, so it is written as the character reference "&#91;" (shown as "[",
# never read as syntax); with no "[" to pair with, "]" and "(" ")" stay as
# they are, except a ")" after a line's leading digits, which would start a
# numbered list ("1)"), written "&#41;".
MARKDOWN_SPECIAL_CHARS = frozenset("\\`*_{}<>#+-.!|>")
_CHARACTER_REFERENCES = {"[": "&#91;"}


def has_control_character(value: str) -> bool:
    return (
        any(ord(character) < 32 and character not in "\t\n\r" for character in value)
        or "\x7f" in value
    )


def _is_escaped(value: str, index: int) -> bool:
    preceding_backslashes = 0
    index -= 1
    while index >= 0 and value[index] == "\\":
        preceding_backslashes += 1
        index -= 1
    return preceding_backslashes % 2 == 1


def _validate_latex(latex: str, *, field: str) -> None:
    if not latex.strip():
        raise ValueError(f"{field} contains an empty LaTeX expression")

    brace_depth = 0
    for index, character in enumerate(latex):
        if _is_escaped(latex, index):
            continue
        if character == "{":
            brace_depth += 1
        elif character == "}":
            brace_depth -= 1
            if brace_depth < 0:
                break
    if brace_depth != 0:
        raise ValueError(f"{field} contains unbalanced LaTeX braces")


def split_text_and_latex(
    value: str, *, field: str, maximum: int
) -> list[tuple[Literal["text", "inline", "display"], str]]:
    if not value.strip():
        raise ValueError(f"{field} must not be empty")
    if len(value) > maximum:
        raise ValueError(f"{field} exceeds {maximum} characters")
    if has_control_character(value):
        raise ValueError(f"{field} contains unsupported control characters")

    segments: list[tuple[Literal["text", "inline", "display"], str]] = []
    text_start = 0
    index = 0
    while index < len(value):
        delimiter = value[index : index + 2]
        if delimiter in {r"\)", r"\]"} and not _is_escaped(value, index):
            raise ValueError(f"{field} contains an unmatched LaTeX delimiter")
        if delimiter not in {r"\(", r"\["} or _is_escaped(value, index):
            index += 1
            continue

        if index > text_start:
            segments.append(("text", value[text_start:index]))
        kind: Literal["inline", "display"] = (
            "inline" if delimiter == r"\(" else "display"
        )
        closing = r"\)" if kind == "inline" else r"\]"
        latex_start = index + 2
        index = latex_start
        while index < len(value):
            candidate = value[index : index + 2]
            if candidate in {r"\(", r"\["} and not _is_escaped(value, index):
                raise ValueError(f"{field} contains nested LaTeX delimiters")
            if candidate in {r"\)", r"\]"} and not _is_escaped(value, index):
                if candidate != closing:
                    raise ValueError(f"{field} contains mismatched LaTeX delimiters")
                latex = value[latex_start:index]
                _validate_latex(latex, field=field)
                segments.append((kind, latex))
                index += 2
                text_start = index
                break
            index += 1
        else:
            raise ValueError(f"{field} contains an unclosed LaTeX delimiter")

    if text_start < len(value):
        segments.append(("text", value[text_start:]))
    return segments


def validate_study_text(value: str, *, field: str, maximum: int) -> str:
    split_text_and_latex(value, field=field, maximum=maximum)
    return value


def _starts_numbered_list(value: str, index: int) -> bool:
    """Whether the ")" at ``index`` follows only digits since the line began."""
    start = value.rfind("\n", 0, index) + 1
    digits = value[start:index]
    return 0 < len(digits) <= 9 and digits.isdigit()


def escape_markdown(value: str) -> str:
    escaped: list[str] = []
    for index, character in enumerate(value):
        if character in _CHARACTER_REFERENCES:
            escaped.append(_CHARACTER_REFERENCES[character])
        elif character == ")" and _starts_numbered_list(value, index):
            escaped.append("&#41;")
        elif character in MARKDOWN_SPECIAL_CHARS:
            escaped.append(f"\\{character}")
        else:
            escaped.append(character)
    return "".join(escaped)


def study_text_to_markdown(value: str, *, field: str, maximum: int) -> str:
    return "".join(
        escape_markdown(content)
        if kind == "text"
        else f"\\({content}\\)"
        if kind == "inline"
        else f"\\[\n{content}\n\\]"
        for kind, content in split_text_and_latex(value, field=field, maximum=maximum)
    )
