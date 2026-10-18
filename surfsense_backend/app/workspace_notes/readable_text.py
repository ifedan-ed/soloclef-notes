# ruff: noqa: RUF001, RUF002, RUF003  (Greek letters and maths signs are the point)
"""Maths and Markdown as the plain words they show: a note's title and preview.

A chat answer writes its maths in TeX between ``\\( \\)`` or ``\\[ \\]``
(sometimes ``$$ $$`` or single dollars), and a title cut from one kept it raw:
"Pearson's \\(r = 0.463\\)". Here each formula becomes plain text: common TeX
as Unicode (``r^2`` → r², ``\\alpha`` → α, ``\\le`` → ≤, ``\\frac{a}{b}`` →
a/b, ``\\text{kg}`` → kg), anything else as its letters with the commands and
braces taken out. A single dollar is maths only as pandoc reads it (no space
after the opening one, none before the closing one, no digit after it), so
"$3 and $5" stays money. Delimiters left without their pair (a title cut in
the middle of a formula) are dropped.

The web has the same rules (``surfsense_web/lib/markdown/math-as-text.ts``
and ``plain-text.ts``) and so does the phone app; keep them in step.
"""

from __future__ import annotations

import re
from collections.abc import Callable

_GREEK = {
    "alpha": "α",
    "beta": "β",
    "gamma": "γ",
    "delta": "δ",
    "epsilon": "ε",
    "varepsilon": "ε",
    "zeta": "ζ",
    "eta": "η",
    "theta": "θ",
    "vartheta": "ϑ",
    "iota": "ι",
    "kappa": "κ",
    "lambda": "λ",
    "mu": "μ",
    "nu": "ν",
    "xi": "ξ",
    "pi": "π",
    "rho": "ρ",
    "sigma": "σ",
    "tau": "τ",
    "upsilon": "υ",
    "phi": "φ",
    "varphi": "φ",
    "chi": "χ",
    "psi": "ψ",
    "omega": "ω",
    "Gamma": "Γ",
    "Delta": "Δ",
    "Theta": "Θ",
    "Lambda": "Λ",
    "Xi": "Ξ",
    "Pi": "Π",
    "Sigma": "Σ",
    "Upsilon": "Υ",
    "Phi": "Φ",
    "Psi": "Ψ",
    "Omega": "Ω",
}

_SYMBOLS = {
    "times": "×",
    "cdot": "·",
    "div": "÷",
    "pm": "±",
    "mp": "∓",
    "le": "≤",
    "leq": "≤",
    "ge": "≥",
    "geq": "≥",
    "lt": "<",
    "gt": ">",
    "neq": "≠",
    "ne": "≠",
    "approx": "≈",
    "sim": "∼",
    "simeq": "≃",
    "equiv": "≡",
    "propto": "∝",
    "ll": "≪",
    "gg": "≫",
    "infty": "∞",
    "to": "→",
    "rightarrow": "→",
    "leftarrow": "←",
    "leftrightarrow": "↔",
    "Rightarrow": "⇒",
    "implies": "⇒",
    "Leftrightarrow": "⇔",
    "iff": "⇔",
    "uparrow": "↑",
    "downarrow": "↓",
    "degree": "°",
    "circ": "°",
    "partial": "∂",
    "nabla": "∇",
    "sum": "∑",
    "int": "∫",
    "prod": "∏",
    "in": "∈",
    "notin": "∉",
    "cup": "∪",
    "cap": "∩",
    "subset": "⊂",
    "forall": "∀",
    "exists": "∃",
    "neg": "¬",
    "land": "∧",
    "lor": "∨",
    "mid": "|",
    "vert": "|",
    "lvert": "|",
    "rvert": "|",
    "langle": "⟨",
    "rangle": "⟩",
    "ldots": "…",
    "cdots": "⋯",
    "dots": "…",
    "prime": "′",
    "quad": " ",
    "qquad": " ",
    "log": "log",
    "ln": "ln",
    "exp": "exp",
    "sin": "sin",
    "cos": "cos",
    "tan": "tan",
    "min": "min",
    "max": "max",
    "lim": "lim",
}

_SUPERSCRIPT = dict(
    zip(
        "0123456789+-−=()nixyT°",
        "⁰¹²³⁴⁵⁶⁷⁸⁹⁺⁻⁻⁼⁽⁾ⁿⁱˣʸᵀ°",
        strict=True,
    )
)
_SUBSCRIPT = dict(
    zip(
        "0123456789+-−=()aeoxiruvhklmnpstj",
        "₀₁₂₃₄₅₆₇₈₉₊₋₋₌₍₎ₐₑₒₓᵢᵣᵤᵥₕₖₗₘₙₚₛₜⱼ",
        strict=True,
    )
)

# A mark over one letter: \bar{x} → x̄.
_ACCENTS = {
    "bar": "\u0304",
    "overline": "\u0304",
    "hat": "\u0302",
    "widehat": "\u0302",
    "tilde": "\u0303",
    "widetilde": "\u0303",
    "vec": "\u20d7",
    "dot": "\u0307",
    "ddot": "\u0308",
}

