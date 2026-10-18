# Playwright E2E suite

End-to-end tests for the SurfSense web app (notes.example.com), notebooks-first.
There are two targets and they never mix:

| Project | Runs against | May write? | Command |
|---|---|---|---|
| `desktop` (1280×800) | the isolated e2e stack | yes | `pnpm test:e2e` (both) or `pnpm test:e2e:desktop` |
| `phone` (390×844, touch, `isMobile`) | the isolated e2e stack | yes | `pnpm test:e2e` (both) or `pnpm test:e2e:phone` |
| `prod-smoke` | production, read-only | **no** | `PROD_E2E_TOKEN=… pnpm test:e2e:prod-smoke` |

`setup` (tests/auth.setup.ts) runs automatically before `desktop` and `phone`.

## The isolated stack

`docker/docker-compose.e2e-web.yml` is a complete, throwaway copy of the app
under its own compose project, **`surfsense-web-e2e`**:

- Postgres on tmpfs (a fresh database every `up`), Redis, zero-cache;
- the backend and Celery worker from the `e2e` Dockerfile target, started through
  `surfsense_backend/tests/e2e/run_backend.py` / `run_celery.py`, which swap in
  fake chat/quick/embedding models and third-party SDKs and mount the test-only
  routes (`/__e2e__/auth/token`, `/__e2e__/seed/studio-output`);
- the frontend built from this checkout (`surfsense-web-e2e-frontend:local`);
- Caddy with the live routing plus `/__e2e__/*` (`docker/proxy/Caddyfile.e2e`).

Only the proxy is published, on **127.0.0.1:3940** (`E2E_WEB_PORT` to change).
Everything else sits on an internal network with no route out, and the Python
services also get an unreachable HTTP(S) proxy and sentinel API keys, so no
test can reach a paid provider. It does not touch the live `surfsense` compose
project, its volumes, or `docker/.env`.

```bash
cd surfsense_web
pnpm install
pnpm test:e2e:install          # Chromium for Playwright, once
pnpm test:e2e:stack:up         # build images if missing, start, wait, seed the e2e user
pnpm test:e2e                  # desktop + phone
pnpm test:e2e:stack:down       # stop and delete everything (compose files stay)
```

After changing app code, `pnpm test:e2e:stack:rebuild` rebuilds both images
(the frontend build takes about a minute) and restarts the stack.
`bash scripts/e2e-stack.sh status|logs [service]` for diagnostics.

## What is real and what is faked

Real: Postgres, Redis, Celery, zero-cache, Caddy, the backend's routes,
permissions and persistence, indexing (with fake embeddings), the frontend
build, and every browser interaction.

Faked, in the backend e2e entrypoints (never in production code):

| Flow | How it is covered |
|---|---|
| Chat answers | `fakes/chat_llm.py`: `E2E_ANSWER` → a fixed answer; `E2E_CITE:<chunk id>` → an answer citing that chunk (the spec reads a real chunk id from the API). Streamed through the real `/api/v1/new_chat`. |
| Follow-ups, welcome questions, Studio focus ideas, source guides | `fakes/quick_llm.py` (deterministic `E2E …` replies) |
| Studio generation | Not run (needs the sandbox and paid models). Specs assert the dialog and the exact `POST /studio/generate` body. Audio is queued for real (TTS points at a sentinel key; the run then fails offline); other kinds get the stack's 503 "Generation is not enabled", shown inline. |
| Studio viewers | `POST /__e2e__/seed/studio-output` saves finished outputs through the production `save_artifact` path (real files in storage, document rows) and links them as Studio does, for every kind and for running/failed states. |
| An answer looked at mid-stream | The fake model answers at once, so `chat-steps-line.spec.ts` replaces `POST /api/v1/new_chat` in the browser with a stream the test pushes, in the backend's event shapes; the reload reads the real thread with the saved answer added to its messages. |
| Research search | The isolated stack has no internet: the real backend is used for the failure path; result lists come from a browser-side SSE stub with the backend's event shape. The import that follows is real. |
| Sources | Created as a user would: a note, then "Convert to source" (real NOTE document, real Celery indexing). Uploads go through the real upload route with the fake parser. |

## Layout checks (phone)

`tests/helpers/layout/overflow.ts` runs after every page, dialog, sheet and
menu is opened (`expectNoOverflow(page, label)`):

- no horizontal document scroll (`scrollWidth <= clientWidth`);
- no visible element past the viewport's left/right edge, except inside an
  element that scrolls sideways on purpose (`overflow-x: auto|scroll`); content
  clipped by an `overflow: hidden` ancestor counts only where it shows;
- on phones, text inputs (inputs, textareas, contenteditables) are at least
  16px, so iOS does not zoom on focus;
- `expectHeaderClearsSafeArea(header)`: the notebook header is padded by
  `env(safe-area-inset-top)` (Chromium cannot emulate a notch, so this checks
  the CSS rule that applies to the header).

Failures are soft (the test continues and lists every offender with a selector
and its left/right edges). `tests/app/overflow-helper.spec.ts` proves the
checker catches what it claims to.

Every app test also fails on a native `alert`/`confirm`/`prompt` and on an
uncaught page error (tests/app/fixtures.ts).

## Specs (tests/app)

