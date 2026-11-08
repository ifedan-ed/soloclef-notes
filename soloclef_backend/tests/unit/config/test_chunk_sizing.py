"""What sizes a chunk: EMBEDDING_TOKENIZER and EMBEDDING_CHUNK_SIZE."""

from __future__ import annotations

import logging

import pytest

from app.config.chunk_sizing import (
    CHARACTER_TOKENIZER,
    CHUNK_SIZE_ENV,
    DEFAULT_CHUNK_SIZE,
    TOKENIZER_ENV,
    load_chunk_sizing,
)

pytestmark = pytest.mark.unit


class _Loaded:
    """Stands in for a chonkie tokenizer so no test reaches a model hub."""

    def __init__(self, name: str) -> None:
        self.name = name


def _loader(*, fails: set[str] = frozenset()):
    def load(name: str):
        if name in fails:
            raise ValueError(f"no such tokenizer {name!r}")
        return _Loaded(name)

    return load


def test_unset_sizes_chunks_in_characters_and_names_the_setting(caplog):
    with caplog.at_level(logging.WARNING):
        sizing = load_chunk_sizing({}, load=_loader())

    assert sizing.tokenizer.name == CHARACTER_TOKENIZER
    assert sizing.chunk_size == DEFAULT_CHUNK_SIZE == 512
    assert sizing.unit == "characters"
    assert TOKENIZER_ENV in caplog.text


def test_a_named_tokenizer_is_loaded_and_counts_the_chunk_size(caplog):
    with caplog.at_level(logging.WARNING):
        sizing = load_chunk_sizing(
            {TOKENIZER_ENV: " BAAI/bge-m3 ", CHUNK_SIZE_ENV: "1024"},
            load=_loader(),
        )

    assert sizing.tokenizer.name == "BAAI/bge-m3"
    assert sizing.chunk_size == 1024
    assert sizing.unit == "BAAI/bge-m3 tokens"
    assert caplog.text == ""


def test_a_tokenizer_that_cannot_be_loaded_stops_the_server():
    with pytest.raises(ValueError) as raised:
        load_chunk_sizing(
            {TOKENIZER_ENV: "BAAI/bge-m3"}, load=_loader(fails={"BAAI/bge-m3"})
        )

    message = str(raised.value)
    assert TOKENIZER_ENV in message
    assert "BAAI/bge-m3" in message
    # The point of the setting: no other vocabulary is quietly used instead.
    assert "cl100k" not in message.lower()


def test_an_unreadable_chunk_size_stops_the_server():
    with pytest.raises(ValueError) as raised:
        load_chunk_sizing({CHUNK_SIZE_ENV: "many"}, load=_loader())

    assert CHUNK_SIZE_ENV in str(raised.value)
    assert str(DEFAULT_CHUNK_SIZE) in str(raised.value)


def test_a_chunk_size_below_one_stops_the_server():
    with pytest.raises(ValueError):
        load_chunk_sizing({CHUNK_SIZE_ENV: "0"}, load=_loader())


def test_the_startup_line_says_what_counts_and_how_much():
    assert (
        load_chunk_sizing({}, load=_loader()).description
        == "chunks of at most 512 characters"
    )
    assert (
        load_chunk_sizing(
            {TOKENIZER_ENV: "BAAI/bge-m3", CHUNK_SIZE_ENV: "1024"}, load=_loader()
        ).description
        == "chunks of at most 1024 BAAI/bge-m3 tokens"
    )


def test_the_chunker_built_from_it_counts_in_the_named_unit():
    """The seam that matters: chunk_size is in EMBEDDING_TOKENIZER's units.

    ``word`` is one of chonkie's own counters, so this runs offline; a model's
    tokenizer reaches the same chunker the same way.
    """
    from chonkie import RecursiveChunker

    text = " ".join(f"word{n}" for n in range(60))

    sizing = load_chunk_sizing({TOKENIZER_ENV: "word", CHUNK_SIZE_ENV: "10"})
    chunker = RecursiveChunker(tokenizer=sizing.tokenizer, chunk_size=sizing.chunk_size)

    assert sizing.unit == "word tokens"
    assert all(len(chunk.text.split()) <= 10 for chunk in chunker.chunk(text))
    assert len(chunker.chunk(text)) >= 6


def _bake_a_tokenizer(directory):
    """A real tokenizer.json on disk, as the deployment's image bakes one."""
    from tokenizers import Tokenizer
    from tokenizers.models import WordLevel
    from tokenizers.pre_tokenizers import Whitespace

    vocab = {f"word{n}": n for n in range(60)}
    vocab["[UNK]"] = len(vocab)
    tokenizer = Tokenizer(WordLevel(vocab, unk_token="[UNK]"))
    tokenizer.pre_tokenizer = Whitespace()
    path = directory / "tokenizer.json"
    tokenizer.save(str(path))
    return path


def test_a_baked_tokenizer_directory_sizes_chunks_in_its_tokens(tmp_path):
    """The offline case: a directory of weights, no hub fetch, no transformers.

    A path was only ever loadable through transformers, which arrived with the
    on-box TTS stack. It is loaded here with ``tokenizers`` itself, so the
    image that bakes a tokenizer keeps starting.
    """
    from chonkie import RecursiveChunker

    _bake_a_tokenizer(tmp_path)
    text = " ".join(f"word{n}" for n in range(60))

    sizing = load_chunk_sizing({TOKENIZER_ENV: f" {tmp_path} ", CHUNK_SIZE_ENV: "10"})
    chunker = RecursiveChunker(tokenizer=sizing.tokenizer, chunk_size=sizing.chunk_size)

    assert sizing.unit == f"{tmp_path} tokens"
    assert all(chunk.token_count <= 10 for chunk in chunker.chunk(text))
    assert len(chunker.chunk(text)) >= 6


def test_the_tokenizer_json_itself_can_be_named(tmp_path):
    path = _bake_a_tokenizer(tmp_path)

    sizing = load_chunk_sizing({TOKENIZER_ENV: str(path)})

    assert sizing.tokenizer.encode("word1 word2") == [1, 2]


def test_a_directory_holding_no_tokenizer_stops_the_server(tmp_path):
    with pytest.raises(ValueError) as raised:
        load_chunk_sizing({TOKENIZER_ENV: str(tmp_path)})

    assert TOKENIZER_ENV in str(raised.value)
    assert str(tmp_path) in str(raised.value)
