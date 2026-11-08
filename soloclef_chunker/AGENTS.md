# soloclef_chunker

The chunker that decides where a stored chunk begins and ends, in Go. Nearest
`AGENTS.md` wins; the root one owns organisation and TDD.

## The constraint

The boundaries this module cuts are already in the database, against the
chunks and the vectors they produced. It is therefore not an implementation of
recursive chunking: it is a reproduction of chonkie's, as
`soloclef_backend/app/config/__init__.py` configures it, and a cut that lands
one character away from chonkie's is a corpus that no longer matches its own
index.

So nothing here is changed on judgement. A change is made because the
comparison against the Python chunker demanded it, and the comparison is run
again afterwards:

```bash
bash scripts/compare_corpus.sh            # every live document, both chunkers
go test ./... && go vet ./... && gofmt -l .
```

## Why it is a module and not a package

Indexing is the one part of the pipeline that still has a Python-only step
(parsing), and which Go tier ends up owning the rest of it is not settled. A
chunker in `soloclef_worker/internal` could not be used by `soloclef_api` and
the reverse is equally true, and a second copy is a second set of boundaries —
which is the one thing the byte comparison exists to prevent. `internal`
cannot cross a module, so this is a module, required with a relative
`replace`, exactly as `soloclef_celery/AGENTS.md` argues for the Celery
encoder.

It has no dependencies. A chunk boundary is decided from the text alone, so
nothing here reads configuration, a database or a network; the caller passes
the size it is configured with.

## Layout

| Path | Role |
|------|------|
| `recursive` | chonkie's `RecursiveChunker`: the level hierarchy, the delimiter cuts, the merge back up to the ceiling, and the final cut every N characters. |
| `tablesafe` | `chunk_text_hybrid`: a Markdown table block is emitted whole and the prose around it is chunked, with Python's `str.strip()` on every piece. |
| `cmd/chunkdump` | Chunks newline-delimited JSON documents, so the two implementations can be run over the same corpus. |
| `scripts` | The two proofs: the live-corpus comparison, and re-recording the fixture goldens from the Python chunker. |
| `testdata/documents` | Synthetic documents and the chunks Python cut from them. Synthetic on purpose — this repository has a public mirror. |

## What the Python side does that is easy to get wrong

Each of these moved a boundary during the port, and each has a test:

- `min_characters_per_chunk` (24) is named in characters and measured by
  chonkie-core **in bytes**. A document of em dashes cuts every eight
  characters, not every twenty-four.
- The last level counts **characters**, not bytes: it cuts every 512 code
  points, because the configured tokenizer is chonkie's `character` one.
- A delimiter cut is only taken where the piece it closes *and* the next raw
  piece both reach the minimum, so a one-word line pulls its neighbours
  together instead of being emitted alone.
- Within a level the delimiters are tried in the order the rules list them, so
  a position where two match is cut by the first one listed, not the longest.
- `str.strip()` is not `strings.TrimSpace`: Python strips U+001C–U+001F and
  Go's `unicode.IsSpace` does not.
- A table row must end in a newline, so a table that runs to the end of the
  document without one is prose.

## Do not

- Do not change a boundary without re-running `scripts/compare_corpus.sh`, and
  do not read its output as a licence to reindex: swapping production over is
  a separate decision with a reindex argument of its own.
- Do not commit live document text, in a fixture or in a failure message. The
  comparison prints offsets and lengths for that reason.
- Do not port `CodeChunker`. `should_use_code_chunker` is `False` at every
  call site in this tree, so nothing has ever been chunked with it, and a
  reproduction of it would be a tree-sitter parity problem with no corpus to
  check it against.
- Do not add a token-counting mode by guessing a tokenizer. The size unit is a
  single seam — `characterCount` in `recursive` — but a real tokenizer has to
  agree with the Python one token for token before it decides a boundary.
