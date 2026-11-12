# soloclef_agent

The agent loop Soloclef's chat runs on, as a small Go library: call the
model, run the tools it asked for, show it the results, repeat until it
answers. Around that loop it provides the things a production chat needs
and nothing else: a typed stream of events, pauses for human approval that
survive a restart, a checkpoint after every step, nested agents, and
cancellation through `context.Context` everywhere.

## Why it exists

The chat ran on Python langgraph. A study of that code
(`custom/docs/soloclef-go-chat.md`) found that almost none of langgraph was
in use: the whole "graph" was one call to the stock two-step loop with
twenty-six middleware add-ons hung on it, and nothing in the chat defined a
node or an edge. What the framework did add was cost: a compile step slow
enough (4 to 5 seconds) to need a cache, which then held each user's trusted
tools past their removal; 2,072 lines of bridging so that an approval inside
a specialist could reach the person; a paused step that is run again from
its first line to deliver an answer; and a per-thread step counter that
makes a long conversation stop answering.

This library is that loop written out directly, with those defects designed
out rather than patched.

## Layout

| Package | What it is |
|---|---|
| `agent` | The loop. `Start`, `Resume`, `Continue`; hooks; approvals; delegates (nested runs). |
| `tool` | A tool: name, schema, function, approval policy, whether it may run in parallel. `WithTimeout`, `AskUser`, `Collect`. |
| `model` | The model interface, messages, `WithRetry`. |
| `model/openaicompat` | The one adapter: streamed chat completions over HTTP, which is what LiteLLM speaks for every model. |
| `model/modeltest` | A scripted model for tests, exported so code built on the loop can use it. |
| `event` | The event stream a run emits. No protocol knowledge. |
| `uistream` | Translates events into the server-sent-event frames the web and phone apps read. The only package that knows the client protocol. |
| `checkpoint` | The store interface and an in-memory store. |
| `checkpoint/postgres` | The Postgres store (pgx). The only package that imports a driver. |
| `checkpoint/checkpointtest` | The contract every store must keep, run against both. |
| `citation` | One numbering of sources per thread, shared by every agent in a run. |
| `graph` | A small typed state graph for fixed pipelines. Optional; the loop is not built on it. |
| `cmd/chatturn` | An example: one real turn with one tool against LiteLLM. |

## Using it

```go
client, err := openaicompat.New(openaicompat.Config{
    BaseURL: "http://litellm.notes.example/v1", APIKey: key, Model: "some-model",
    HTTPClient: &http.Client{},
})
m, err := model.WithRetry(client, model.RetryPolicy{Attempts: 3, Backoff: time.Second, MaxBackoff: 10 * time.Second})

a, err := agent.New(agent.Config{
    Model:            m,
    System:           "You are helpful.",
    Tools:            []tool.Tool{search, writeNote},
    MaxSteps:         80, // per turn, never per thread
    MaxParallelTools: 8,
    Store:            store, // checkpoint/postgres in a server
})

res, err := a.Start(ctx, threadID, []model.Message{model.User(text)}, sink)
```

`agent.New` validates and builds two small maps. It costs about 33
microseconds for forty tools and two delegates, so an agent is built per
turn from that turn's own prompt, tools and permissions. There is nothing to
cache between turns and so nothing to invalidate.

Every required setting is required: `New` refuses a config with no model, no
store, no step limit or no parallel limit, and a tool that does not say
whether it needs approval. There are no defaults that guess.

### Tools

```go
tool.Tool{
    Name: "write_note", Description: "...", Parameters: schemaJSON,
    Approval: tool.Ask,   // Auto, Ask or AlwaysAsk: no zero value
    Parallel: false,      // true only for tools safe beside other calls
    Run: func(ctx context.Context, call tool.Call) (string, error) { ... },
}
```

An error from `Run` is shown to the model as a failed result and the turn
goes on. A panic stops the run with a `*agent.PanicError` and does not take
the process down. `call.Citations` numbers a source the model may cite,
`call.Notify` sends the page a progress notice, and returning
`tool.AskUser(payload)` pauses the turn with a question; the tool is called
again with `call.Answer` set.

A remote tool listing is read with `tool.Collect`, which follows the cursor
to the last page and treats a listing cut short as an error.

### Approval

A call is decided in a fixed order, and each rule is final:

1. An unknown tool, or arguments that are not a JSON object: an error
   result the model can act on.
2. A delegate runs; its own tools carry their own policies.
3. `tool.Auto` runs.
4. `tool.AlwaysAsk` asks. Nothing below is consulted.
5. `tool.Ask` runs if the person said "always allow" earlier in this turn,
   or if `Config.Trusted` says so now; otherwise it asks.

