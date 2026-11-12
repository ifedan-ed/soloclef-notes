# soloclef_celery

The Celery wire protocol in Go. Required by `soloclef_worker` and
`soloclef_api`; it serves no process of its own. Nearest `AGENTS.md` wins; the
root one owns organisation and TDD.

## Why it is a module and not a package

Both Go tiers publish Celery tasks — the worker tier's beat publishes the
schedule, the API tier publishes the background work its writes owe — and both
have to be indistinguishable from Celery's own Python client, byte for byte. A
second copy of the encoder is a second spelling, and the whole point is that
there is one. `internal` cannot be shared across modules, so this is a module,
required with a relative `replace` from each tier rather than a tagged version:
both are deployed from this checkout, and a tag would let them publish different
bytes for the length of a version bump.

The decision and what it replaced are in `custom/docs/go-api-tier.md`, "Where
the encoder lives".

## Layout

| Path | Role |
|------|------|
| `celery` | Protocol 2: the envelope in and out, Python's `json.dumps` spelling and `repr`, `celery.Mapping` for an argument that is a dict whose key order is read, and the `celery-task-meta-<id>` result rows. No I/O. |
| `queues` | The Redis list keys kombu holds a queue's work in, one per priority step. |
| `monotonic` | `CLOCK_MONOTONIC`, the clock `surfsense.enqueued_at_ns` is in. |
| `routing` | `app/celery_app.py`'s `task_routes`: the heavy queue, the connectors queue, and the default everything else falls through to. |
| `produce` | Putting a task on a queue: the ids, the routing, this app's two `before_task_publish` headers, `countdown`, `expires`, the limits, and one `LPUSH`. |

## Do not

- Do not pass a `map[string]any` where the key order can be read. Python writes
  a dict in insertion order, in the body's `json.dumps` and in the `argsrepr`
  header alike; `pyJSON` and `pyRepr` sort a map's keys, which agreed with
  Python only while no envelope carried an object with more than one key. The
  first one that does is `push_send`, whose single argument is the nine-key
  payload `app/push/events.py:_payload` builds. `celery.Mapping` is the ordered
  form and `ordered_mapping_test.go` has CPython's own output for it.
- Do not add a dependency. The two tiers reach Redis with different clients
  (go-redis and valkey-go), and the one call a producer makes is
  `produce.Broker` — one method, satisfied in five lines on either side. A Redis
  client here would make one tier carry the other's.
- Do not change a spelling on judgement. Every field, every separator, every
  header Celery writes `null` and the two headers this app adds are there
  because an envelope published here was compared with one Celery's own client
  published for the same call. Four divergences were found that way and none
  of them was visible from inside Go. Both ends are Go now, but a message
  already queued was written in this spelling and must still be read.
- Do not rename `surfsense.enqueued_at_ns` or the queue names. They are the
  names on every queued message and in every deployment's settings
  (`SOLOCLEF_GO_CELERY_DEFAULT_QUEUE`); a rename is a migration of its own.
- Do not guess at a Celery option this app does not use. `expires` as a
  datetime, chains, callbacks, `shadow` and a non-empty exchange are each
  absent on purpose: writing one without a comparison against Celery would put a
  value in the envelope that Celery did not compute.

## Commands

```bash
cd soloclef_celery && go test ./...
cd soloclef_celery && go vet ./... && gofmt -l .
```

The envelopes were compared byte for byte with Celery's own client before the
Python app was removed; the unit tests hold what that comparison found
(`ordered_mapping_test.go`, `produce_test.go`).