# Commands whose argument is words, kept as they are.
_TEXT = {
    "text",
    "textrm",
    "textit",
    "textbf",
    "mathrm",
    "mathit",
    "mathbf",
    "mathsf",
    "operatorname",
    "mbox",
}
_FRACTIONS = {"frac", "dfrac", "tfrac"}
_SPACES = {"\\", ",", ";", ":", " "}
_COMMAND = re.compile(r"\\([A-Za-z]+|[\s\S]?)")
_LETTERS = re.compile(r"[A-Za-z]+")
_MORE_THAN_ONE_TERM = re.compile(r"[\s+\-−×·=<>/]")
_WHITESPACE = re.compile(r"\s+")


def _command_at(src: str, i: int) -> str:
    match = _COMMAND.match(src, i)
    return match.group(0) if match else "\\"


def _argument(src: str, i: int) -> tuple[str, int]:
    """The argument at ``i`` (a {group}, a \\command or one character) and the index after it."""
    while i < len(src) and src[i] == " ":
        i += 1
    if i < len(src) and src[i] == "{":
        depth = 0
        for j in range(i, len(src)):
            if src[j] == "{":
                depth += 1
            elif src[j] == "}":
                depth -= 1
                if depth == 0:
                    return src[i + 1 : j], j + 1
        return src[i + 1 :], len(src)
    if i < len(src) and src[i] == "\\":
        command = _command_at(src, i)
        return command, i + len(command)
    return (src[i] if i < len(src) else ""), i + 1


def _term(text: str) -> str:
    """A fraction's part, bracketed when it is more than one term: (a+b)/2."""
    return f"({text})" if _MORE_THAN_ONE_TERM.search(text) else text


def _scripted(text: str, table: dict[str, str], mark: str) -> str:
    if text and all(ch in table for ch in text):
        return "".join(table[ch] for ch in text)
    return f"^({text})" if mark == "^" and len(text) > 1 else f"{mark}{text}"


def _words(src: str) -> str:
    out: list[str] = []
    i = 0
    while i < len(src):
        ch = src[i]
        if ch == "\\":
            command = _command_at(src, i)
            name = command[1:]
            i += len(command)
            if name in _FRACTIONS:
                a, i = _argument(src, i)
                b, i = _argument(src, i)
                out.append(f"{_term(_words(a))}/{_term(_words(b))}")
            elif name == "sqrt":
                root = "√"
                if i < len(src) and src[i] == "[":
                    close = src.find("]", i)
                    degree = "" if close < 0 else src[i + 1 : close].strip()
                    root = {"3": "∛", "4": "∜"}.get(degree, "√")
                    i = i if close < 0 else close + 1
                x, i = _argument(src, i)
                inner = _words(x)
                out.append(f"{root}({inner})" if len(inner) > 1 else f"{root}{inner}")
            elif name in _TEXT:
                x, i = _argument(src, i)
                out.append(_words(x))
            elif name in _ACCENTS:
                x, i = _argument(src, i)
                inner = _words(x)
                out.append(inner + _ACCENTS[name] if len(inner) == 1 else inner)
            elif name in ("begin", "end"):
                i = _argument(src, i)[1]
            elif name in ("left", "right"):
                # The bracket after it stays; \left. is no bracket.
                if i < len(src) and src[i] == ".":
                    i += 1
            elif name in _GREEK:
                out.append(_GREEK[name])
            elif name in _SYMBOLS:
                out.append(_SYMBOLS[name])
            elif name in _SPACES:
                out.append(" ")
            elif not _LETTERS.fullmatch(name) and name != "!":
                # An escaped character (\%, \$, \{, \_) is itself.
                out.append(name)
            # Any other command is dropped; what it applies to stays.
        elif ch in ("^", "_"):
            x, i = _argument(src, i + 1)
            table = _SUPERSCRIPT if ch == "^" else _SUBSCRIPT
            out.append(_scripted(_words(x), table, ch))
        elif ch in ("{", "}"):
            i += 1
        elif ch in ("~", "&"):
            out.append(" ")
            i += 1
        else:
            out.append(ch)
            i += 1
    return "".join(out)


def tex_as_text(tex: str) -> str:
    """One formula's TeX as plain text. Never fails: what it can't turn into a symbol stays as letters."""
    return _WHITESPACE.sub(" ", _words(tex)).strip()


# The forms chat answers write maths in. Groups: 1 $$…$$, 2 \[…\], 3 \(…\),
# 4–5 \begin{equation|displaymath|math}…\end{…}, 6 a single-dollar formula.
_MATH = re.compile(
    r"\$\$([\s\S]+?)\$\$"
    r"|\\\[([\s\S]+?)\\\]"
    r"|\\\(([\s\S]+?)\\\)"
    r"|\\begin\{(equation|displaymath|math)\}([\s\S]*?)\\end\{\4\}"
    r"|(?<![\\$])\$(?![\s$])((?:[^$\n\\]|\\.)+?)(?<!\s)\$(?![\d$])"
)
# A delimiter whose pair is not there (a cut title): dropped.
_STRAY = re.compile(r"\\[()\[\]]|\$\$|\\(?:begin|end)\{(?:equation|displaymath|math)\}")
# Where a formula stood while ``outside`` runs: private-use characters, which
# no Markdown pattern matches.
_HELD = re.compile("\ue000([0-9]+)\ue001")
_PRIVATE = re.compile("[\ue000\ue001]")

