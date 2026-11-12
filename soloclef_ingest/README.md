# soloclef_ingest

Document parsing for the Go indexing pipeline: a file from storage in,
Markdown and its page offsets out, or a typed error saying why not.

| Package | What it is |
|---|---|
| `parse` | The contract: `Parser`, `Source` (an opener, so files stream), `Document`, the typed `Error` |
| `selection` | The per-source choice (`Selection`) and the `Registry` that routes a file to the chosen parser |
| `parserconfig` | Builds the registry from the environment; names any setting that is missing |
| `localservice` | Local: client for `soloclef_parse_service` (pymupdf4llm) |
| `datalab`, `llamaparse`, `unstructured` | The hosted parsers |
| `textfile`, `delimited`, `htmlmarkdown` | Read in Go: text and Markdown, CSV/TSV to a table, HTML to Markdown |
| `filekind`, `utf8text`, `multipartfile`, `hostedapi` | Extension to format, strict text decoding, streamed uploads, HTTP failures to typed errors |
| `chunkflow` | A test only: parsed Markdown going into `soloclef_chunker` |

A source names one of `local`, `datalab`, `llamaparse`, `unstructured`. That
parser reads its documents and no other does: one with no key is listed as
unavailable and refused if chosen; a file it does not read is refused with
the list of what it reads. Text, Markdown, CSV, TSV and HTML are read in Go
whatever was chosen.

```bash
gofmt -l . && go build ./... && go vet ./... && go test ./... -count=1
go test ./textfile/ ./delimited/ ./htmlmarkdown/ -run '^$' -bench . -benchmem
bash scripts/parse_service_container_test.sh    # Local, against a throwaway container
```

`chunkflow`'s test links the chunker, which needs its Rust static library
built first (`soloclef_chunker/AGENTS.md`).

Tests that call a real hosted API are skipped unless that API's key is set,
and are billed for two pages each: `DATALAB_API_KEY`; `LLAMA_CLOUD_API_KEY`
with `LLAMA_CLOUD_BASE_URL`; `UNSTRUCTURED_API_KEY` with
`UNSTRUCTURED_API_URL`. Every other test uses replies written by hand from
each API's documented shapes (`*/testdata`).
