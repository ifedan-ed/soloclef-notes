# soloclef_parse_service

pymupdf4llm behind one HTTP endpoint. It is the one Python process the Go
indexing pipeline keeps: MuPDF's layout model (headings, reading order across
columns, real pipe tables) has no Go equivalent. It imports nothing from the
application.

- `POST /v1/parse?filename=<name>` with the file as the raw body. Answers
  `{"parser", "parser_version", "pages": [{"number", "markdown"}]}`. The
  document's Markdown is the pages concatenated in order, nothing between.
- A file that cannot be read is refused as `{"error": {"kind", "message"}}`:
  `unsupported_format` (415), `no_text_layer` (422, a scan; OCR is off) or
  `parse_error` (422). The other statuses are listed in `parse_service/http_api.py`.
- `GET /health`.

It reads `.pdf`, `.docx`, `.pptx` and `.epub`.

Settings, all required, none defaulted:

| Variable | Meaning |
|---|---|
| `PARSE_SERVICE_LISTEN` | `host:port` to listen on |
| `PARSE_SERVICE_WORKERS` | warm parser processes, which is also how many files parse at once |
| `PARSE_SERVICE_MAX_BYTES` | largest file accepted |
| `PARSE_SERVICE_PARSE_TIMEOUT_SECONDS` | longest one parse may run before its process is killed |
| `PARSE_SERVICE_QUEUE_WAIT_SECONDS` | longest a request waits for a free worker before `503 busy` |

```bash
uv sync && uv run pytest
docker build -t soloclef-parse-service:latest .
```

The Go client is `soloclef_ingest/localservice`; its opt-in test runs against
this image in a throwaway container (`soloclef_ingest/scripts/parse_service_container_test.sh`).

pymupdf4llm is pinned exactly at 1.28.2. MuPDF is AGPL; that stays a standing
item and a separate service does not settle it.