#: The most of a text ``math_as_text`` reads: 2,000 characters; the rest is
#: cut. ``_MATH`` scans from every opening delimiter to the end of the text
#: when no closing one follows, so its time grows with the square of the
#: length: a 20,000-character note of bare ``\[`` took a second, a megabyte
#: would hold the worker for hours, and a note's title is cut from it on
#: every save. Every caller shows only the opening words (a title of at most
#: 100 characters, a 240-character preview, a title), so 2,000 is plenty and
#: keeps the worst case near 10 ms. A formula cut in two loses its unpaired
#: delimiter, as in a cut title. custom/README.md, "Security fixes of
#: 2026-10-11".
MATH_TEXT_MAX_CHARS = 2_000


def math_as_text(text: str, outside: Callable[[str], str] | None = None) -> str:
    """The first ``MATH_TEXT_MAX_CHARS`` of ``text``, each formula as plain text (``tex_as_text``).

    ``outside`` (a caller's Markdown clean-up) runs on the text with the
    formulas held out of its reach, since ``_`` and ``*`` mean something else
    in maths; it still sees where each line starts.
    """
    text = (text or "")[:MATH_TEXT_MAX_CHARS]
    formulas: list[str] = []

    def hold(match: re.Match[str]) -> str:
        tex = next((g for g in match.group(1, 2, 3, 5, 6) if g is not None), "")
        formulas.append(tex_as_text(tex))
        return f"\ue000{len(formulas) - 1}\ue001"

    held = _STRAY.sub("", _MATH.sub(hold, _PRIVATE.sub("", text)))
    if outside is not None:
        held = outside(held)
    return _HELD.sub(lambda m: formulas[int(m.group(1))], held)


_QUOTE = re.compile(r"^[ \t]*(?:>[ \t]?)+", re.M)
_HEADING = re.compile(r"^[ \t]*#{1,6}[ \t]+", re.M)
_LIST_MARKER = re.compile(r"^[ \t]*(?:[-+*]|\d{1,9}[.)])[ \t]+", re.M)
_LINK = re.compile(r"!?\[([^\]\n]*)\]\([^)\n]*\)")
_ESCAPE_OR_MARK = re.compile(r"\\([!-/:-@\[-`{-~])|[*_`]")


def _without_marks(text: str) -> str:
    text = _LIST_MARKER.sub("", _HEADING.sub("", _QUOTE.sub("", text)))
    text = _LINK.sub(r"\1", text)
    return _ESCAPE_OR_MARK.sub(lambda m: m.group(1) or "", text)


#: A link or image anywhere in a text: ``[words](where)`` or ``![words](where)``.
#: A label stops at the next "[" and a destination at the next "]", so its
#: time grows only with the length (``_LINK``'s pattern, run over a whole
#: note, would grow with its square); not after a backslash, so ``\[x\](1)``
#: stays maths.
_LINK_ANYWHERE = re.compile(r"(?<!\\)!?\[([^\[\]\n]*)\]\([^)\]\n]*\)")


def links_as_words(markdown: str) -> str:
    """Every link and image of a whole text as its words, before any cut.

    ``math_as_text`` reads only the first ``MATH_TEXT_MAX_CHARS``: a note
    opening with an image whose address is longer than that (a pasted
    ``data:`` image) or with a long link was cut inside it, the pattern no
    longer matched, and its title and preview were the raw markup
    (``![](data:image/png;base64,iVBOR…``). Read first, the image is its
    alt text (often none, so the title comes from the next line) and the
    link its words. custom/README.md, "Security fixes of 2026-10-11",
    follow-up.
    """
    return _LINK_ANYWHERE.sub(r"\1", markdown or "")


def plain_text(markdown: str) -> str:
    """The opening of a Markdown text as the words it shows (at most
    ``MATH_TEXT_MAX_CHARS`` of it after its links and images became their
    words): maths as text; heading, list, quote, emphasis and code marks
    dropped; line breaks kept."""
    return math_as_text(links_as_words(markdown), _without_marks)


def first_line_of_words(markdown: str) -> str:
    """The first line of ``plain_text(markdown)`` with any words, spaces folded.

    Only the opening ``MATH_TEXT_MAX_CHARS`` are read, whatever the note's
    length: a note whose first words come later is "Untitled".
    """
    for line in plain_text(markdown).split("\n"):
        words = _WHITESPACE.sub(" ", line).strip()
        if words:
            return words
    return ""


def readable_title(title: str) -> str:
    """A title with its maths as text and its spaces folded; nothing else changed."""
    return _WHITESPACE.sub(" ", math_as_text(title)).strip()
