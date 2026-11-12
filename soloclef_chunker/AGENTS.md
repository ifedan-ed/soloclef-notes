# soloclef_chunker

The chunker that decides where a stored chunk begins and ends, in Go. Nearest
`AGENTS.md` wins; the root one owns organisation and TDD.

## The constraint

A stored chunk's boundaries are in the database, against the vectors they
produced, so a cut that moves by one byte is a corpus that no longer matches
its own index. This module is therefore not an implementation of recursive
chunking on judgement: it is a reproduction of chonkie's, as the Python app
configured it, proved byte for byte against it over the live corpus before the
Python app was removed. The fixture goldens in `testdata/documents` were
recorded from that chunker and are now the contract:

```bash
cargo build --release --manifest-path chonkie/rust/Cargo.toml   # needed first

# The fixture comparison and the real-vocabulary tests SKIP without this, and
# a skip reads as "ok". Always pass it. The file is the indexer image's own.
docker run --rm --entrypoint cat soloclefgo-indexer:local \
  /opt/tokenizers/bge-m3/tokenizer.json > /tmp/tokenizer.json
SOLOCLEF_TOKENIZER=/tmp/tokenizer.json go test ./... && go vet ./... && gofmt -l .
```

Agreement is demanded **byte for byte, chunk for chunk**, with no tolerance.
Every level slices the document rather than rebuilding it from token ids, so
nothing is left that could legitimately differ.

## How it reproduces chonkie: by calling it

The splitting and the merging are chonkie's own Rust core, pinned to the
version the installed `chonkie-core` wraps, and the token counting and
per-token offsets are HuggingFace's `tokenizers` reading the same
`tokenizer.json` the indexer image bakes (soloclef_worker/Dockerfile.indexer). Both are reached through `chonkie`,
a cgo binding over `chonkie/rust` (see `chonkie/rust/README.md`).

They were written out again in Go until 2026-11-11. That was correct as far as
it was tested and was a standing invitation to drift: a reimplementation
agrees until the day upstream changes a tie-break nobody thought to test.

What is still Go is the part that is chonkie's **Python**, not its core: the
level hierarchy, which of the core's two splitters each level goes to, the
recursion between levels, and chonkie's `character` tokenizer.

The cost is the module's old "no dependencies, pure Go" property, which was a
real one: a cgo build now needs a C toolchain and the static library built
beside it. That trade was Daniel's call on 2026-11-10.

## The one seam: what counts a chunk's size

`recursive.Sizer`. Nothing in this module decides what a chunk size of 512
means; the caller passes it, because that is what `EMBEDDING_TOKENIZER` does
on the Python side.

- `recursive.Characters` is chonkie's `character` tokenizer: 512 code points.
  It is what the Python side counts with when `EMBEDDING_TOKENIZER` is unset,
  and what the chunks stored before 2026-11-09 were cut to.
- `*chonkie.Tokenizer` is the baked vocabulary. Since 2026-11-09 the app runs
  `EMBEDDING_TOKENIZER=/app/tokenizer` (BAAI/bge-m3), so Python cuts at 512 of
  that model's tokens — roughly five times as much text per chunk.

A `Chunker` with no `Sizer` refuses to chunk. It does not pick one: the
vocabulary decides every boundary, so a guess is a corpus that silently does
not match its index. `cmd/chunkdump` takes it from `SOLOCLEF_TOKENIZER` and
refuses to run without it.

## Why it is a module and not a package

More than one Go tier chunks text (the indexer, and the API and assistant for
what a tool reads). A chunker in `soloclef_worker/internal` could not be used
by `soloclef_api` and the reverse is equally true, and a second copy is a second set of boundaries —
which is the one thing the byte comparison exists to prevent. `internal`
cannot cross a module, so this is a module, required with a relative
`replace`, exactly as `soloclef_celery/AGENTS.md` argues for the Celery
encoder.

## Layout