When any call of a batch must ask, the turn pauses with nothing of that
batch run, is saved, and `Start` returns `Paused`. The stream carries one
`ApprovalNeeded` event per waiting call across the whole tree of nested
agents. `Pending` lists the same calls from the saved state (for a page
that reloaded), and `Resume` takes one decision per call: approve, approve
with edited arguments, approve always, reject with a reason, or answer. A
set of decisions that does not match the waiting calls exactly is refused
whole and changes nothing.

Nothing waits in memory. A turn paused in one process is resumed by any
process that can reach the store; the tests do this through Postgres.

### Hooks

Behaviour around the loop's two actions is a function handed the call and
the next step:

```go
type ModelHook func(ctx context.Context, call ModelCall, next ModelFunc) (model.Response, error)
type ToolHook  func(ctx context.Context, call tool.Call, next tool.Func) (string, error)
```

They stack in slice order, the first outermost. A tool hook can change a
call's arguments, refuse it, rewrite its result, or pause the turn to ask
the person (the "same call three times, carry on?" check is a dozen lines).
`agent.ContinueCutOff` is a model hook that finishes an answer the output
cap cut short. Three things are deliberately not hooks, each for a reason
given in `agent/hooks.go`: what the model is shown without it being stored
(`Config.Prepare`), who must approve a call (the fixed order above), and
retrying a failed request (`model.WithRetry`).

### Nested runs

A `Delegate` offers another agent to the model as a tool that takes a task.
The nested agent runs the same loop on a thread of its own, named after the
parent's thread and the call. This is how a specialist that builds
artifacts (decks, quizzes, podcasts, reports) is run. Everything it needs
crosses the boundary without bridging code:

- Its own tools, hooks and limits: it is an ordinary agent.
- Its approvals and questions: the parent pauses, the waiting calls are
  announced by the outermost run with ids like `k1/w1`, and a decision for
  that id is routed down to the call that asked.
- Its citations: both runs number sources in the one registry, so there is
  no second registry to merge and no label is ever renumbered.
- Its progress: every event of the nested run reaches the outermost sink
  with `Parent` set, including the notices its tools send.
- Cancellation: the run's context reaches the nested agent's model call and
  tools. A stopped run keeps the nested thread so `Continue` can go on; a
  new `Start` answers the abandoned call "interrupted" and removes it.
- A time limit: `Delegate.Timeout` turns a specialist that runs too long
  into one failed tool result, and removes its thread.

A crash between two saves is handled by reading where the nested thread
actually stands, not what the parent last recorded about it.

### Streaming to the apps

```go
w, err := uistream.New(uistream.Config{
    Out: rw, Flush: http.NewResponseController(rw).Flush,
    Now: time.Now, NewID: uistream.RandomID,
    Card: cards, Approval: approvalCard,
})
w.Begin(assistantMessageID)
w.Data("turn-info", info)            // the chat layer's own frames
go w.KeepAlive(ctx, 15*time.Second)
res, err := a.Start(ctx, thread, input, w.Event)
w.Data("turn-status", idle)
w.End()                              // or w.Fail(message, code)
```

The frames are a client contract, read by the web app's `readSSEStream` and
by the phone app, so they are kept exact: the tests compare the writer's
output byte for byte with frames printed by the Python service's own
encoder (`uistream/testdata`). The seven ordering rules the readers depend
on are listed at the top of `uistream/writer.go`, and each has a test. A
tool's output reaches the browser only through the `Card` function, so what
is shown is an allowlist by construction.

## The seven defects, and where each is fixed

| Defect in the Python chat | Here |
|---|---|
| "Always Allow" worked on always-ask tools, and trust outlived its removal in a compiled-agent cache. | `tool.AlwaysAsk` is checked before any trust and refuses an "always" answer. `Config.Trusted` is called at each decision and never remembered; turn-level trust is cleared when a turn starts. There is no cache. |
| Every MCP tool was denied in the Documents scope. | The library has no scope rule that can deny by accident. An MCP tool is given to the agent as `tool.Ask`: allowed, asking first. |
| A thread stopped answering after 120 model calls over its life. | `MaxSteps` is counted per turn and reset by `Start`. |
| The memory block was saved into the conversation every turn. | `Config.Prepare` builds what is sent; what it adds is never stored. Neither is the system prompt. |
| Tool listing read only the first page. | `tool.Collect` follows the cursor, and errors rather than return a short list. |
| Parallel sub-runs' citations were renumbered on merge while prose kept the old numbers. | One `citation.Registry` per thread, shared by the whole tree, saved with the thread. Nothing is merged. |
| A 4 to 5 second agent build per turn, hidden behind a cache. | `agent.New` takes tens of microseconds (`BenchmarkNew`). |

