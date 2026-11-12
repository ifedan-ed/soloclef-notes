# Usage and limits

Soloclef has no trial, no plans, no credits, no invite or redeem codes, and
nothing to buy. Nothing is metered: what your server can do is what your own
models and hardware allow, and the model bills are between you and your
providers.

## The limits there are

These are settings in `docker/.env.go`, the same for every account. They keep
one request or one person from taking the whole server, not from using it.

| What | Settings |
| --- | --- |
| The largest uploaded file, and files in one upload | `SOLOCLEF_API_UPLOAD_MAX_FILE_BYTES`, `SOLOCLEF_API_UPLOAD_MAX_FILES` |
| One chat turn's steps and running time | `SOLOCLEF_API_CHAT_MAX_STEPS`, `SOLOCLEF_API_CHAT_MAX_TURN_SECONDS` |
| Sandboxes one account may have at once, and their size | `SOLOCLEF_API_SANDBOX_MAX_PER_ACCOUNT`, `SOLOCLEF_API_SANDBOX_CPU`, `SOLOCLEF_API_SANDBOX_MEMORY` |
| Documents indexed at once, and background jobs at once | `SOLOCLEF_GO_INDEX_CONCURRENCY`, `SOLOCLEF_GO_WORKER_CONCURRENCY`, `SOLOCLEF_GO_WORKER_HEAVY_CONCURRENCY` |

Two limits are built in rather than set: the API answers at most 1,024
requests a minute from one client, and checks a password at most ten times a
minute from one client.

## Model costs

A person who adds a provider of their own under Settings → Models uses their
own key, so those calls are on their own bill. Everyone else uses the
server's models (`SOLOCLEF_API_CHAT_MODEL` and the others in
[Every setting](guide/settings-reference.md#models)), which are on yours. To
cap spending, set budgets in the LiteLLM proxy, which every model call goes
through.
