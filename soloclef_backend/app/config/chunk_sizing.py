"""What counts a chunk's size, and how many of those units a chunk holds.

Chonkie's chunkers size a chunk with whatever tokenizer they are handed, and
``RecursiveChunker``'s default counts characters -- so a chunk size means
characters until a tokenizer is named. The embedding client cannot supply
one: chonkie's ``LiteLLMEmbeddings`` guesses a tokenizer from the model name
and reaches for ``cl100k_base`` when the guess fails, which is the hidden
fallback this project's rules forbid, needs a network fetch of its own at
every cold start, and on this server's own documents counts 4-33% fewer
tokens than BGE-M3 does.

So both halves are named outright: ``EMBEDDING_TOKENIZER`` counts, and
``EMBEDDING_CHUNK_SIZE`` says how many of its units fit in a chunk. A named
tokenizer that cannot be loaded stops the server instead of sizing chunks by
some other vocabulary.

Unset, chunks keep being sized in characters, as they have been since
upstream moved from ``LateChunker`` (which did use the embedding model's
tokenizer) to ``RecursiveChunker``. Refusing to start would break every
existing deployment on upgrade and force an immediate reindex, and a
character count is not the kind of fallback the rule is aimed at: it is
chonkie's own explicit, deterministic, offline counter, not a guess at a
model's vocabulary. The startup warning names the setting.

A tokenizer named here is loaded for the chunkers alone and is never the
instance the embedding client holds, so the fast-tokenizer hazard
``app/utils/document_converters.py`` records -- and the ``TOKENIZER_LOCK``
guarding it -- stays confined to that client's own counting.
"""

from __future__ import annotations

import logging
import os
from collections.abc import Callable, Mapping
from dataclasses import dataclass
from pathlib import Path
from typing import Any

TOKENIZER_ENV = "EMBEDDING_TOKENIZER"
CHUNK_SIZE_ENV = "EMBEDDING_CHUNK_SIZE"

# Chonkie's name for its character counter: the size unit when no tokenizer
# is named.
CHARACTER_TOKENIZER = "character"
DEFAULT_CHUNK_SIZE = 512

logger = logging.getLogger(__name__)


@dataclass(frozen=True, slots=True)
class ChunkSizing:
    """A chunker's two size arguments, and what its numbers mean."""

    tokenizer: Any
    chunk_size: int
    unit: str

    @property
    def description(self) -> str:
        """The startup line: the ceiling and the vocabulary it is counted in."""
        return f"chunks of at most {self.chunk_size} {self.unit}"


def load_chunk_sizing(
    environ: Mapping[str, str] | None = None,
    *,
    load: Callable[[Any], Any] | None = None,
) -> ChunkSizing:
    environ = os.environ if environ is None else environ
    if load is None:
        from chonkie.tokenizer import AutoTokenizer

        load = AutoTokenizer
    chunk_size = _chunk_size(environ)
    name = (environ.get(TOKENIZER_ENV) or "").strip()
    if not name:
        logger.warning(
            "%s is not set, so chunk sizes are counted in characters: %s. "
            "Set it to the embedding model's tokenizer (its HuggingFace name, "
            "or a directory holding tokenizer.json) to size chunks in that "
            "model's tokens, and set %s to a ceiling in those tokens.",
            TOKENIZER_ENV,
            ChunkSizing(
                tokenizer=None, chunk_size=chunk_size, unit="characters"
            ).description,
            CHUNK_SIZE_ENV,
        )
        return ChunkSizing(
            tokenizer=load(CHARACTER_TOKENIZER),
            chunk_size=chunk_size,
            unit="characters",
        )
    try:
        tokenizer = load(_baked_tokenizer(name) or name)
    except Exception as exc:
        raise ValueError(
            f"{TOKENIZER_ENV}={name!r} could not be loaded ({exc}). It names the "
            "tokenizer that sizes every chunk, so this server will not size "
            f"chunks by another vocabulary instead; unset, {TOKENIZER_ENV} "
            "means characters."
        ) from exc
    return ChunkSizing(
        tokenizer=tokenizer, chunk_size=chunk_size, unit=f"{name} tokens"
    )


def _baked_tokenizer(name: str) -> Any | None:
    """The tokenizer at ``name`` when that is a path, not a hub repo id.

    A deployment bakes ``tokenizer.json`` into its image and names the
    directory holding it, so no cold start fetches a vocabulary. Chonkie's
    ``AutoTokenizer`` reads a name rather than a path -- a path resolved only
    through ``transformers``, which arrived with the on-box TTS stack and is
    gone -- so the file is opened here with ``tokenizers`` itself and the
    instance handed on. A path that holds no tokenizer raises, and the caller
    turns that into the refusal naming the setting.
    """
    path = Path(name)
    if not path.exists():
        return None

    from tokenizers import Tokenizer

    return Tokenizer.from_file(str(path / "tokenizer.json" if path.is_dir() else path))


def _chunk_size(environ: Mapping[str, str]) -> int:
    raw = (environ.get(CHUNK_SIZE_ENV) or "").strip()
    if not raw:
        return DEFAULT_CHUNK_SIZE
    try:
        size = int(raw)
    except ValueError:
        size = 0
    if size < 1:
        raise ValueError(
            f"{CHUNK_SIZE_ENV}={raw!r} is not a whole number of at least 1; "
            f"unset, it is {DEFAULT_CHUNK_SIZE}"
        )
    return size
