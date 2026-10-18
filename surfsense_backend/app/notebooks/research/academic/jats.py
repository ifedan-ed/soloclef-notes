"""JATS article XML (PubMed Central, medRxiv/bioRxiv) as markdown.

Keeps what a reader needs to cite from: title, abstract, and the body's
sections, paragraphs, lists and figure/table captions. References, tables'
cells and back matter are left out; they bloat the source without helping it
answer questions.
"""

from __future__ import annotations

from xml.etree.ElementTree import Element

from .xml_text import flat_text, local_name

_SKIP = frozenset({"ref-list", "table", "xref-group", "fn-group", "ack", "app-group"})


def article_element(root: Element) -> Element | None:
    """The ``<article>`` itself, whether ``root`` is it or an article set around it."""
    if local_name(root.tag) == "article":
        return root
    return next((el for el in root.iter() if local_name(el.tag) == "article"), None)


def _child(element: Element, name: str) -> Element | None:
    return next((c for c in element if local_name(c.tag) == name), None)


def _find(element: Element, path: list[str]) -> Element | None:
    node: Element | None = element
    for name in path:
        node = _child(node, name) if node is not None else None
    return node


def has_body_text(article: Element) -> bool:
    """True when the full text is present (PMC withholds it outside the open-access subset)."""
    body = _child(article, "body")
    return body is not None and any(
        flat_text(p) for p in body.iter() if local_name(p.tag) == "p"
    )


def title_of(article: Element) -> str:
    return flat_text(
        _find(article, ["front", "article-meta", "title-group", "article-title"])
    )


def _blocks(element: Element, depth: int) -> list[str]:
    out: list[str] = []
    for child in element:
        name = local_name(child.tag)
        if name in _SKIP:
            continue
        if name == "title":
            continue
        if name == "sec":
            heading = flat_text(_child(child, "title"))
            if heading:
                out.append(f"{'#' * min(depth, 6)} {heading}")
            out.extend(_blocks(child, depth + 1))
        elif name == "p":
            text = flat_text(child)
            if text:
                out.append(text)
        elif name == "list":
            items = [
                flat_text(item) for item in child if local_name(item.tag) == "list-item"
            ]
            out.extend(f"- {item}" for item in items if item)
        elif name in ("fig", "table-wrap"):
            label = flat_text(_child(child, "label"))
            caption = flat_text(_child(child, "caption"))
            if caption:
                out.append(f"*{label + ': ' if label else ''}{caption}*")
        elif name in ("disp-quote", "boxed-text"):
            out.extend(_blocks(child, depth))
    return out


def abstract_text(article: Element) -> str:
    abstract = _find(article, ["front", "article-meta", "abstract"])
    if abstract is None:
        return ""
    blocks = _blocks(abstract, 3)
    return "\n\n".join(blocks) if blocks else flat_text(abstract)


def body_markdown(article: Element) -> str:
    body = _child(article, "body")
    return "\n\n".join(_blocks(body, 2)) if body is not None else ""
