# soloclef_mcp_go

The MCP server, in Go. Talks to the Soloclef Notes backend over REST only and
holds no database of its own. Nearest `AGENTS.md` wins; the root one owns
organisation and TDD. The inventory, the design and what could not be ported are
in `custom/docs/go-mcp.md`.

## Why this exists

`surfsense_mcp` is a whole Python service with no database of its own, so it can
be replaced rather than shaved. This tree is that replacement.

## The one rule that makes this safe

**The tool surface is published.** A client has these tool names, parameter
names and descriptions written in its configuration, and the documentation at
`soloclef_web/content/docs/connect-your-agent.mdx` names many of them. Renaming
or dropping one breaks a setup silently, with no error anyone sees.

The contract is now this server and the Go API it calls (`soloclef_api`), not
the Python server it was ported from. The surface is still pinned, so that it
moves only on purpose:

| Path | Role |
|------|------|
| `internal/toolmanifest/testdata/tools_list.json` | The whole `tools/list`. The specification, and the regression guard. |
| `internal/toolmanifest/testdata/argument_refusals.json` | The sentence each bad argument is refused with. |
| `internal/mcpserver/testdata/tool_results.json` | What each tool answers against a stand-in backend. |

Each of those is checked by a test that fails if this server's answer moves.
All three were first captured from the Python server; they are edited now when
the surface changes on purpose, in the same change as the code and the
documentation.

There is no scope argument and no selection. A caller's API key or sign-in is
its account, and every tool reads and writes that account's data through the
Go API's user-keyed routes (`/api/v1/documents`, not a path with a scope id in
it).

## Where the mirrors are, and why each one exists

| Path | Role |
|------|------|
| `internal/pythonjson` | JSON as the Python tier handles it: member order kept, `json.dumps(indent=2, ensure_ascii=False)`, `repr()`, and `dict.get` with an f-string. Go's own encoder sorts members, escapes HTML and prints floats its own way, and each of those shows in a tool result. |
| `internal/toolschema` | One parameter declaration, from which both the published schema and the argument checking come. The schema is pydantic's shape; the refusals are pydantic's sentences, including its 50-character cap on a printed value. |
| `internal/restclient` | The authenticated REST transport and the one sentence each HTTP failure becomes. |
| `internal/callercredential` | The caller's credential and identity for one request. |
| `internal/signincheck` | `GET /verify-token`, with the same short memory of a yes. |
| `internal/protectedresource` | This server as an OAuth protected resource (RFC 9728). |
| `internal/httptransport` | The identity middleware, the public paths and CORS. |
| `internal/tooldefinition` | A declared tool, published through the SDK with the handler wrapped so a refusal is a text result. |
| `internal/mcpserver` | The composition root, the instructions, and the two receiving middlewares. |

## Commands

```bash
cd soloclef_mcp_go && go test ./...
cd soloclef_mcp_go && go vet ./... && gofmt -l .

# Every tool's name, title, description and schema, as a client is listed them.
cd soloclef_mcp_go && go run ./cmd/mcp dump-tools

# The offline check that the manifest is whole and each description routable.
cd soloclef_mcp_go && go run ./cmd/mcp selfcheck
```

## Do not

- Do not add, rename or drop a tool, a parameter or a description without the
  same change in the captures above and in `connect-your-agent.mdx`, in one
  change. `go run ./cmd/mcp dump-tools` prints what the capture should become;
  read the difference before writing it, because that difference is what every
  client will see.
- Do not let the SDK infer a schema from a Go type. `mcp.AddTool`'s generic form
  writes its own schema and its own refusals; both are part of the contract, so
  `Server.AddTool` is used with the schema this tree builds.
- Do not "simplify" a mirror of Python's behaviour. The repr truncation, the lax
  integer that accepts `" 3 "` and `"2.0"` but not `"2.5"`, the empty failure
  body that yields no detail rather than the word `None`, and the query
  parameters kept in written order each look like a quirk, and each is a case
  where the two servers were asked the same thing over HTTP and disagreed.
- Do not verify a credential here. The backend is the one judge of an API key,
  and this server only asks about a sign-in token, once, so a bad one earns a
  401 the client can act on. A second verifier would be a second answer.
- Do not give this server a database, a cache server or a model. It has none of
  those, which is what made it portable.
- Do not commit a fixture with an account's own text in it. This repository has a
  public copy; everything in `testdata` is synthetic and uses `example.invalid`.
