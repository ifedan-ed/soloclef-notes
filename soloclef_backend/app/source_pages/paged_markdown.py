"""Parser output that remembers where its pages were."""

from __future__ import annotations


class PagedMarkdown(str):
    """Markdown text plus the markdown of each source page, in page order.

    A ``str`` so every caller that treats parser output as text keeps working;
    only the page stamper reads ``page_texts``. ``page_texts[i]`` is page
    ``i + 1``; a page the parser returned nothing for is an empty string.
    """

    page_texts: tuple[str, ...]

    def __new__(cls, markdown: str, page_texts: list[str] | tuple[str, ...]):
        instance = super().__new__(cls, markdown)
        instance.page_texts = tuple(page_texts)
        return instance


def page_texts_of(value: object) -> list[str] | None:
    """The page texts a parser attached to its output, or None if it had none."""
    pages = getattr(value, "page_texts", None)
    if not pages:
        return None
    return list(pages)


def pages_by_number(numbered: list[tuple[int, str]]) -> list[str]:
    """Order ``(page_number, text)`` pairs into a list indexed by page - 1.

    Parsers report 1-based page numbers and may skip a page; a skipped page
    stays an empty string so later pages keep their numbers.
    """
    if not numbered:
        return []
    last = max(number for number, _ in numbered)
    pages = [""] * last
    for number, text in numbered:
        if number >= 1:
            pages[number - 1] = (
                f"{pages[number - 1]}\n{text}" if pages[number - 1] else text
            )
    return pages