## What we did not copy from langgraph, and why

- A graph as the core. The chat never used one. The loop is two steps
  written as a loop; `graph` exists for real pipelines and is a separate,
  optional package of about 450 lines.
- A compile step. Compiling is what made construction slow, the cache
  necessary and stale trust possible. `New` only validates.
- Channels, reducers and the clear sentinel. One step runs at a time and
  owns the state, so there is nothing to reduce. Parallel tool calls write
  to their own slots of the batch and are flushed in call order.
- `Command`, `Overwrite`, `jump_to`. Control flow is the loop's own code. A
  tool that needs to affect the run returns a result, an error or a
  question.
- Rerunning a whole step to deliver an answer. A resume skips every call
  that already has a result. Only a tool that itself asked a question is
  called again, and that is visible in the tool's own body.
- Interrupts as exceptions caught, stamped and raised again through each
  level of nesting. A nested agent's pause is its run returning `Paused`.
- A checkpoint namespace per subgraph with hashed thread ids. A nested
  thread is named `parent/callID`, which is all that links the two.
- A full history of snapshots per thread. One row per thread, replaced on
  each step, guarded by a sequence number so two runs cannot interleave.
  Rewinding is `Edit`.
- A serialiser allowlist and pickled state. State is plain JSON, readable
  with `psql` and `jq`.
- Numbered middleware slots with their own state schemas. A hook is a
  function; order is slice order.
- A callback and tracing system threaded through every call. The event
  stream is the one output channel, and a hook is where a trace is taken.
- Silent defaults, such as a recursion limit of 25 that nobody chose.
  Every such choice here is a required field or a named value
  (`event.Discard`, `checkpoint.NewMemory()`).
- The Vercel AI SDK protocol as such. The apps do not use that SDK. The
  contract is the frames their own readers accept, and it lives in one
  adapter package.

Also not built, because nothing needs it:

- Valkey. It was considered for cross-process approval resume and for
  trust invalidation. Resume is already cross-process, because a paused
  turn is a row in the store and the sequence check decides which process
  takes it. Trust needs no invalidation, because it is read at each
  decision and held nowhere. The one cross-process signal a chat server
  does need is Stop, and that belongs to the server: it cancels the
  context of the run, which is all this library asks for.
- An MCP client, summarisation, prompt-cache markers, the in-stream
  citation rewrite and the tool cards. They are the chat layer's, built on
  `Config.Prepare`, hooks, `tool.Collect`, the registry and `uistream.Card`.

## Benchmarks

Loop overhead with the model answering at once and the store in memory, on
an AMD EPYC 9645 (`go test ./... -run xxx -bench .`):

| What | Cost |
|---|---|
| One step (model call or tool batch, saved) | 7.3 µs |
| One tool call, alone | 11.4 µs |
| One tool call, eight in order | 4.4 µs each |
| One tool call, eight in parallel | 8.2 µs each |
| Four pass-through tool hooks | about 1 µs added per call |
| Handing a task to a nested agent and back (3 nested steps) | 28 µs |
| Building an agent, 40 tools and 2 delegates | 33 µs |
| One streamed token, model to sink | 39 ns |
| One text-delta frame written (`uistream`) | 118 ns, 0 allocations |
| One token parsed from the provider's stream | 2.7 µs |
| One graph step | 0.28 µs |
| One-step turn on a 50 KB / 200 KB / 1 MB thread | 0.6 / 2.7 / 14 ms |

The last row is the cost of saving whole state as JSON on every step: it
grows with the thread. It is small beside a model call, and it is the
reason a chat layer should summarise long threads through `Edit` rather
than let them grow without limit.

## Tests

```
go test ./... -count=1 -race
```

The Postgres store is tested only when `SOLOCLEF_AGENT_TEST_DATABASE_URL`
names a scratch database; without it those tests say they were skipped.
The example runs a real turn when `LITELLM_URL`, `LITELLM_KEY_FILE` and
`LITELLM_MODEL` are set, and otherwise says it was skipped and exits 0:

```
LITELLM_URL=http://127.0.0.1:4000/v1 LITELLM_KEY_FILE=/path/to/key LITELLM_MODEL=some-model go run ./cmd/chatturn
```