| Path | Role |
|------|------|
| `chonkie` | cgo over chonkie's Rust core and HuggingFace's `tokenizers`: token counts, per-token cut offsets, both of the core's splitters, and the core's merge. |
| `chonkie/rust` | The C ABI those calls land in. `target/` is gitignored build output. |
| `recursive` | chonkie's `RecursiveChunker`: the level hierarchy, which splitter each level uses, the recursion, and the last level's cut at token boundaries. |
| `tablesafe` | `chunk_text_hybrid`: Markdown tables recognised conservatively and packed row by row with their header repeated, and the prose around them chunked. |
| `cmd/chunkdump` | Chunks newline-delimited JSON documents, so a corpus can be chunked from the command line and compared with what is stored. |
| `testdata/documents` | Synthetic documents, the chunks the Python chunker cut from them, and `sizing.json` saying which vocabulary cut them. Synthetic on purpose — this repository has a public mirror. |

## What the Python side does that is easy to get wrong

Each of these moved a boundary during the port, and each has a test:

- `min_characters_per_chunk` (24) is named in characters and measured by
  chonkie-core **in bytes**, so a piece of six multi-byte characters can reach
  a minimum that a piece of twenty ASCII ones does not.
- A delimiter cut is only taken where the piece it closes *and* the next raw
  piece both reach that minimum, so a one-word line pulls its neighbours
  together instead of being emitted alone.
- Within a level the core matches leftmost-first, so a position where two
  delimiters match is cut by the one listed **first**, not the longest.
- Which splitter a level goes to is decided by `any(len(d) > 1 ...)` on Python
  **strings**, so it counts characters. An em dash is one character and three
  bytes; a level holding em dashes and nothing longer would be sent to the
  single-byte splitter, which tears the dash into three delimiter bytes. See
  `level.hasMultiByteDelimiter`.
- A merged piece's token count is the **sum** of the counts that were merged,
  not a recount of the joined text, and with a SentencePiece vocabulary the
  two differ (measured: 60 against 13). chonkie branches on the sum.
- The last level must never decode. Decoding BGE-M3 ids loses every newline
  and some boundary spaces, so the chunk stops being a substring of its
  document and its citation's line range cannot be found. Both sides cut at
  token **start** offsets only — a character can belong to no token at all, so
  cutting at `(start, end)` pairs would drop it.
- `str.strip()` is not `strings.TrimSpace`: Python strips U+001C–U+001F and
  Go's `unicode.IsSpace` does not.
- `str.splitlines()` is not a split on `"\n"`: Python ends a line at eleven
  boundaries, including a form feed and U+001C–U+001E but **not** U+001F. The
  table detector is built out of line numbers, so one form feed moves every
  table offset after it.
- Python's `re` is Unicode where Go's `regexp` is ASCII: `\s`, `\w` and `\b`
  all mean something different, and RE2 has no lookahead at all. The caption
  and notes patterns are written out accordingly in `tablesafe`.
- A `tablesafe` chunk is **not** stripped. Python tests whether a piece is
  blank and drops it if it is; a piece that survives keeps the whitespace it
  was cut with.
- A table's labelled caption and notes appear **twice** in the output: once as
  the prose around the table, once carried onto the table's chunks. That is
  Python's behaviour, recorded in the fixtures, not a bug to tidy.

## Do not

- Do not change a boundary unless the fixtures demand it. A change that moves
  one is a change to every stored document's chunks, and needs a reindex
  argument of its own.
- Do not commit live document text, in a fixture or in a failure message. The
  comparison prints offsets and lengths for that reason.
- Do not reimplement anything the Rust core already does. If a split or a
  merge needs something the shim has not got, extend the shim.
- Do not give `recursive` or `tablesafe` a default `Sizer`. The refusal is the
  feature.
- Do not port `CodeChunker`. `should_use_code_chunker` is `False` at every
  call site in this tree, so nothing has ever been chunked with it, and a
  reproduction of it would be a tree-sitter parity problem with no corpus to
  check it against.
- Do not compute a chunk's place in the document by searching for its text.
  `tablesafe.Chunker.Pieces` returns the span each chunk was cut from
  (Python's `raw_start`/`raw_end`); a table chunk begins with a header that is
  not at that place in the document, so a search finds the wrong lines or
  none.