| Spec | Area |
|---|---|
| `home.spec.ts` | Notebooks home: empty state, create, search, All/Recent, rename, pin/unpin featured, copy, delete |
| `notebook-sources.spec.ts` | Notebook layout (desktop panels / phone tabs, safe area), header rename, Add sources dialog (workspace / upload with Local parser / connector), sources list toggles, select all, source guide, remove, pick from the workspace |
| `research.spec.ts` | Research box: Web, PubMed, arXiv, medRxiv/bioRxiv, Google Scholar; fast/deep mode; select and import; offline failure path |
| `chat.spec.ts` | One chat per notebook, welcome card (guided starts / suggested questions), streamed answer, follow-ups, save to note, citations, delete chat history, legacy chat links, typing while the chat loads |
| `chat-steps-line.spec.ts` | An answer's steps are one line while it works (the step it is on, with its icon, by the clock), the list only from that line (inline / phone Summary sheet), the line kept when done and after a reload; the closed list out of a screen reader's and Tab's reach; a screen reader told the step once the line settles on it (a polite status, once per step, never the clock). The email answer is streamed by a browser-side stub of `new_chat` (`helpers/ui/chat-stream-stub.ts`, `helpers/ui/email-search-turn.ts`); `SHOTS_DIR=<dir>` saves a screenshot per stage |
| `mentions-prompts.spec.ts` | `@` lists only this notebook's sources and sends the pick; `/` saved prompts |
| `studio-dialogs.spec.ts` | Every generate dialog: defaults ("Default: X", "(Default)"), segmented options, style gallery + custom style, report templates, focus ideas, source picker, the generate request |
| `studio-outputs.spec.ts` | Every viewer on seeded outputs: audio player + transcript, video, PDF / Word / interactive reports, slides, mind map, flashcards, quiz, data table, infographic; running/failed rows, retry, rename, delete |
| `notes-and-menu.spec.ts` | Notes (autosave, convert to source, delete); notebook menu: customize, configure chat, copy, pin, create |
| `user-settings.spec.ts` | Every user-settings tab; profile, delete-account dialog (not confirmed), API keys, prompts, appearance |
| `workspace-settings.spec.ts` | Every workspace-settings tab; rename, Studio outputs model (per-type override, back to recommended), provider dialog, connectors search, team roles, system instructions |
| `automations-team-logs.spec.ts` | Automations list/builder/detail/edit/delete, team invites, logs |
| `navigation-auth.spec.ts` | Legacy redirects, unknown workspace, app shell navigation, sign-in, wrong password, logout |
| `prod-guard.spec.ts` | The prod-smoke write guard, proven on the isolated stack |

## prod-smoke (read-only, production)

`tests/prod-smoke/read-only.spec.ts` navigates production, opens dialogs and
closes them, and runs the layout checks at both viewports. It never submits a
form. On top of that, `tests/prod-smoke/guard.ts` aborts, in the browser, every
request that is not GET/HEAD/OPTIONS, except these reads that the app
implements as POST:

- `POST /api/zero/query` (Zero query resolution);
- `POST /api/v1/messages/comments/batch` (comment counts for visible messages);
- `POST /api/v1/workspaces/<ws>/notebooks/<nb>/followups` (follow-up questions).

It also blocks two GETs that would spend model tokens and write a cache:
`/studio/suggestions` and `/sources/<id>/guide`. Blocked requests are attached
to each test's report as `blocked-writes`. (An app write that the guard
aborts, e.g. creating a chat for a notebook that has none, just fails in the
page; nothing reaches the server.)

```bash
PROD_E2E_TOKEN=<access token for a real user> pnpm test:e2e:prod-smoke
```

Optional: `PROD_BASE_URL` (default https://notes.example.com),
`PROD_WORKSPACE_ID`, `PROD_NOTEBOOK_ID` (default: the notebook with the most
sources), `PROD_EXTRA_COOKIES="name=value; …"` (e.g. the SSO proxy's session
cookie in front of the app), `PROD_EXTRA_HEADERS='{"X-…":"…"}'`.

To dry-run it against the isolated stack:

```bash
TOKEN=$(curl -s -X POST http://127.0.0.1:3940/__e2e__/auth/token \
  -H 'Content-Type: application/json' \
  -H 'X-E2E-Mint-Secret: e2e-mint-secret-not-for-production' -d '{}' | jq -r .access_token)
PROD_BASE_URL=http://127.0.0.1:3940 PROD_E2E_TOKEN=$TOKEN pnpm test:e2e:prod-smoke
```

## Writing tests

- Import `test`/`expect` from `tests/app/fixtures.ts`; use the `workspace`
  (fresh per test) and `notebook` fixtures so tests stay parallel-safe.
- Prefer roles and accessible names; add a `data-testid` only when there is no
  stable accessible name.
- Call `expectNoOverflow(page, "<what is open>")` after opening anything.
- Seed data through the real API (`tests/helpers/api/*`) or, for Studio outputs,
  `seedOutput()`.
- Known gap worth knowing: the automation builder still requires premium/BYOK
  image and vision models for creation (the backend dropped that policy), so
  in this stack creation is checked as blocked and the detail/edit/delete flow
  starts from an automation created through the API.
