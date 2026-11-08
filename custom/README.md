# Patched Soloclef backend

`Dockerfile` here is now just `FROM ${BACKEND_BASE}`: every local fix is in
`../soloclef_backend` itself, with tests, so a build no longer rewrites
upstream's code and an upstream pull that moves the code shows up as a merge
conflict rather than a failed patch. Build the source image, then this:

    docker build -t soloclef-backend-src:latest --target production ../soloclef_backend
    docker build -t soloclef-backend-fast:latest .

The stage is kept so the compose file and these commands stay the same, and
so a genuinely temporary patch has an obvious home. Prefer a source change.

The source tree is named `soloclef` throughout. A few values kept the old
`surfsense` spelling because they name something outside this tree — the
Celery queues, the `surfsense.enqueued_at_ns` message header, the database
and its role, cookies, the installed app's URL scheme — and each needs its
own window: `custom/docs/rename-soloclef.md` has the plan for every one.

## What the patches became (folded in on 2026-09-23)

Five build-time patches lived here until they were moved into source:

- `structured_retry_patch.py` -> `app/utils/structured_output.py`.
  `invoke_json` re-asks once when a reply fails validation, now with the
  model's rejected reply (`AIMessage`) and the concise validation errors in
  the conversation. `attempt_timeout` bounds each ask separately, so the
  vision reviews no longer share one 120 s budget across the retry, and
  `validation_context` lets a caller's rule (the video executor's "exactly
  N scenes") fail inside validation and be retried with "expected N scenes,
  got M".
- `video_review_advisory_patch.py` -> `review_video_stills.py`. An
  unreadable or timed-out still review returns `status: "unavailable"` and
  the video ships unreviewed; provider errors still propagate. The artifact
  verifier (`app/artifacts/verification/vision.py`) got the same rule: an
  unreadable reply is an `unavailable_reason`, not a blocking finding.
- `video_review_lenient_patch.py` and `video_summary_patch.py` ->
  `review_video_stills.py`. Unknown keys are ignored, evidence is trimmed to
  the caps (and object items flattened to text), near-miss verdicts are
  mapped and an unrecognised one becomes a noted warning, and any summary is
  coerced to one string within 500 characters. The prompt is now built from
  those same constants, so it states the limits the schema enforces.
- `embed_limit_patch.py` -> `app/utils/document_converters.py`. The
  embedding truncation fallback is 6000 tokens, overridable with
  `EMBEDDING_MAX_TOKENS`, because a generic tokeniser counted 8192 where the
  provider counted 8193 and refused every document over about eight thousand
  tokens.

`video_review_proof.py` exercised the review patches against a pristine copy
of the file; its reply shapes are now the table-driven tests in
`tests/unit/deliverables/test_video_services.py`.

## Deploying: wait for the gate

Restarting the backend or the Celery worker kills whatever is generating, so
every deploy waits on the deploy gate first:

    python3 soloclef_backend/scripts/deploy_gate.py          # check once
    python3 soloclef_backend/scripts/deploy_gate.py --wait   # poll 30 s, up to 60 min

It exits non-zero while any of these is in flight, and prints which:
`deliverable_jobs` queued, running or cancelling (every Studio output except
audio, and chat-started videos); running `soloclef-sandbox-local` containers
(an idle one lingers up to `SANDBOX_IDLE_TTL_SECONDS`, 15 min); Celery tasks
named for a deliverable, artifact, podcast or video (`celery inspect active`
in the worker); and `podcast_runs` drafting or rendering, or
`video_presentation_runs` pending or generating, from the last six hours
(older rows were orphaned by a crash and are not waited on). A probe that
cannot answer (the database or worker is down) counts as busy. Container
names are overridable with `SOLOCLEF_DB_CONTAINER`,
`SOLOCLEF_WORKER_CONTAINER` and `SOLOCLEF_SANDBOX_IMAGE`.

A deploy, then:

    python3 soloclef_backend/scripts/deploy_gate.py --wait && \
      (cd docker && docker compose -f docker-compose.yml -f docker-compose.override.yml \
         up -d backend celery_worker celery_beat)

and the same gate before `up -d frontend`. A chat turn still writing a reply
holds the gate too (`chat_session_state`, below).

## Deploying the release of 2026-10-11 (`release/soloclef-2026-10-11`, deployed 2026-10-11 09:31 UTC)

One deploy for four branches, merged in this order with `--no-ff` onto
`main` at `2c9a1dec2`: `fix/server-sign-in-options` (`0b232bc1b`),
`fix/parser-settings` (`3c2b6fbe1`), `fix/security-2026-10-11` (`49a04a7db`,
the one conflict: the admin paragraph of "Accounts" below, both sides kept)
and `fix/shared-outputs` (`066e5d85d`). Two migrations, both from the
security branch, one head after them: `c0029_zero_config_thread_access`
(the Zero publication only) and `c0030_verify_sso_linked_accounts` (data:
one account here). No sandbox or MCP image changes. The phone app's half is
`release/app-2026-10-11` (an over-the-air update, no new build;
`soloclef_mobile/RELEASING.md`, "The release of 2026-10-11"), published
after this deploy. Each section above and below says what its branch does;
this is the order, the checks and the undo for all of it, replacing the
branches' own deploy notes. Tested on the release branch: the backend unit
suite (5179 passed; the three failures `main` already has:
`test_git.py::...test_diff_reports_net_changes_against_base`,
`test_synthesize_narration.py::...[slides4-12-scene]`,
`test_account_export.py::test_committed_export_sample_matches_the_contract`),
the branches' integration tests on a throwaway Postgres (87 passed: OIDC,
c0029, c0030, public sharing, Google Drive, the app hand-off, account
connections; the OIDC ones need `SECRET_KEY` set) and, run on their own
(no database), the 4 crawler tests in a real Chromium,
`tests/integration/proprietary/web_crawler/test_pinned_egress_browser.py`
with `CRAWLER_TEST_CHROMIUM` set to Playwright's headless shell
(`~/.cache/ms-playwright/chromium_headless_shell-1243/chrome-headless-shell-linux64/chrome-headless-shell`;
4 passed, again on 2026-10-11), the website's unit tests (642) and `tsc` (no
error in a changed file; one fewer than `main`), ruff on every changed
Python file.

Run everything from `/home/danvics/docker/surfsense`, with

    C="docker compose -f docker-compose.yml -f docker-compose.override.yml"

**1. Read-only checks.**

- `git status --short` shows only ` M docker/docker-compose.override.yml`,
  ` M docker/searxng/settings.yml` (and the untracked
  `soloclef_backend/.flashrank_cache/`); the release touches neither file:
  `git diff --name-only main release/soloclef-2026-10-11 -- docker/docker-compose.override.yml docker/searxng/settings.yml`
  prints nothing. `git rev-parse --short main` is `2c9a1dec2`.
- The database is at `c0028_announcement_cleared_reads`:
  `docker exec soloclef-db-1 psql -U surfsense -d surfsense -At -c 'SELECT version_num FROM alembic_version'`.
- **Who c0030 will mark verified**, kept for the undo. The provider name is
  `OIDC_PROVIDER_NAME` (`grep '^OIDC_PROVIDER_NAME=' docker/.env`; no line
  means `authentik`):

        umask 077; docker exec soloclef-db-1 psql -U surfsense -d surfsense -At -c "BEGIN READ ONLY; SELECT u.id, u.email FROM \"user\" u WHERE u.is_active AND NOT u.is_verified AND EXISTS (SELECT 1 FROM oauth_account o WHERE o.user_id = u.id AND o.oauth_name = 'authentik' AND lower(o.account_email) = lower(u.email)); COMMIT;" \
          | tee docker/backups/c0030-accounts-$(date -u +%Y%m%dT%H%M%SZ).txt

  Expected: exactly one row, `<id>|daniel@danvics.com`. Stop if it is not.
  None: the address Authentik last gave for his link differs from his
  account's, and after the deploy his agent sign-in (MCP) by email is
  refused: check the address, then set his `is_verified` by hand. More than
  one: someone else would be verified; check each address is its owner's.
- The billing admin is verified, or its admin pages go with this deploy:
  `docker exec soloclef-db-1 psql -U surfsense -d surfsense -At -c "BEGIN READ ONLY; SELECT email, is_verified FROM \"user\" WHERE lower(email) = 'danitex@danvics.com'; COMMIT;"`
  is `danitex@danvics.com|t` (each address of `BILLING_ADMIN_EMAILS`).
- `curl -s -o /dev/null -w '%{http_code}\n' https://notes.danvics.com/api/v1/sign-in-options`
  is `404` (it was on 2026-10-11): the old backend.

**2. Merge, test, push.** One merge commit, so the undo is one revert:

    git merge --no-ff release/soloclef-2026-10-11 -m "Merge release/soloclef-2026-10-11 into main"
    M=$(git rev-parse HEAD); echo "$M"   # keep it for the undo

Then the tests on the merged checkout (nice, one at a time), and only then
the push:

    (cd soloclef_backend && nice .venv/bin/pytest -q -m unit -p no:cacheprovider -o addopts="" --import-mode=importlib tests/unit)
    (cd soloclef_web && nice npm run test:unit)
    git push origin main

Expected: the three known failures above and nothing else (5179 passed, 2
skipped, 1192 deselected); 642 web tests passed.

**3. Back up.** The database, `docker/.env` and the running images:

    umask 077
    DUMP=docker/backups/surfsense-$(date -u +%Y%m%dT%H%M%SZ)-before-2026-10-11-release.dump
    docker exec soloclef-db-1 pg_dump -U surfsense -d surfsense -Fc > "$DUMP" && echo dumped
    docker exec -i soloclef-db-1 pg_restore -l < "$DUMP" | grep -c 'TABLE DATA'   # as many as the last dump's, or more
    docker exec -i soloclef-db-1 pg_restore -l < docker/backups/surfsense-20261010T231356Z-before-c0028-notes-inbox.dump | grep -c 'TABLE DATA'
    docker exec -i soloclef-db-1 pg_restore -l < "$DUMP" | grep -c 'TABLE DATA public alembic_version '   # 1
    docker exec -i soloclef-db-1 pg_restore -f /dev/null < "$DUMP" && echo readable   # reads every byte
    ls -l "$DUMP"   # about the size of the last one (87 MB, 2026-10-11 01:14)
    ENVBAK="docker/.env.backup-$(date -u +%Y%m%dT%H%M%S)-before-2026-10-11-release"
    cp -p docker/.env "$ENVBAK" && cmp docker/.env "$ENVBAK"
    docker tag "$(docker inspect -f '{{.Image}}' soloclef-backend-1)" soloclef-backend-fast:pre-2026-10-11-release
    docker tag "$(docker inspect -f '{{.Image}}' soloclef-frontend-1)" soloclef-web-local:pre-2026-10-11-release
    docker tag soloclef-backend-src:latest soloclef-backend-src:pre-2026-10-11-release

(The images are tagged from the running containers, not `latest`, so the
undo returns to what ran even if `latest` was rebuilt meanwhile.)
`pg_restore -l` lists the archive's table of contents, which `pg_dump`
writes first; `pg_restore -f /dev/null` reads the data too, so a truncated
or damaged dump fails there.

**4. `docker/.env`:** drop the stale parser line (or replace its comment
block too, "Document parsers on this server" below):

    sed -i '/^ETL_SERVICE=LLAMACLOUD$/d' docker/.env
    diff "$ENVBAK" docker/.env          # only: < ETL_SERVICE=LLAMACLOUD
    grep -n '^ETL_' docker/.env         # ETL_CLOUD_SERVICE=DATALAB, ETL_DEFAULT_PARSER=CLOUD, ETL_CREDIT_BILLING_ENABLED=FALSE
    grep -c '^SOLOCLEF_PUBLIC_URL=https://notes.danvics.com$' docker/.env   # 1: the server every push now names
    grep -c '^NEXT_PUBLIC_SIGNUPS_OPEN=' docker/.env                          # 0: Request access, as now

Nothing else changes in `docker/.env`. It is read at step 6.

**5. Build**, one image at a time, nothing restarts:

    docker build -t soloclef-backend-src:latest --target production soloclef_backend
    docker build -t soloclef-backend-fast:latest custom
    (cd docker && $C build frontend)

The frontend goes through compose, which hands it `docker/.env`'s address,
support address, `OIDC_*` lines and `NEXT_PUBLIC_SIGNUPS_OPEN` as build
args ("This server's own settings"); its log must not say
`NEXT_PUBLIC_SITE_URL is empty`. Check the backend image has the two
revisions: `docker run --rm --entrypoint sh soloclef-backend-fast:latest -c 'ls /app/alembic/versions | grep -E "^c00(29|30)_"'`.

**6. The gate once, then the server and the website back to back:**

    python3 soloclef_backend/scripts/deploy_gate.py --wait && \
      (cd docker && $C up -d backend celery_worker celery_beat && $C up -d frontend)

`up -d backend …` runs the `migrations` container first (c0029, c0030, then
`app.zero_publication --verify`) and returns once the backend is healthy, so
the website starts after the server, which the shared outputs need (the new
public page reads the server's `files`). There is **no second gate wait
before the frontend**: from the moment c0029 runs until the new website is
up, zero-cache refuses every open tab of the old website (its schema names
`search_source_connectors.config`), and Zero reloads the tab onto the old
website, which is refused again, with its backoff (at most once a minute);
a wait there (up to 60 min while a Studio job runs) would stretch that.
Restarting the website stops no job: chat turns and Studio run in the
backend and the worker. **Restart zero-cache right after** (`docker restart soloclef-zero-cache-1`):
zero-cache 1.6.0 does not take added columns from a changed publication by
itself. On 2026-10-11 it kept replicating `new_chat_threads` as `id`,
`workspace_id` only and refused every new website tab and updated phone
(`SchemaVersionNotSupported`: "The \"new_chat_threads\".\"created_by_id\" column
does not exist or is not one of the replicated columns") for 11 minutes,
until it was restarted; it then rebuilt its copy (`/data/zero.db`) in under
a minute and refused nothing more. Clients reconnect by themselves. The same
holds for any migration that changes `ZERO_PUBLICATION`'s column lists.

**If step 6 fails**, the old server is already gone: compose replaces the
backend, worker and beat containers before it waits on `migrations` (the old
ones stopped and removed, the new ones created), so whatever fails, the API
is down until step 6 succeeds or is undone; the website is not restarted
(the `&&`), and its pages fail meanwhile. Where the database is:
`docker exec soloclef-db-1 psql -U surfsense -d surfsense -At -c 'SELECT version_num FROM alembic_version'`
(each revision runs in its own transaction, `transaction_per_migration`).

- **`migrations` exits non-zero** (compose: `service "migrations" didn't
  complete successfully`; `docker logs soloclef-migrations-1`): c0029
  failed, so the database is still at `c0028_announcement_cleared_reads`
  and nothing changed; or c0030 failed, at `c0029_…` (the new publication:
  every open tab of the old website refused); or the
  `zero_publication --verify` after them failed, at `c0030_…`. The new
  backend never started, so `docker exec soloclef-backend-1 …` has nothing
  to run in.
- **The backend never turns healthy, or restarts in a loop** (compose:
  `dependency failed to start: container soloclef-backend-1 is unhealthy`,
  or `docker ps` shows it `Restarting`; `docker logs soloclef-backend-1`):
  the database is at `c0030_…`; the workers did not start.
- **Everything started, but a check of step 7 fails** that its own line
  does not settle (the version, the migrations log, the publication, a
  repeating zero-cache error, the website as the test account).

Each: fix and run step 6 again, or the undo **after step 6** (below), all of
its steps; its database step runs in a one-off container of the new image,
so it works whether the new backend runs, loops or never started, and at
`c0028` it changes nothing. The gate's wait is skipped there while the
worker is not running.

**7. Checks**, right after:

- `docker exec soloclef-db-1 psql -U surfsense -d surfsense -At -c 'SELECT version_num FROM alembic_version'`
  is `c0030_verify_sso_linked_accounts`.
- `docker logs soloclef-migrations-1 2>&1 | grep -E 'c0029|c0030|marked|verified'`
  shows both revisions, `c0030: marked 1 SSO-linked account(s) verified` and
  `zero_publication shape verified.`; the step-1 query now returns no row,
  and `SELECT email, is_verified FROM "user" WHERE id = '<the id kept>'` is
  `daniel@danvics.com|t`.
- `docker exec soloclef-backend-1 python -m app.zero_publication --verify`
  prints `zero_publication shape verified.`
- `docker logs soloclef-zero-cache-1 --since 10m 2>&1 | tail -50`: no
  repeating error (one resync is fine); refusals of phones still on the old
  app (`SchemaVersionNotSupported`) are expected until their update.
- `docker logs soloclef-backend-1 --since 10m 2>&1 | grep -E '\[startup\] (parser|push):'`
  (and `soloclef-celery_worker-1`) prints nothing: no stale `ETL_SERVICE`,
  and the push address is good.
- `curl -s https://notes.danvics.com/api/v1/sign-in-options` answers as in
  "Sign-in options" below: `"service": "soloclef"`, `"sign_up": false`,
  `"sso"` with `"label": "Continue with Danvics SSO"` and `"app": true`. If
  `sso` is null or `app` false, do not publish the phone update: the
  updated app would offer no single sign-on here.
- On the website, as the test account (claude-probe@example.com, never
  Daniel's): the sign-in page offers Request access; Notes and a note; a
  chat whose reply arrives live; Connectors, an MCP server's edit view
  filled in after "Loading this server's settings…"; home search; the
  upload dialog (Datalab's list); a shared Studio output's public link
  opens in its kind's viewer (flashcards one card at a time) and its
  `<title>` names it
  (`curl -s https://notes.danvics.com/public/<token> | grep -o '<title>[^<]*'`);
  `/docs/self-hosting` and `/docs/getting-started` read right.
- Daniel's own, never in tests: his SSO sign-in and his agent sign-in
  (MCP); `danitex@` still has Billing → Admin.

**8. The phone app's update**, within minutes of step 7:
`soloclef_mobile/RELEASING.md`, "The release of 2026-10-11". Until a phone
has it, it works by polling (zero-cache refuses its copy of the schema), and
its sign-in screen does not read `/api/v1/sign-in-options`. Its own check
that an updated phone is back on Zero, on the phone itself: on its second
start, signed in, More → Report an issue reads **`Live data: Zero
(connected)`**, not `Live data: polling (zero-cache needs a newer app)` (the
app's update `7df8c50e2` puts the line there; the app logs nothing a person
can read otherwise); on the server at the same moment
`docker logs soloclef-zero-cache-1 --since 2m 2>&1 | grep -c SchemaVersionNotSupported`
is 0 (an un-updated phone adds a line at each start).

**Google Drive connect from the phone app, between step 6 and step 8.**
The hand-off now needs the app's PKCE challenge for `drive_connect`
("Google Drive connect is bound to the browser that started it" below): a
phone without the update gets "Update the app to connect Google Drive from
it, or connect it on the website" (400 `HANDOFF_CHALLENGE_REQUIRED`), as it
already does today for a Microsoft account, which the live server confirms
the same way. The app's update fixes both (`release/app-2026-10-11`
`3ac6be2e8`: an S256 `code_challenge` with the hand-off and, on
`status=confirm`, `POST /api/v1/app/handoff/complete` with the verifier and
its token, `features/workspace/connectors/handoff.ts`), so the message is
right for the phones it reaches. Drives already connected, and the Picker,
keep working throughout. Checked after step 8 on the test-account phone
(RELEASING.md, checks after C).

**Undo.** Every step can be undone; the order matters once step 6 ran.

**Before step 6** nothing runs the new code. Steps 2 to 5: `git revert -m 1 "$M"`
and `git push origin main` (never `git reset --hard` here: it would discard
the two live-config files); `cp -p "$ENVBAK" docker/.env`; the new `latest`
images are unused until an `up`, and
`for i in backend-fast backend-src web-local; do docker tag soloclef-$i:pre-2026-10-11-release soloclef-$i:latest; done`
puts the old ones back. The backups stay.

**After step 6, putting the old images back alone fails.** The old
`migrations` image runs `alembic upgrade head` on a database at `c0030`, a
revision it does not know, and exits 1, so compose starts neither the
backend nor the workers; and were it to start, the old website's schema
still names `search_source_connectors.config`, which c0029's publication no
longer has, so every tab of the old website is refused and reloads, for
ever. So the database goes back first, with the new image (still
`latest`), then the old images, server before website, in one go:

1. If the phone update went out, take it back first (RELEASING.md, "The
   release of 2026-10-11"): with the old server, a phone on the update is
   refused by zero-cache and polls, and its sign-in screen falls back to the
   generic button.
2. `python3 soloclef_backend/scripts/deploy_gate.py --wait`, while the
   worker runs (`docker inspect -f '{{.State.Running}}' soloclef-celery_worker-1`
   is `true`). After a failed step 6 it does not, nothing can be generating,
   and the gate, which counts a worker that cannot answer as busy, would
   wait its whole 60 minutes: skip it.
3. The database, from a one-off container of the **new** image, before
   step 5 puts the old one back under `latest` (the old image does not know
   c0029 or c0030). Stop the new backend and workers first: a backend that
   keeps restarting runs `ensure_publication` at each start
   (`DB_BOOTSTRAP_ON_STARTUP`, on by default) and would put c0029's Zero
   publication back after the downgrade removed it, so step 6's
   `zero_publication --verify` would fail. A container stopped by hand is
   not restarted; step 6's `up -d` brings them back with the old image:

       (cd docker && $C stop backend celery_worker celery_beat)
       (cd docker && $C run --rm --no-deps migrations alembic downgrade c0028_announcement_cleared_reads)
       docker exec soloclef-db-1 psql -U surfsense -d surfsense -At -c 'SELECT version_num FROM alembic_version'   # c0028_announcement_cleared_reads

   `migrations` is the compose service that runs `soloclef-backend-fast:latest`
   with the database address (`DATABASE_URL`) and `docker/.env`; the image
   has no `ENTRYPOINT`, only `CMD ["/app/scripts/docker/entrypoint.sh"]`
   (whose `migrate` role is `alembic upgrade head`), so the words after the
   service name replace that command and run `alembic` in `/app`, where
   `alembic.ini` is. `--no-deps` starts nothing else (the database is up);
   `--rm` removes the container (`soloclef-migrations-run-…`) when it ends.
   At `c0028` already (c0029 failed) it does nothing.
   c0030's downgrade changes nothing: the accounts it verified stay
   verified, which the old code does not mind (it links SSO by email without
   asking). c0029's downgrade is one `ALTER PUBLICATION … SET TABLE` (no
   table lock; zero-cache resyncs) back to the old shape:
   `search_source_connectors` published whole, **`config` included**, so
   every member of a workspace who syncs its MCP servers receives their
   URLs, headers and tokens again (rotate an MCP key of any workspace with
   more than one member that synced after this); `new_chat_threads` back to
   `id` and `workspace_id`, so the new website's tabs and an updated phone
   are refused from here until step 6.
4. `cp -p "$ENVBAK" docker/.env` (the old website's fallback file list is
   then LlamaCloud's, as before). After the database step, which reads the
   `docker/.env` the new image was deployed with.
5. `for i in backend-fast backend-src web-local; do docker tag soloclef-$i:pre-2026-10-11-release soloclef-$i:latest; done`
6. `(cd docker && $C up -d backend celery_worker celery_beat && $C up -d frontend)`:
   the old `migrations` finds `c0028`, its own head, and the old
   publication shape, and passes.
7. `git revert -m 1 "$M"`, the tests of step 2, `git push origin main` (the
   checkout matches what runs; deploying the release again later is a
   revert of that revert).

Checks after it: `alembic_version` is `c0028_announcement_cleared_reads`;
`docker logs soloclef-migrations-1` ends with `zero_publication shape verified.`;
`/api/v1/sign-in-options` is 404 again; the website loads and a chat's reply
arrives live; zero-cache logs no repeating error. Setting the accounts c0030
verified back is optional and not needed by the old code:
`UPDATE "user" SET is_verified = FALSE WHERE id IN (<the ids in docker/backups/c0030-accounts-*.txt>)`.
Restoring the step-3 dump instead is a last resort, with Daniel: it loses
everything written since, and zero-cache must sync again from nothing.

## Document parsers on this server (`fix/parser-settings`, 2026-10-11; `fix/llamacloud-v2`, 2026-10-12)

Every parser setting, its default and its limits are in the self-hosting
guide, "Reading documents"; the code is `app/config/parser_settings.py`
(both lines checked at startup) and `app/etl_pipeline/`. This server:

| Setting (`docker/.env`) | This server |
|---|---|
| `ETL_CLOUD_SERVICE` | `DATALAB`: Cloud is Datalab's Marker API, balanced mode (since 2026-09-27) |
| `ETL_DEFAULT_PARSER` | `CLOUD`: an upload or a connector sync that names no parser gets Cloud |
| `DATALAB_API_KEY` | set |
| `LLAMA_CLOUD_API_KEY` | set: LlamaCloud reads the types Datalab cannot open and any file Datalab fails on, and switching Cloud to it is one line. Since 2026-10-12 a new key, whose account the v1 API refuses: LlamaCloud reads only once `fix/llamacloud-v2` (below) is deployed |
| `ETL_CREDIT_BILLING_ENABLED` | `FALSE` (only hides the cost note; page charges follow `BILLING_MODE`) |
| `ETL_CACHE_ENABLED`, `AZURE_DI_*` | unset (off) |
| `ETL_SERVICE` | `LLAMACLOUD`, stale: remove it (below) |

**`unstructured` was removed** (`feat/drop-unstructured`). It was the third
value `ETL_CLOUD_SERVICE` took -- the bundled open-source library, no key --
and the heaviest dependency in the backend image. This server never used it
(`ETL_CLOUD_SERVICE=DATALAB`, and only one document in the database was ever
read by it, a PDF), so nothing here changes: Datalab, LlamaCloud and the
Azure DI accelerator are untouched and still chosen the same way, and
pymupdf4llm still backs Local.

What it took with it, stated plainly: **`.eml`, `.msg` and `.p7s` are no
longer readable at all** -- no parser here opens them -- and **`.heic` is
readable only through a vision model**, having lost the document parser
behind it. A file of one of those types is refused before it is stored, with
a message naming the formats that do work
(`app/etl_pipeline/unreadable_file.py`); nothing is stored as an empty
document. A server still naming `UNSTRUCTURED` in `ETL_CLOUD_SERVICE` or the
deprecated `ETL_SERVICE` is stopped at startup and told to pick `LLAMACLOUD`
or `DATALAB`. Adding an external API in its place is four edits, listed in
`app/etl_pipeline/capabilities.py`'s docstring.

`docker/docker-compose.yml` and `docker-compose.dev.yml` still set
`UNSTRUCTURED_HAS_PATCHED_LOOP=1`; nothing reads it any more, so it can go
with the next compose edit.

**The stale `ETL_SERVICE=LLAMACLOUD`.** It is the single parser setting from
before the Local/Cloud split, and the backend ignores it because
`ETL_CLOUD_SERVICE` is set. It was not quite harmless: compose also handed it
to the website, whose upload dialog used LlamaCloud's file list until
`/api/v1/etl/capabilities` answered. The website now derives that list from
`ETL_DEFAULT_PARSER` and `ETL_CLOUD_SERVICE` (`lib/etl/fallback-service.ts`,
Datalab's list here), and until the line goes the backend and both Celery
processes log at start:

    [startup] parser: ETL_SERVICE is ignored because ETL_CLOUD_SERVICE is set (Cloud is DATALAB); remove the ETL_SERVICE line.

An unknown `ETL_CLOUD_SERVICE` or `ETL_DEFAULT_PARSER` now stops startup
naming the allowed values (before, Cloud was silently unavailable). Remove
the line after a backup, then restart (the gate first); in the release of
2026-10-11 this is its step 4 ("Deploying the release of 2026-10-11", above):

    cd /home/danvics/docker/surfsense/docker
    cp -p .env ".env.backup-$(date -u +%Y%m%dT%H%M%S)-before-drop-etl-service"
    sed -i '/^ETL_SERVICE=LLAMACLOUD$/d' .env
    grep -n '^ETL_' .env    # ETL_CLOUD_SERVICE=DATALAB, ETL_DEFAULT_PARSER=CLOUD, ETL_CREDIT_BILLING_ENABLED=FALSE

The comment block above it (from `# Document parsing service: PYMUPDF4LLM,
LLAMACLOUD, or UNSTRUCTURED.`) still describes the single setting and
Basic/Premium. Optionally replace that block, the stale line included, with
an accurate one (instead of the `sed` above; it keeps the two live lines):

    python3 - <<'EOF'
    from pathlib import Path
    p = Path(".env"); s = p.read_text()
    start = s.index("# Document parsing service: PYMUPDF4LLM, LLAMACLOUD, or UNSTRUCTURED.\n")
    end = s.index("ETL_DEFAULT_PARSER=CLOUD\n")
    assert start < s.index("\nETL_SERVICE=LLAMACLOUD\n") < end
    p.write_text(s[:start] + """# Document parsing: each upload picks Local (pymupdf4llm, on this server) or
    # Cloud, the service ETL_CLOUD_SERVICE names; ETL_DEFAULT_PARSER is the choice
    # when an upload or a connector names none. Cloud is Datalab (balanced mode)
    # since 2026-09-27. LLAMA_CLOUD_API_KEY stays set, so LlamaCloud reads what
    # Datalab cannot open or fails on, and ETL_CLOUD_SERVICE=LLAMACLOUD (then a
    # restart through the deploy gate) switches Cloud to it. See custom/README.md,
    # "Document parsers on this server".
    ETL_CLOUD_SERVICE=DATALAB
    """ + s[end:])
    EOF

(The heredoc body is indented here for the README; paste it unindented.)

**Checked live on 2026-10-11**, with one public-domain page (the first
paragraph of the Declaration of Independence) through the backend's own
parser functions inside `soloclef-backend-1`, one attempt each, nothing
written to the database:

- Datalab read it in 6.4 s: 441 characters, 1 page, the heading and the
  paragraph intact.
- LlamaCloud refused the upload in 3.6 s: "You've exceeded the maximum number
  of credits for your plan." The key is valid and the API answers, but the
  plan's credits are spent. Until they reset (LlamaCloud's Free plan: 10,000
  credits a month, then 402 on every parse) or the plan is upgraded,
  LlamaCloud reads nothing here: not as Cloud, and not as Datalab's fallback.
  So on Cloud that day the LlamaCloud-only types the dialog offers (RTF, BMP,
  SVG, `.tif`, HWP, HWPX, `.docm`, `.dot`, `.dotm`, `.pptm`, `.pot`, `.potx`,
  `.xlsb`, `.xlw`) fail with "Failed to process file", and a file Datalab
  fails on fails outright. Local still reads PDF, Word, PowerPoint, EPUB and
  the common images.

**Checked again on 2026-10-12, with the new LlamaCloud key** (in
`docker/.env`, the backend restarted with it). The live code's v1 upload
fails at once, in 4.2 s: "The v1 Parse API is not available for this
account. Use the v2 Parse API instead". So until `fix/llamacloud-v2` is deployed, LlamaCloud
still reads nothing here, and the types above still fail on Cloud. With the
branch's code, in a one-off container of the live image (the branch's `app/`
mounted read-only over `/app/app`, llama-cloud 2.18.0 on `PYTHONPATH`,
`--env-file docker/.env`, the default bridge network, no ports), the same
check read the page:

    LLAMACLOUD: read in 8.5s, 442 characters, 1 page(s), first words found: True
        | ## The Declaration of Independence
        | When in the Course of human events, it becomes necessary for one people to dissolve the political bands which have conne
    LlamaCloud parse done: tier=agentic version=latest pages=1 failed_pages=0 cache_hit=False credits=10.0 attempt=1 job=pjb-...

The same page sent twice took 8.2 s and then 2.0 s; the second came back
`cache_hit=True credits=0`, LlamaCloud handing back the first job.

Run the same check after a top-up, a new key, a deploy of the parser, or
before any switch (it costs a page at each service it asks; LlamaCloud's is
10 credits):

    docker exec -w /app soloclef-backend-1 python scripts/check_cloud_parsers.py
    docker exec -w /app soloclef-backend-1 python scripts/check_cloud_parsers.py llamacloud

**Switching Cloud to LlamaCloud** is `ETL_CLOUD_SERVICE=LLAMACLOUD` in
`docker/.env`, then the gate and `up -d backend celery_worker celery_beat
frontend`. People keep the same page price; what changes for them: Cloud
gains the LlamaCloud-only types above (already offered through the
fallback) and loses `.xltx`; reading is slower (LlamaCloud's agentic parse
took 17 to 42 s a paper on the 8 open-access PDFs of 2026-09-23, with clean
tables and all of a scanned page, where Datalab answers a short file in
seconds; Datalab's balanced mode matched its accurate mode on clinical PDFs
on 2026-09-27); and this server pays at least 10 LlamaCloud credits a page
($12.50 per 1,000 pages at $1.25 per 1,000 credits) against Datalab's $4
per 1,000. Documents already read keep their text. Back to Datalab is the
same line with `DATALAB`.

**LlamaCloud's v2 Parse API (`fix/llamacloud-v2`, 2026-10-12, not
deployed).** `llama-cloud-services` 0.6.94, the LlamaParse client (its last
release, past its support date, v1 only), is replaced by LlamaCloud's own
SDK, `llama-cloud` 2.18.0 (0.1.46 was installed under the old client). The
lock loses llama-index-core, nltk, banks, griffe, aiosqlite, dirtyjson,
nest-asyncio, platformdirs and tinytag, which nothing in the backend
imports. `app/etl_pipeline/parsers/llamacloud.py` uploads to
`POST /api/v2/parse/upload` (tier `agentic`, which was v1's
`parse_page_with_agent`; version `latest`; OCR in English; the same job
timeouts, the base capped at v2's 7,200 s) and polls
`GET /api/v2/parse/{id}?expand=markdown,usage`; the markdown pages become
the page texts, as before. Kept: an empty result or a failed job is sent
again, 5 attempts in all, 10 s doubling to 2 minutes apart; a refusal (401,
402) or a job past its wait fails at once; the wait is at least 2,000 s.
Changed:

- The llama-cloud client retries each request itself on any network error,
  timeout, 408, 409, 429 or 5xx: 5 more tries, about 0.5, 1, 2, 4 and 8 s
  apart. (LlamaParse retried a refused or timed-out connection, 429 and 5xx,
  4, 4, 4 and 8 s apart, and this parser resubmitted the file after a read,
  write or TLS error.) When they fail, an upload fails at once, and a poll
  is tried again until the wait runs out.
- A resubmission asks for a fresh parse (`disable_cache`): LlamaCloud would
  otherwise hand back the same cached job.
- v2 has no cache flag, so a cache hit is the upload answering with an
  earlier job already COMPLETED, logged as `cache_hit=True credits=0`;
  otherwise `credits=` is what the job was billed.
- A request may take the upload timeout (2 to 30 minutes by file size);
  LlamaParse let every request take the whole wait.

The mocked-HTTP tests in `tests/unit/etl_pipeline/test_llamacloud_parser.py`
drive the real SDK against the v2 routes (27 tests). Deploying it: no
migration, no website or phone change. The backend image's dependency layer
builds again, since `uv.lock` changed. From the checkout, once the branch is
merged into `main`:

    for i in backend-src backend-fast; do docker tag soloclef-$i:latest soloclef-$i:pre-llamacloud-v2; done
    docker build -t soloclef-backend-src:latest --target production soloclef_backend
    docker build -t soloclef-backend-fast:latest custom
    python3 soloclef_backend/scripts/deploy_gate.py --wait && \
      (cd docker && docker compose -f docker-compose.yml -f docker-compose.override.yml \
         up -d backend celery_worker celery_beat)
    docker exec -w /app soloclef-backend-1 python scripts/check_cloud_parsers.py llamacloud

Undo: tag the two `pre-llamacloud-v2` images back to `latest`, then the gate
and the same `up -d` (LlamaCloud then fails again with this key).

## This server's own settings: nothing instance-specific in code (2026-10-11)

Anyone can build and run Soloclef Notes from this repository, so no value in
the code names this server (notes.danvics.com), its contact, its SSO, its
Nextcloud or its BTCPay. Each comes from `docker/.env` (or the billing admin
page), and unset means off or empty, never this server's value. The
product's name, logo and colours stay the product's. The self-hosters' guide
is the website's docs page `soloclef_web/content/docs/self-hosting.mdx`
(`/docs/self-hosting`); two unit tests keep it true:
`soloclef_backend/tests/unit/test_no_instance_values.py` (no string value in
`app/` or `alembic/` names this server) and
`soloclef_web/tests/unit/self-hosted-defaults.test.ts` (no shipped web file
does).

| Setting (`docker/.env`) | Default | Read by | This server |
|---|---|---|---|
| `SOLOCLEF_PUBLIC_URL` | `http://localhost:3929` (compose) | backend: email links, OAuth/OIDC callbacks, `app.brand.BRAND_SITE_URL` (docs link in chat, error `report_url`), OpenRouter `OR_SITE_URL`, and alone the `server` every push names (`app/push/server_origin.py`: the https address people type in the phone app; http or unset sends none and warns at startup while `PUSH_ENABLED` is on); frontend build: `NEXT_PUBLIC_SITE_URL` (sitemap, robots, canonical/OG links, JSON-LD, the docs' `/mcp` address) | `https://notes.danvics.com` |
| `NEXT_FRONTEND_URL`, `BACKEND_URL`, `BRAND_SITE_URL`, `NEXT_PUBLIC_SITE_URL` | `SOLOCLEF_PUBLIC_URL` | overrides for a split deployment; `app.brand.site_url_from_env` takes `BRAND_SITE_URL`, then `NEXT_FRONTEND_URL`, then `SOLOCLEF_PUBLIC_URL`; never the push `server` | unset |
| `SUPPORT_EMAIL` | none | backend: the `support_email` billing setting until an admin saves one (`app/support_contact`); frontend build: `NEXT_PUBLIC_SUPPORT_EMAIL` (fallback before `/support-contact` loads, JSON-LD contact point) | `hello@danvics.com` (**new line, see below**) |
| `BTCPAY_URL` | none | the `btcpay_url` billing setting until an admin saves one; crypto stays "pending" without it | `https://pay.danvics.com` (**new line, see below**) |
| `OIDC_ENABLED`, `OIDC_DISCOVERY_URL`, `OIDC_BUTTON_LABEL`, `OIDC_AUTOCREATE_DOMAINS`, `OIDC_ALLOW_SIGNUP` | off, none, "Continue with single sign-on", none, `FALSE` | backend (sign-in), frontend runtime (`OIDC_ENABLED`, `OIDC_BUTTON_LABEL`: the login button), frontend build (`NEXT_PUBLIC_OIDC_*`: the docs' wording through `components/docs/single-sign-on.tsx`) | `TRUE`, `https://sso.danvics.com/application/o/notes/...`, `Continue with Danvics SSO`, `danvics.com`, `FALSE` |
| `NEXT_PUBLIC_SIGNUPS_OPEN` | unset (sign-ups not offered) | frontend build (`lib/signups.ts`: **Sign Up** with exactly `true`, else **Request access**), backend (`sign_up` in `GET /api/v1/sign-in-options`, for the phone app); one line read by both since 2026-10-11 (before, the website build never got it) | unset: closed at Caddy (Accounts, below) |
| `REGISTRATION_ENABLED` | `TRUE` | backend: the kill switch for **every** sign-in (`app.py` `registration_allowed`: register, password login, Google, OIDC, `/auth/desktop/login`), not a sign-up switch | `TRUE` (must stay on for anyone to sign in) |
| `NEXTCLOUD_BASE_URL` | none (Nextcloud not offered) | backend | `https://cloud.danvics.com` |
| `MCP_OAUTH_ISSUER` | none (API keys only) | backend, MCP server | `https://sso.danvics.com/application/o/soloclef-mcp/` |
| `SMTP_FROM` | none | backend mail | `notes@danvics.com` |
| `MAIL_TRANSPORT`, `MAIL_SENDERS`, `SMTP2GO_API_KEY` | none (required while `SMTP_ENABLED`), none (required by `mail_sender`), none | `mail_sender`: how queued mail leaves (`smtp` or `smtp2go_api`), how many send at once, and the send-only smtp2go key for `smtp2go_api` (`custom/docs/mail-queue.md`) | `smtp`, `4`, unset (**new lines, not deployed**) |
| `EMBEDDING_MODEL` | none (required) | backend, worker, migrations: the embedding model, and the default of the two below | `litellm://openai/local-bge-m3` |
| `EMBEDDING_QUERY_MODEL`, `EMBEDDING_DOCUMENT_MODEL` | `EMBEDDING_MODEL` | backend, worker: the model questions are embedded with, and the one stored text is (`app/embedding_roles`; below) | `litellm://openai/local-bge-m3`, `litellm://openai/openrouter-bge-m3` (**new lines, not deployed**) |
| `EMBEDDING_DOCUMENT_CONCURRENCY`, `EMBEDDING_DOCUMENT_BATCH_SIZE`, `EMBEDDING_DOCUMENT_TIMEOUT_SECONDS`, `EMBEDDING_DOCUMENT_BREAKER_SECONDS`, `EMBEDDING_DOCUMENT_FALLBACK` | `4`, `32`, `20`, `300`, `TRUE` | backend, worker: for a document model of its own (OpenRouter here) the requests in flight host-wide, texts a request, seconds a request may take (sent once), seconds every batch goes to the question model after a failure, and whether a failed batch does | unset (the defaults) |
| `EMBEDDING_QUERY_HOST_DOCUMENT_CONCURRENCY`, `EMBEDDING_QUERY_HOST_DOCUMENT_BATCH_SIZE` | `1`, `8` | backend, worker: document batches on the question model's server (the local TEI; one model for both, or a fallback batch) | unset (the defaults) |
| `EMBEDDING_TOKENIZER`, `EMBEDDING_CHUNK_SIZE` | unset (characters), `512` | backend, worker: what counts a chunk's size and how many of those units a chunk holds (`app/config/chunk_sizing.py`; "What sizes a chunk", below). Unset is 512 characters, about 128 bge-m3 tokens; a tokenizer that cannot be loaded stops the start | unset (the defaults; setting them needs a full re-index) |

**What changed on 2026-10-11 and the two lines this server needs.** Three
defaults used to be this server's own values: `brand.json` `siteUrl`
(`https://notes.danvics.com`, also `app.brand.DEFAULT_SITE_URL`), the support
address (`admin@soloclef.com`, in `brand.json` `contactEmail`,
`app.support_contact.DEFAULT_SUPPORT_EMAIL` and `app/billing/defaults.py`)
and `btcpay_url` (`https://pay.danvics.com`). All three are now empty. This
server keeps exactly what it showed before with:

    SUPPORT_EMAIL=admin@soloclef.com
    BTCPAY_URL=https://pay.danvics.com

Later the same night (2026-10-11) the support address became
`SUPPORT_EMAIL=hello@danvics.com` (a mailbox on mail.danvics.com):
soloclef.com has no mail server, so `admin@soloclef.com` bounced. The website
was rebuilt for it (the address is a build arg) and the backend restarted.

in `docker/.env` (an admin-saved `support_email`/`btcpay_url` row still wins;
the lines only matter where none was saved). The site URL needs no new line:
`SOLOCLEF_PUBLIC_URL` is already set, `docker-compose.yml` hands it to the
backend as `NEXT_FRONTEND_URL` and to the frontend build as
`NEXT_PUBLIC_SITE_URL`.

**Build the frontend through compose.** The public pages are prerendered, so
the frontend image now takes the address, the support address, the
`OIDC_*` lines and `NEXT_PUBLIC_SIGNUPS_OPEN` as build args
(`soloclef_web/Dockerfile`), filled by `docker compose ... build frontend`
from `docker/.env`. Next.js writes every `NEXT_PUBLIC_` value into the
pages and scripts at build time, so the running container never reads them.
A plain `docker build soloclef_web` without them builds a site that names no
address (empty sitemap, no canonical links, no contact point, docs without
the SSO paragraph), and `next build` warns `NEXT_PUBLIC_SITE_URL is empty`.
Rebuild the frontend after changing any of these lines.

**One visible difference.** The share image (`public/og-image.png`,
`og-image-twitter.png`) no longer prints `notes.danvics.com` on its bottom
line: every server ships the same image, and the shared link shows the
address anyway. Its URL and dimensions are unchanged.

**Left as they are.** `docker/docker-compose.override.yml` is this host's
shape (`danvics_litellm`, `danvics_mcp`, the loopback port) and stays: Compose
reads it automatically, so the self-hosting page tells other hosts to pass
`-f docker-compose.yml`. `custom/authentik/soloclef-mcp.yaml` is this
server's Authentik blueprint, applied by hand. The Playwright `prod-smoke`
default (`PROD_BASE_URL`) targets this server and is a test tool, not
shipped. Comments and docstrings in `app/auth_oidc` still describe this
server's SSO; they hold no values, and that slice belongs to the phone app's
sign-in work.

### Embeddings: questions local, documents at OpenRouter (`fix/split-embeddings`, 2026-10-12, not deployed)

**Why.** One chat answer took 4 min 26 s: its question's embedding waited
about a minute and a half in the local TEI's queue (`tei-bge-m3`, bge-m3 on
CPU) behind a research import's indexing batches. Every Celery worker
process (up to 10) and the backend sent the local TEI a whole document at a
time (the client's batch of up to 128 texts), and a CPU TEI works through one
batch at a time. Measured on 2026-10-12, uncached, through the live LiteLLM:

| | `local-bge-m3` (TEI, CPU) | `openrouter-bge-m3` |
|---|---|---|
| one question (5 tries) | 97 ms median (89 to 148) | 477 ms median (460 to 730) |
| 8 chunks of 500 characters (the chunker's size) | 0.89 s, 1.00 s | 1.97 s (the run's first request) |
| 16 chunks | 1.56 s | 0.99 s |
| 32 chunks | 3.31 s (8.4 s in an earlier run) | 1.37 s |
| one document's own text, 5,843 tokens | 15.59 s | 0.95 s |

So questions stay local (four times quicker) and documents go to OpenRouter
(the local TEI then sees only questions). Measured with a throwaway script
against the proxy on 127.0.0.1:4000, each text unique so LiteLLM's response
cache could not answer, the TEI's own log confirming it served the local
calls: read-only, public-domain sentences, nothing stored.

**Same vectors, so no re-index.** 20 public-domain sentences in five
languages, embedded by both: cosine similarity of each pair at least
0.999999 (mean 1.000000); 8 questions embedded locally found the same top 1,
the same top 3 and the same top 5 in the same order 8 out of 8 times whether
the sentences were embedded locally (today) or at OpenRouter (the split),
with no score moving by more than 0.0001. Both aliases give 1024 dimensions.
Moving documents between the two hosts never needs a re-index; moving to
another model does (the self-hosting page, "Embeddings: questions and
documents").

**What changed.** `app/embedding_roles` (new): `settings.py` reads the nine
lines (an unreadable value stops the start, naming it; the defaults and why
are commented there), `models.py` builds one client when both roles name one
model (exactly today's single client, no new probe) or two, with the same
`EMBEDDING_BASE_URL` handling, the document client getting the 20 s timeout
and a send-once OpenAI client (`clients.py`), `dimension_agreement.py`
refuses to start when two models' vectors differ in length (one 20 s try per
model; a model unreachable at start does not stop it: the other's length is
used and every later vector is checked), `document_slots.py` holds the
host-wide caps in Redis (keys `embedding:document_slot:<model>:<n>`, one set
per server, 60 s lease renewed every 20 s, 900 s wait then anyway),
`breaker.py` the breaker (`embedding:document_breaker:<model>`, expires by
itself), `redis_link.py` the shared connection (5 s timeout; Redis down means
no cap and a breaker per process, logged and left alone for a minute),
`document_embedding.py` sizes and sends the batches and falls back,
`query_embedding.py` embeds a question with no slot, and `startup_line.py`
logs `[startup] embeddings: ...` from the backend and the worker. Every
search goes through `embed_query` (chat retrieval, both retrievers, the
connector service, notebook search, the startup warmup); everything stored
through `embed_texts`/`embed_text` (`app/utils/document_converters.py`). The
vector columns take `config.EMBEDDING_DIMENSION`; the embedding cache is keyed
on `EMBEDDING_DOCUMENT_MODEL` (off on this server). The LiteLLM alias
`local-bge-m3` keeps its own fallback to `openrouter-bge-m3` after 20 s.

**Review fixes (round 2, 2026-10-12).**

- *A hung OpenRouter.* Before: a batch was tried as one request, then again
  text by text until the first failed, and the OpenAI client under litellm
  tries every request three times (litellm turns `max_retries=0` into 2), so
  against a host that accepts and never answers a batch of 8 cost 6
  connections and 9.1 s at a 1 s timeout (the reviewer's
  `skeptic_hang_check.py`): about 6 minutes at the real 60 s, in a slot
  leased for 5. Now the document client has a 20 s timeout and a client that
  sends once (`SingleAttemptOpenAI`), a batch is one call, the first failure
  opens the breaker for 300 s in every process, and the lease is renewed
  while a request runs. The same check through the new path (loopback hung
  host, loopback question host, throwaway Redis; 1 s standing for 20 s, 30 s
  for 300): the first document cost 1 connection and 1.1 s, then its 32
  texts went to the question host in 4 requests of 8; the next two documents
  and one embedded by a second process cost 0 connections and 0.1 s each;
  after the breaker expired, one connection again; a 128-chunk document with
  the breaker closed sent its 4 batches side by side (4 connections, 1.6 s
  in all). No slot was left held.
- *Startup with a host that hangs* (each of migrations, backend, worker,
  beat): a hung document host costs 2 tries of the timeout (the client's own
  probe while it is built, then the length check): 2 connections, 2.2 s at
  1 s, so 40 s at 20. A hung question host (its client keeps the 60 s and
  its retries): 4 connections, 5.3 s at 1 s, about 3.5 minutes at 60.
- *Fast imports.* A document model of its own gets 4 requests at once and 32
  texts a request; the local TEI (one model, or a fallback batch) keeps 1 and
  8. Through the live LiteLLM to OpenRouter (read-only, public-domain
  sentences, nothing stored, fallback off so the TEI was never asked, two
  rounds each):

  | run | old (1 at a time, 8 texts) | new (4 at a time, 32 texts) |
  |---|---|---|
  | 10 documents of 20 chunks, 4 indexed at a time (210 texts) | 28.5 s, 28.1 s (7.5 texts/s) | 4.8 s, 4.5 s (44 to 47 texts/s) |
  | one document of 200 chunks (201 texts) | 24.4 s, 24.3 s (8.2 texts/s) | 3.0 s, 3.1 s (64 to 67 texts/s) |

- *A model in each process* (`sentence-transformers/...`) takes no slot.
  (Removed on 2026-11-08: there is no in-process model any more, and a model
  name with no provider stops the start. See `feat/drop-local-ml`.)
- *The tokenizer lock* is no longer held while a batch waits for its slot or
  OpenRouter answers, and the file processors cut a summary's text off the
  event loop.

**The lines for `docker/.env`** (next to `EMBEDDING_MODEL`, which stays):

    EMBEDDING_QUERY_MODEL=litellm://openai/local-bge-m3
    EMBEDDING_DOCUMENT_MODEL=litellm://openai/openrouter-bge-m3

The other seven stay unset: at OpenRouter 4 requests at once, 32 texts each,
20 s each; a failed OpenRouter batch (down, out of credit, slower than 20 s)
embedded locally instead with `Document embedding model ... failed for a
batch` in the worker's log, and every batch for the next 5 minutes; on the
local TEI one document batch of 8 at a time.

**Deploy** (no migration):

    cd /home/danvics/docker/surfsense
    git merge --no-ff fix/split-embeddings -m "Merge fix/split-embeddings into main"
    (cd soloclef_backend && nice .venv/bin/pytest -q -m unit -p no:cacheprovider -o addopts="" --import-mode=importlib tests/unit)
    git push origin main
    umask 077; cp docker/.env docker/.env.bak-$(date -u +%Y%m%dT%H%M%SZ)-before-split-embeddings
    docker tag soloclef-backend-fast:latest soloclef-backend-fast:before-split-embeddings
    printf '\n%s\n%s\n' 'EMBEDDING_QUERY_MODEL=litellm://openai/local-bge-m3' \
      'EMBEDDING_DOCUMENT_MODEL=litellm://openai/openrouter-bge-m3' >> docker/.env
    grep -c '^EMBEDDING_\(QUERY\|DOCUMENT\)_MODEL=' docker/.env   # 2
    nice docker build -t soloclef-backend-src:latest --target production soloclef_backend
    nice docker build -t soloclef-backend-fast:latest custom
    python3 soloclef_backend/scripts/deploy_gate.py --wait && \
      (cd docker && docker compose -f docker-compose.yml -f docker-compose.override.yml up -d backend celery_worker celery_beat)

No phone app change: nothing to publish over the air.

**Checks.**

- `docker logs soloclef-backend-1 2>&1 | grep '\[startup\] embeddings'` and
  the same for `soloclef-celery_worker-1` read
  `questions litellm://openai/local-bge-m3, documents litellm://openai/openrouter-bge-m3; 1024 dimensions; document batches at most 4 at once, 32 texts a request, 20 s timeout; on the question model's server at most 1 at once, 8 texts a request; a failed document batch goes to the question model, and so does every batch for 300 s after`,
  with no `not measured at startup` and no `could not reach`.
- As the test account (claude-probe@example.com), ask a chat question that
  searches its documents: `docker logs tei-bge-m3 --since 5m | grep openai_embed | tail`
  shows single small requests with `queue_time` in microseconds or a few
  milliseconds and `total_time` near 0.1 s.
- As the test account, upload a small document (or import two research
  sources) and time it to "Ready"; meanwhile the TEI log shows no batch
  requests (documents went to OpenRouter), and
  `docker logs soloclef-celery_worker-1 --since 10m | grep -E 'failed for a batch|document embedding slot|Redis unavailable'`
  is empty.

**Undo.** The lines only: delete the two lines from `docker/.env` (or copy
the backup back), then
`python3 soloclef_backend/scripts/deploy_gate.py --wait && (cd docker && docker compose -f docker-compose.yml -f docker-compose.override.yml up -d backend celery_worker celery_beat)`;
the start line then reads `questions and documents litellm://openai/local-bge-m3`.
The code too:
`docker tag soloclef-backend-fast:before-split-embeddings soloclef-backend-fast:latest`,
restore the `.env` backup, the same gate and restart, then
`git revert -m 1 <the merge>` and push. Nothing stored changes either way.

**Switching documents back to local later** is one line. In
`/home/danvics/docker/surfsense`:

    umask 077; cp docker/.env docker/.env.bak-$(date -u +%Y%m%dT%H%M%SZ)-before-documents-local
    sed -i '/^EMBEDDING_DOCUMENT_MODEL=/d' docker/.env   # or set it to litellm://openai/local-bge-m3
    python3 soloclef_backend/scripts/deploy_gate.py --wait && \
      (cd docker && docker compose -f docker-compose.yml -f docker-compose.override.yml up -d backend celery_worker celery_beat)

No re-index. The start line then reads `questions and documents
litellm://openai/local-bge-m3; 1024 dimensions; document batches at most 1 at
once, 8 texts a request; no fallback (one model)`: the local TEI's own small
limits keep its queue to one document batch (about a second, up to about
16 s when that batch starts with a long document's own text), so a question
no longer waits behind an import. Moving them out again is the line added
back and the same two commands.

## Security fixes of 2026-10-11 (`fix/security-2026-10-11`, `c0029_zero_config_thread_access`, `c0030_verify_sso_linked_accounts`, not deployed)

Seven findings of a review and seven of its follow-up (the subsections from
"Studio outputs of a private chat" on), each fixed with a test that failed
before. Two migrations: c0029 (the Zero publication only, no table altered)
and c0030 (data: accounts already signed in to through SSO count as
verified; one account here). The phone app needs an update (an over-the-air
one, no new build: `fix/app-zero-security`); until it has it, its live data
falls back to polling and Drive connect from the app asks for an update.
Everything else the installed app keeps doing. Deploying and undoing it all:
"Deploying the release of 2026-10-11", near the top of this file.

### What sizes a chunk (`feat/embedding-tokenizer`, 2026-11-08, not deployed)

**Why.** Every start logged `Failed to initialize tokenizer: 'Could not
automatically map cloudflare-bge-m3 to a tokeniser.'. Using cl100k_base
fallback.`, which reads like chunk sizing was being computed against the
wrong vocabulary. Measured, it was not: nothing in this server asks that
client to count anything. `get_tokenizer()` is never called, and
`RecursiveChunker` was built with a size but no tokenizer, so it used
chonkie's default counter -- characters. `getattr(model, "max_seq_length",
512)` mixed the units: a hosted client has no `max_seq_length`, so every
chunk was 512 **characters**.

**Measured on all 149 documents in this database** (2,091,082 characters),
chunked through `chunk_text_hybrid`, the real indexing path:

- BGE-M3 counts 11.6% more tokens than cl100k_base over the corpus (546,604
  against 489,581); per document the median ratio is 1.113, the quartiles
  1.050 and 1.158, the extremes 0.789 and 2.333. Had the chunker ever used
  the fallback it would have been sizing chunks about 12% small -- real, but
  not the headline.
- The headline is the unit. 512 characters is 102 bge-m3 tokens a chunk on
  average and 164 at the 95th percentile, against a model window of 8192.
  Those settings reproduce **5,424** chunks against the 5,324 actually
  stored, which is how we know this is what production does. At 512 bge-m3
  tokens the same corpus gives **1,424** chunks of 385 tokens: 3.8x fewer,
  3.8x fewer vectors, and retrieval returning paragraphs instead of
  sentences.
- cl100k_base and BGE-M3 at the same chunk size are not interchangeable
  either: 46 of 149 documents chunk identically, 456 of 1,115 boundaries
  (41%) coincide, and a cl100k boundary sits 181 characters from the nearest
  bge-m3 one at the median, 508 at the upper quartile, 1,166 at most.

**What changed.** `app/config/chunk_sizing.py` (new) names both halves
outright: `EMBEDDING_TOKENIZER` counts and `EMBEDDING_CHUNK_SIZE` says how
many of its units a chunk holds. A tokenizer that cannot be loaded stops the
server, naming the setting -- no second vocabulary is used instead. Unset,
chunks are still sized in characters and a start warns, naming the setting:
refusing to start would break every deployment on upgrade and force an
immediate re-index, and chonkie's character counter is an explicit, offline,
deterministic choice rather than a guess at a model's vocabulary.
`app/embedding_roles/tokenizer_notice.py` (new) rewrites the client's
`cl100k_base` warning into one that names `EMBEDDING_TOKENIZER` and says the
client's own counter is unused, so a start still reports which setting
decides.

**Enabling it needs the tokenizer in the image.** `BAAI/bge-m3`'s tokenizer
is a 17 MB HuggingFace download that is not cached in the backend image, and
`cl100k_base` is not either -- today's "fallback" fetches from
`openaipublic.blob.core.windows.net` at every cold start. Chonkie loads a
tokenizer from a directory holding `tokenizer.json` with no network, so bake
the directory into the image (17 MB against 8.5 GB) rather than fetching it
at start; a hub name works but puts the hub on the start path.

**Re-indexing.** Nothing changes until `EMBEDDING_TOKENIZER` is set:
production stays at 512 characters, and the 5,324 stored chunks stay valid.
Setting it re-sizes every chunk, so newly indexed documents would stop
matching the stored ones -- turn it on together with a full re-index, not
before. The re-index itself is cheap: 1,424 chunks at 32 a request and 4
requests at once is about 11 rounds of roughly 1.5 s, under a minute of
embedding for the whole corpus. What costs is the source side -- 59 of the
149 documents are `CRAWLED_URL` and would be re-fetched.

### Does a bigger chunk retrieve better? Measured (2026-11-08)

The section above establishes that chunks are 512 **characters**, and that
512 bge-m3 tokens would give 1,424 chunks instead of 5,424. What it did not
show is that the bigger chunks *retrieve better* — only that there are fewer
of them. Measured now, and the answer is conditional, so the setting stays
off until the condition is met.

**Setup.** All 149 documents, read-only out of the live database into a
throwaway `pgvector/pgvector:pg17`. Three corpora, one row per chunk, each
chunk carrying the character span it was cut from:

| corpus | chunks | mean chars | median | p95 | vectors |
|---|---|---|---|---|---|
| `character/512` (today) | **5,324** | 384 | 406 | 507 | the live ones, copied |
| `bge-m3/512` | 1,424 | 1,467 | 1,626 | 2,191 | recomputed |
| `bge-m3/1024` | 834 | 2,506 | 2,894 | 4,314 | recomputed |

The baseline uses the **vectors production actually searches**. The other two
were embedded with `BAAI/bge-m3` locally, which reproduces the hosted model:
on three stored chunks, local-against-stored cosine is 0.9997–0.9998 (and 0.53
against a different chunk), so the two are interchangeable. Re-chunking
reproduces 5,424 / 1,424 / 834 exactly, and the live rows' own statistics
(384 mean, 406 median, 507 p95) confirm `character/512` is what runs.

**How "better" is judged.** 28 questions written against passages that are
really in this corpus — NY DMV rules, anatomy shelf notes, term-life rates,
AI-ethics papers, a Bronx restaurant menu, PICU orientation, p-value
misconceptions. Each has a gold **character span** in one document, so the
ground truth is the same text for every chunking and a chunk counts only if
its own span overlaps it. Two measures:

- **hit** — the retrieved text contains the sentence that answers the question.
- **passage** — the share of the surrounding ±900-character passage that comes
  with it, trimmed to whole sentences. This is the "paragraphs not sentences"
  claim, made measurable.

Compared at **equal context cost**, not equal chunk count: chunks are taken in
fused (RRF, the retriever's own two legs) rank order until a character budget
is full. Equal `k` is not a fair comparison — 10 chunks of 2,506 characters is
eight times the prompt of 10 of 384.

**hit** (answers found, of 28):

| context budget | `character/512` | `bge-m3/512` | `bge-m3/1024` |
|---|---|---|---|
| 2,000 | **0.714** (20) | 0.536 (15) | 0.000 (0) |
| 4,000 | **0.857** (24) | 0.750 (21) | 0.393 (11) |
| 8,000 | **0.893** (25) | 0.821 (23) | 0.750 (21) |
| 16,000 | **0.893** (25) | 0.857 (24) | 0.857 (24) |
| 26,000 | 0.893 (25) | 0.893 (25) | **0.964** (27) |
| 40,000 | 0.929 (26) | **1.000** (28) | 0.964 (27) |

**passage** (how much of the surrounding passage comes with it):

| context budget | `character/512` | `bge-m3/512` | `bge-m3/1024` |
|---|---|---|---|
| 4,000 | 0.458 | **0.624** | 0.354 |
| 8,000 | 0.587 | **0.694** | 0.666 |
| 16,000 | 0.642 | 0.743 | **0.789** |
| 26,000 | 0.697 | 0.852 | **0.900** |
| 40,000 | 0.761 | **0.961** | 0.916 |

**What that says.** There is a crossover, not a winner. Below roughly 16,000
characters of context today's 512 characters finds the answer *more* often —
a budget buys 20 independent places to look instead of 7. At and above about
26,000 it is overtaken, and `bge-m3/512` is the only corpus that reaches 28 of
28. Passage completeness favours the bigger chunks almost everywhere: at
26,000 characters, 0.697 against 0.852. So the claim that retrieval "returns
sentences where it could return paragraphs" is **confirmed**; the claim that
bigger chunks retrieve *better* is true only above about 26,000 characters of
context and false below it.

`bge-m3/1024` returns **nothing at all** under a 2,000-character budget — one
chunk averages 2,506 characters, so not one fits. That is disqualifying for a
squeezed prompt, and 1024 is no better than 512 where it matters. If this
moves, it moves to **512 bge-m3 tokens**.

**Why it is still off: the per-document cap is a count.**
`_MAX_FETCH_CHUNKS_PER_DOC = 20` in `app/retriever/chunks_hybrid_search.py`, and
`top_k` defaults to 5 documents, so what a search hands the prompt today is
measured at **26,343 characters**. Replaying that exact shape (5 documents,
≤20 chunks each, matched ones kept first):

| corpus | hit | passage | prompt |
|---|---|---|---|
| `character/512` | 0.893 | 0.615 | 26,343 |
| `bge-m3/512` | 0.964 | 0.944 | **74,036** |
| `bge-m3/1024` | 0.964 | 0.938 | **109,245** |

Better, and at 2.8–4.1x the prompt — roughly 20,000 extra tokens of
knowledge-base context per search. Lower the cap to match the prompt instead
(`per_doc` 4 for `bge-m3/512`, 2 for `bge-m3/1024`, ~26,500 characters either
way) and it collapses: hit 0.536 and 0.464 against 0.893. A count-based cap
cannot express "the same amount of text", so with it in place the switch
either triples the prompt or loses half the answers.

**Recommendation.** Keep `EMBEDDING_TOKENIZER` unset for now. The prerequisite
is to make the per-document cap a **character budget** rather than a chunk
count — which is worth doing on its own merits (at 26,000 characters in rank
order today's corpus scores passage 0.697 against the grouped shape's 0.615)
and costs no re-index. With that in place, set
`EMBEDDING_TOKENIZER=/opt/chunk-tokenizer` and `EMBEDDING_CHUNK_SIZE=512`,
bump `EMBEDDING_CACHE_CHUNKER_VERSION`, and re-index. Not before.

**Caveats, so the numbers are not over-read.** 28 queries means one query is
0.036, so differences under about four queries (0.14) are weak — the
`character/512`-vs-`bge-m3/512` gap at 8,000 characters is two queries, and
paired, each finds two the other misses. One corpus, one annotator, gold spans
written by the same person who chose the queries. The reranker
(`voyage-rerank-2.5-lite`) is not in this measurement: it reorders documents
after fusion, and seeing more complete passages can only help it, so the
numbers above are a floor for the bigger chunkings, not a ceiling. Two
documents' spans (1 of 1,424 and 1 of 834) could not be located exactly
because the table-aware chunker strips a table block; everything else is
exact.

**Also found.** `_cap_chunks_per_document` keeps matched chunks ahead of
context, but returns them in *reading* order, so when one document contributes
more matched chunks than the cap allows it keeps the positionally-first rather
than the best-ranked — and can drop the one that answered. It needs a document
to own more than 20 of the fused window's 25 chunks, which bigger chunks make
rarer and smaller chunks make likelier. Worth a look when the cap is reworked.

**The tokenizer is baked.** `custom/Dockerfile` downloads `BAAI/bge-m3`'s
tokenizer (17 MB: `tokenizer.json` and its two companions) to
`/opt/chunk-tokenizer` at build time, behind `ARG CHUNK_TOKENIZER`. Verified
offline, `--network none`: chonkie loads it from the directory and the startup
line reads `chunks of at most 512 /opt/chunk-tokenizer tokens`. A build that
wants no tokenizer passes an empty `CHUNK_TOKENIZER`; a named one that cannot
be fetched fails the build rather than shipping an image whose server will not
start. Nothing uses it until `EMBEDDING_TOKENIZER` is set.

**The thread-safety hazard was checked, and does not reproduce.**
`app/embedding_roles/document_embedding.py` records that HuggingFace fast
tokenizers are not thread-safe ("Already borrowed"), and
`index_batch_parallel` runs up to 4 documents at once, each calling
`asyncio.to_thread(chunk_text_hybrid, ...)` against the single shared
`config.chunker_instance`. Probed directly — 8 threads, 2,000 mixed
`chunk` / `encode` / `count_tokens` calls through one `RecursiveChunker` built
on the baked bge-m3 tokenizer — nothing raised. Chonkie reaches the tokenizer
through `transformers`' Python wrapper and does not mutate truncation or
padding state per call, so the lock that guards an in-process *embedding*
model is not needed for the chunker. Re-probe if chonkie's tokenizer backend
changes.

**Re-indexing, when it happens.** Cheaper than previously assumed: **all 149
documents hold their own `source_markdown`, the 59 `CRAWLED_URL` ones
included**, so `reindex_document_task` re-chunks and re-embeds from Postgres
and **nothing is re-fetched or re-crawled**. `knowledge_store_enabled` is
false on all five workspaces, so no document is deferred to the store's
indexer.

1. Bump `EMBEDDING_CACHE_CHUNKER_VERSION` in the **same** step as
   `EMBEDDING_TOKENIZER` / `EMBEDDING_CHUNK_SIZE`. The cache key
   (`app/indexing_pipeline/cache/schemas/embedding_key.py`) reads the markdown
   hash, the model and that version — not the chunk sizing — so without the
   bump the cache answers with chunks cut to the old size. Inert on this server
   today (`EMBEDDING_CACHE_ENABLED` is unset and `embedding_cache_sets` is
   empty), so the bump invalidates nothing; do it anyway, or the next operator
   who turns the cache on inherits a silent mismatch.
2. Deploy behind `scripts/deploy_gate.py --wait` as every deploy here is.
   Between the restart and the re-index the stored 512-character chunks and
   newly written 512-token ones coexist; both are searchable and citable, so
   retrieval degrades rather than breaks. Keep the window short.
3. Enqueue `reindex_document_task` per document, oldest first. The chunk
   reconciler (`app/indexing_pipeline/chunk_reconciler.py`) matches on exact
   chunk text, so a sizing change reuses nothing: every chunk is deleted and
   re-embedded, which is what you want — no mixed sizes survive.
4. Cost: chunking the whole corpus takes 2.7 s with the bge-m3 tokenizer
   against 0.11 s with the character counter (25x, still ~20 ms a document,
   nothing next to embedding). Embedding 1,424 chunks at
   `EMBEDDING_QUERY_HOST_DOCUMENT_BATCH_SIZE=32` and concurrency 4 is 11 rounds
   — under a minute for the corpus. The HNSW index re-points 5,324 deletes and
   1,424 inserts; at the ~175 rows/second `c0031` measured for HNSW-indexed row
   churn, call it a minute.
5. Afterwards, confirm the shape changed:
   `SELECT count(*), round(avg(length(content))) FROM chunks;` should read
   about 1,424 and 1,467, not 5,324 and 384.

### The per-document cap is a character budget (`feat/chunk-budget`, measured 2026-11-08, not deployed)

The section above left the prerequisite: `_MAX_FETCH_CHUNKS_PER_DOC = 20` is a
chunk **count**, and a count cannot say "the same amount of text". It is now a
character budget, which is worth it on its own terms — at the same prompt size
retrieval finds one more answer and carries noticeably more of the passage
around it — and it costs no re-index.

**What the settings are.** Three, each explicit, in `app/config/__init__.py`:

| setting | default | what it bounds |
|---|---|---|
| `RETRIEVAL_CONTEXT_BUDGET_CHARS` | 26000 | all the retrieved text one search hands the prompt |
| `RETRIEVAL_DOCUMENT_CONTEXT_BUDGET_CHARS` | 16000 | the most of that any single document may take |
| `RETRIEVAL_FUSION_CANDIDATE_CHUNKS` | 200 | chunks rank fusion scores before the budget is spent |

`app/retriever/context_budget.py` holds the arithmetic and
`app/retriever/fusion_window.py` the window; both are read by every search
that spends the budget.

**Characters, not tokens.** Tokens are what the model is billed for and would
be the honest unit, but counting them on the search path means loading a
tokenizer into every search — the same 17 MB tokenizer the chunker only now
has. Characters are what the measurement above is written in, what a chunk
row can be measured by in SQL, and within one corpus they are proportional
enough to tokens to compare two prompt shapes. The unit is in the setting
name, so a later move to tokens is a new setting, not a silent change of
meaning.

**Both per search and per document, and the second is not redundant.** The
budget is spent in fused-rank order, so without a document ceiling the best
few chunks — which are often all in one document — can be the whole prompt.
Measured, the largest single document already takes a mean 0.64 of the prompt
and as much as 0.92 of it. A 16,000-character document ceiling scores the same
as no ceiling at all (passage 0.734 against 0.740, hit identical) while
bounding that case, so the ceiling is kept and set where it does not bind in
ordinary use. A document the budget leaves with no text is dropped from the
results rather than returned as a source with nothing to cite.

**One search's shape, same corpus and questions as above** (all 149 documents
read-only into a throwaway `pgvector/pgvector:pg17`, the 28 questions and
their gold character spans, `top_k` 5 documents). The budget rows are measured
through the committed `admit_within_budget`, not a replay of it:

| shape | hit | passage | prompt | documents |
|---|---|---|---|---|
| today, chunks retriever: 20 chunks per document | 0.893 (25) | 0.615 | 26,181 | 3.8 |
| today, chat retrieval: 12 passages per document | 0.893 (25) | 0.565 | 7,672 | 3.8 |
| budget 8,000 | 0.893 (25) | 0.587 | 7,961 | 3.6 |
| budget 16,000 | 0.893 (25) | 0.676 | 15,968 | 4.5 |
| **budget 26,000 (the default)** | **0.929 (26)** | **0.734** | 25,552 | 4.9 |
| budget 40,000 | 0.929 (26) | 0.753 | 33,194 | 4.9 |

So at the prompt size the chunks retriever already spent, the budget finds 26
of 28 answers instead of 25 and carries 0.734 of the surrounding passage
instead of 0.615.

**The 0.697 the section above predicted was reproduced, and then beaten.**
Both of its numbers reproduce exactly on the rebuilt corpus — rank order
under a 26,000-character budget scores passage 0.697, the grouped shape 0.615.
The implementation scores **0.734**, above the replay, and the difference is
the document contract: the replay spent its budget across a mean 9.7
documents, while a search returns `top_k` of them, so the same budget buys
more of the few documents that matter. Keeping the contract is what made it
better rather than worse; a straight port of the replay would have had to
abandon `top_k`.

**The budget needs a window to spend.** A character budget can only spend what
fusion ranked, and the window was `top_k * 5` — 25 chunks, about 9,600
characters of 384-character chunks, so a 26,000-character budget would never
have bound. The passages *around* an answer are its neighbouring chunks, which
fuse well but sit below a 25-deep window. Over the pool at budget 26,000:
passage 0.666 at 50 candidates, 0.728 at 100, 0.740 at 150, 0.740 at 200,
0.740 at 300 — it settles by 150, and 200 is the default.

That window costs time: the fused query's p50 goes from 7.9 ms at 25
candidates to 43.7 ms at 200 on this corpus. About 16 ms of that was the
`vector(1024)` column coming back with every row, which nothing downstream
reads, so both fused queries now `defer(Chunk.embedding)` — 200 rows is 2.4 MB
of vectors not transferred. The rest is the deeper HNSW and GIN scan, and is
the price of the measurement above.

**What it changes in the prompt.** The chunks retriever no longer sends
unmatched filler: it used to fetch each document's first 20 chunks in reading
order whether or not they matched, and that filler was most of its 26,000
characters. Now every chunk in the prompt is one fusion ranked, which is also
every chunk that can be cited. Chunks are still presented in reading order —
only the selection is by rank, which is the point of the fix below.

Chat retrieval spends the same budget, so its retrieved context grows from
about 7,700 characters to about 25,500 — roughly 1.9k to 6.4k tokens a search.
That is the measured gain's price, and `RETRIEVAL_CONTEXT_BUDGET_CHARS=16000`
is the middle setting (passage 0.676 at 16,000 characters) if the token cost
matters more than the last 0.06 of passage.

**The ordering defect is fixed.** `_cap_chunks_per_document` kept matched
chunks ahead of context but walked them in *reading* order, so a document
contributing more matched chunks than the cap kept the positionally-first ones.
Reproduced directly against that function on `origin/main`: 21 matched chunks
in one document, cap 20, and the chunk at position 21 is dropped whatever its
rank — if it was the chunk that answered the query, the answer is gone. The
function is gone with the count; selection is now by fused rank and
presentation by position, and
`tests/unit/retriever/test_chunk_search_context_budget.py` drives
`hybrid_search` over that exact shape and asserts the best-ranked chunk
survives.

**Every path that had its own copy.**

- `app/retriever/chunks_hybrid_search.py` — the count cap, the reading-order
  cap and the head-of-document filler fetch, all replaced by the budget. One
  query fewer per search: the fused rows already carry the content.
- `app/agents/chat/multi_agent_chat/shared/retrieval/hybrid_search.py` — the
  live chat path, which had `_MAX_PASSAGES_PER_DOC = 12` and its own
  `top_k * 5` window. Its selection was already by score, so it had no
  ordering defect, only the count.
- `app/retriever/documents_hybrid_search.py` — same `_MAX_FETCH_CHUNKS_PER_DOC
  = 20`. Its documents match as wholes, so there are no chunk ranks to order
  by; the document allowance is applied along reading order *in SQL*, as a
  running `sum(length(content))` window, so a long document is still not
  loaded to be discarded.
- `app/notebooks/search/service.py` and its `grouping.py` were checked and
  left alone: `MAX_PASSAGES`, `MAX_PER_NOTEBOOK` and `_RERANK_TEXT_CHARS` size
  a search box's list of 400-character snippets, not a prompt.

**Deploying it.** Code only — no migration, no re-index, no new image
dependency, and the stored chunks are untouched. Build and deploy behind
`scripts/deploy_gate.py --wait` as every deploy here is. Leave the three
settings unset to get the measured defaults; they are read per search, so
changing one is a restart, not a re-index. Worth watching after the restart:
`[chunk_search] hybrid_search RRF query in ...` for the deeper window's cost,
and prompt sizes on chat turns, which grow as described above.

**And the chunk-size question, which this unblocks.** With a character budget
in place, `EMBEDDING_TOKENIZER=/opt/chunk-tokenizer` with
`EMBEDDING_CHUNK_SIZE=512` can be measured at a *fixed prompt* rather than at
a fixed chunk count — the comparison the section above could not make. Still
not before a re-index, and still its own measured slice.

### `hnsw.ef_search` stays at its default (measured 2026-11-08, no change)

`app/retriever/vector_scan_tuning.py` sets `hnsw.iterative_scan` and
`hnsw.max_scan_tuples` per search transaction and deliberately leaves
`ef_search` alone. Measured on **this database's own 5,324 chunks and their
real BGE-M3 vectors** (copied read-only into a throwaway
`pgvector/pgvector:pg17`, 28 hand-written questions about documents actually
in the corpus), that is the right call, and the reason is the corpus size, not
the parameter.

Method: ground truth is the exact top 10 from a sequential scan with
`enable_indexscan`/`enable_bitmapscan` off; the HNSW side is swept with
`enable_seqscan` off, and **every point's plan is read from `EXPLAIN` before
it is timed** — a sweep whose plan moves underneath it measures the planner,
not the parameter. `recall@10` is the overlap with that exact answer.

| workspace | chunks | share of corpus | plan the planner picks |
|---|---|---|---|
| 1 | 5,004 | 94.0% | `Index Scan using chucks_vector_index` + `Filter: workspace_id = 1` |
| 9 | 250 | 4.7% | `Index Scan using ix_chunks_workspace_id` + `Sort` — **exact** |
| 6 | 70 | 1.3% | `Index Scan using ix_chunks_workspace_id` + `Sort` — **exact** |

So `ef_search` is only in the plan for workspace 1 at all, which is exactly
what `c0031`'s docstring predicted for a workspace small next to the corpus.
And for workspace 1, over `ef_search` 20, 40, 60, 80, 100, 160, 200, under
both `iterative_scan = off` and `relaxed_order`:

- `recall@10` is **1.000 at every value, including 20** — a 5,000-node HNSW
  graph is small enough that even a 20-candidate beam reaches the true nearest
  neighbours. There is no recall to buy.
- Latency is all that moves: p50 0.40 ms at 20, 0.73 ms at 40 (the default),
  1.16 ms at 100, 2.41 ms at 200. Raising it is a pure cost here.
- `rows_returned` is 10 of 10 at every point: the silent short-return `c0031`
  exists to prevent does not occur at this size.
- At `ef_search` 400 and 800 the planner abandoned the HNSW index even with
  `enable_seqscan = off` (25 ms, a different plan). Those two points measure a
  plan change, not `ef_search`, and are reported as such rather than averaged
  in — which is how the earlier sweep's incoherent 0.621 / 1.000 / 0.168 arose.

**Conclusion: leave `ef_search` unset.** pgvector's default is 40, four times
the usual `LIMIT 10`, and this corpus cannot distinguish it from 200. The
honest statement is that the parameter needs more data than this server has:
HNSW recall only falls once the graph is large enough for a fixed beam to miss,
and nothing here is. Revisit when workspace 1 passes roughly a hundred thousand
chunks, and measure the same way — pinned plan, real vectors, recall against a
sequential scan — rather than on a synthetic corpus, where clustered vectors
leave many near-equidistant neighbours and absolute recall means little.

### Zero no longer sends connector config

**Why.** zero-cache sends every published column of a row to whoever syncs
it. `search_source_connectors` was published whole and the connectors panel
synced the workspace's own connectors (`connectors.bySpace`, MCP servers:
account connections have no `workspace_id` and never matched), so every
member of a workspace received each MCP server's `config` unmasked: its URL,
headers and tokens. A read-only count on the live database (2026-10-11)
found no workspace with more than one member, so no co-member can have
received another's keys today; the rows cannot say whether a workspace had
members earlier, so rotate an MCP key only if one did.

**Now.** `ZERO_PUBLICATION["search_source_connectors"]` is a column list
without `config` (and without `enable_vision_llm`, which no client reads);
the website's Zero schema no longer names `config`; the panel takes a live
row's config from the REST list's copy (masked, `mask_connector_config`;
saving the masked form keeps the stored secrets, `restore_masked`), or `{}`
until the REST list has the row. `tests/unit/test_zero_publication_private_columns.py`
fails if the web's Zero schema names a column the publication does not send.

**Installed apps.** zero-cache checks a client's whole schema when it
connects (`checkClientSchema`, Zero 1.6.0): a table or column the client
names that is not replicated refuses the connection, all of it, with
`SchemaVersionNotSupported`. The installed phone app (`feat/sn-ship`
6a203c5ca) carries a copy of the web's schema that names
`search_source_connectors.config` (it never queries connectors), so after
this migration Zero refuses it: the Zero client disables its local client
group and calls `onUpdateNeeded`, the app's session falls back to polling for
that run ("zero-cache needs a newer app", `lib/live/zero/session.ts`), and
the same happens at each start until the app has a schema copy without
`config` (`node scripts/sync-zero-from-web.mjs`, then the update). Screens
still fill (polling); only live push pauses. Keeping the old app on Zero
would need a non-secret column named `config` on the same table, that is
renaming the real column under every reader and writer of connectors
(Postgres publishes tables, not views; a client's column cannot be mapped to
another server column), with running workers writing tokens to the wrong
column during the deploy. Not done. Columns added are harmless to old
clients (zero-cache sends them, clients ignore fields they do not name, as
they already do `documents.updated_at`). Open website tabs get the same
refusal and reload themselves (Zero's default), onto the new site.

**Order.** The migration and backend first, then the frontend, then the
app's update built from the re-synced copy (`fix/app-zero-security`). The
app's update must not go out before the migration: its copy names the new
`new_chat_threads` columns, which an older server does not publish, and
would be refused the same way.

### Private chats stay private in Zero

**Why.** `messages.byThread`, `comments.byThread` and `chatSession.byThread`
checked only the thread's workspace, so any member could sync a co-member's
private thread, messages included, by its id.

**Now.** `new_chat_threads` also publishes `visibility` and `created_by_id`,
and each of those queries keeps to the threads the REST routes allow
(`check_thread_access`, `list_threads`): the user's own, any shared with the
workspace (`SEARCH_SPACE`), and in a workspace they own a thread whose
creator is gone (`created_by_id` NULL; `/zero/context` now also returns
`ownedSpaceIds`). The rule (`soloclef_web/zero/queries/thread-access.ts`)
runs in the website's query route, which builds every client's queries, so
it covers the installed phone app with no update; rows of another member's
private thread already on a device are removed when zero-cache next runs the
query (`ZERO_AUTH_RETRANSFORM_INTERVAL_SECONDS`, 60 s). Test:
`soloclef_web/tests/unit/chat/zero-thread-access.test.ts` (the queries as
zero-cache receives them, evaluated against private, shared and creatorless
threads).

### Indexing settings only where the connector is usable

**Why.** `PUT /search-source-connectors/{id}?workspace_id=…` with
`is_indexable` or the periodic fields made an enablement row for any
workspace id with no membership or permission check, and the schedule
checker then indexed the owner's files into that workspace.

**Now.** The route needs the connector usable there
(`is_enabled_in_workspace`: its owner a member, not switched off; else 404,
as the index route answers) and `connectors:update` there (else 403), before
it reads or makes the row. **Automatic:** the schedule checker (every
minute) never dispatches an enablement whose connector is not usable in its
workspace; it switches that row's periodic indexing off
(`periodic_indexing_enabled = false`, `next_scheduled_at = NULL`) with a
warning in the worker's log. That also covers an owner who has left a
workspace. Tests: `tests/unit/routes/test_connector_enablement_update_authz.py`,
`tests/unit/tasks/celery_tasks/test_schedule_checker_task.py`.

### A note's title and preview read only its opening

**Why.** Saving a note named it from `first_line_of_words(source_markdown)`,
which ran the maths pattern over the whole text on the event loop. With
opening delimiters and no closing ones its time grows with the square of the
length: 60,000 characters of `\[` took 22 s here, a megabyte would hold the
worker for hours; the Notes list's previews did the same.

**Limit.** `math_as_text` reads at most `MATH_TEXT_MAX_CHARS` = **2,000
characters** (`app/workspace_notes/readable_text.py`); every caller shows
only the opening words (a title of at most 100, a 240-character preview), so
the worst case is about 10 ms. A note whose first words come after 2,000
characters of blank or markup-only lines is titled "Untitled". The website's
`mathAsText` has the same cap (a 400,000-character note held a tab 18 s when
its preview was drawn), and so does the phone app's copy
(`soloclef_mobile/src/lib/render/mathAsText.ts`) on `fix/app-zero-security`.
Before the cut, every link and image of the whole text becomes its words
(`links_as_words`, the web's and app's `linksAsWords`; follow-up): a note
opening with an image longer than the cap (a pasted `data:` image) was cut
inside it and titled with its markup; now an image without a description
is nothing and the title comes from the next line. A label stops at the
next `[` and an address at the next `]`, so this too takes time in
proportion to the text. Tests: `tests/unit/workspace_notes/test_readable_text.py`,
`soloclef_web/tests/unit/notes/note-title-math.test.ts` (crafted bodies
under a time bound).

### Profile edits cannot change the email or password; SSO links only to verified accounts

**Why.** `PATCH /users/me` took fastapi-users' `BaseUserUpdate`, so a
session alone changed the account's email (no check of the new address) or
password (no current password). Single sign-on and agent sign-in link a
first-time identity to the account with the same email, so someone could
switch an account to another person's address, or sign up with it, and that
person's first SSO sign-in would land in an account whose password they did
not choose.

**Now.** `PATCH /users/me` takes `display_name` and `avatar_url` only;
anything else is 422 (`app/schemas/users.py`). A password changes through
"Forgot password?" (a mailed token); an email only by the operator (there is
no verified email-change flow). Neither the website nor the phone app sends
either field there. SSO links by email only to an account whose own address
is verified (`user.is_verified`: made by `scripts/create_account.py`, by an
earlier SSO sign-in, by c0030 for an account SSO had already signed in to,
or verified since); otherwise it refuses with
`OIDC_ACCOUNT_NOT_VERIFIED` (the login page: "Account not confirmed"), and
agent sign-in with its usual 403. **Users notice:** an account made with the
password sign-up form is unverified (no verification mail is sent here);
its owner keeps signing in with the password, and SSO into it needs the
operator to set `is_verified` after checking the address is theirs. The
phone app shows its generic sign-in message for the new code until its
`features/auth/reasons.ts` names it. Google sign-in (`AUTH_TYPE=GOOGLE`, not
this server) keeps fastapi-users' own association; that mode has no sign-up
form and now no way to change an email. Tests:
`tests/unit/routes/test_users_me_profile_only.py`,
`tests/unit/auth_oidc/test_account_decision.py`,
`tests/unit/agent_sign_in/test_account_choice.py`,
`soloclef_web/tests/unit/sso-login/sso-login.test.ts`.

### Google Drive connect is bound to the browser that started it

**Why.** The OAuth state was signed but readable, the PKCE verifier inside
it, and accepted from any browser: a consent link someone started in their
own account and got another person to open saved that person's Drive into
the starter's account, whose Picker then browses it.

**Now** (`app/connectors/google_drive/connect/connect_attempt.py`, as the
Microsoft connect): the attempt (state, PKCE verifier, who, where to return)
is in an HttpOnly cookie `notes_drive_connect` (path: the callback's,
SameSite=Lax, Secure on https), signed with `SECRET_KEY`, **10 minutes**
(`LIFETIME_SECONDS`) from Connect to Google's answer. The callback saves
nothing without it and lands on `/dashboard/{id}/connectors/callback?error=google_drive_connect_expired`
(the website says "Couldn't connect"; connect again). A connect in flight
during the deploy fails once that way, and so does an earlier tab's when
Connect is pressed again in the same browser (one attempt per browser, the
last one). From the phone app (`app/app_handoff`),
`drive_connect` now needs the app's `code_challenge`, its callback holds the
grant encrypted (`held_grant.py`) under a 5-minute completion, and the app
saves it with `POST /api/v1/app/handoff/complete`, as for Microsoft mail
(below). **The installed app** sends no challenge, so Drive connect from it
says "Update the app to connect Google Drive from it, or connect it on the
website" (400 `HANDOFF_CHALLENGE_REQUIRED`); its older fallback to the
website's flow would be refused at the callback (the cookie lands in the
app, not its browser sheet). Drives already connected keep working;
nothing to reconnect. The app's update of the same release (JavaScript
only, `release/app-2026-10-11` `3ac6be2e8`) sends an S256 `code_challenge`
for `drive_connect` and `microsoft_mail` (`features/auth/pkce.ts`) and, on
`status=confirm`, posts the `completion` with the verifier and its token
(`features/workspace/connectors/handoff.ts`), so the message is right for
an app without it.
Tests: `tests/unit/connectors/google_drive/test_connect_attempt.py`,
`tests/unit/app_handoff/test_routes.py` (another browser, a forged state, a
replay, the app's confirmation, another account, a wrong verifier),
`tests/integration/connectors/google_drive/test_connect_flow.py` and
`test_app_handoff.py`.

### The crawler stays on public addresses after the first URL

**Why.** `crawl_url` checked only the URL it was given. The browser tiers
(a Chromium on this server, inside its network) followed redirects and
script navigations anywhere, and a static fetch whose redirect curl refused
as internal was handed to the browser tiers, which followed it.

**Now** (`app/proprietary/web_crawler/destination_guard.py`): curl's refusal
ends the crawl (failed, "Restricted URL"), and where curl's redirects ended
is checked; the browser tiers abort every request the page makes to a host
that is not public (navigations in any frame, and subresources, the page's
and its service workers', which reach only the context's route), refuse a
page whose final URL or any main-frame navigation is not public, and such a
refusal ends the ladder. Each host is resolved once per fetch for these
checks. Test: `tests/unit/proprietary/web_crawler/test_destination_guard.py`.

**DNS rebinding, redirect hops, WebSockets** (follow-up,
`app/proprietary/web_crawler/pinned_egress.py`). The checks resolved a host
and the fetchers resolved it again: a name answering a public address to the
check and a private one to the fetch read internal pages through any tier,
and Chromium's HTTP redirect hops and WebSockets never met `page.route`.
Without an operator proxy (`PROXY_URL` and the provider settings,
`app/utils/proxy`), every tier now connects through a SOCKS5 proxy on
127.0.0.1 opened for that fetch: it resolves each host once, refuses unless
every address is public, and connects to exactly those addresses (Chromium
gets `socks5://`, which sends host names, and Playwright proxies loopback
too; curl gets `socks5h://`). A fetch that failed after a refusal ends the
crawl as restricted. Through it the static tier follows every redirect:
curl's "safe" mode judges the address curl is connected to, the proxy's
127.0.0.1, and refused every redirect; the proxy judges each hop instead.
The DynamicFetcher tier also closes a WebSocket to a non-public host
(`page.route_web_socket`); the stealth tier does not (Playwright does it by
replacing the page's `WebSocket`, which a bot check can see) and relies on
the proxy. **Limits:** CONNECT only (no UDP: QUIC is off behind a SOCKS
proxy; WebRTC can still send UDP, which reads no page), 10 s per connection
attempt (`CONNECT_TIMEOUT_SECONDS`), 256 connections at once per fetch
(`MAX_CONNECTIONS_PER_FETCH`), 10 s for the client's handshake; the proxy
closes with its fetch; one loop thread per process (a forked worker starts
its own). **With an operator proxy** the fetch goes through that proxy as
before, and that proxy resolves and connects from wherever it runs: this
server's `PROXY_URL` is a host elsewhere (a public address on :8888, not
this machine), so its connections cannot reach this server's internal
network; if it is a machine of the operator's, that machine's own local
services are within its reach and it should refuse private destinations
itself. Not covered here: the research tools' own fetchers
(`app/notebooks/research/page_read/browser.py`, `ingest/remote_file.py`)
still resolve, check, then fetch. Tests:
`tests/unit/proprietary/web_crawler/test_pinned_egress.py` (real sockets: a
rebinding name, a mixed answer, an address literal, curl's hops, the tiers'
settings) and, in a real Chromium whose own resolver is pointed at the
private address, `tests/integration/proprietary/web_crawler/test_pinned_egress_browser.py`
(a rebinding name, a redirect hop and a WebSocket never reach it; all three
did without the proxy; skipped without Chromium, `CRAWLER_TEST_CHROMIUM`
names one).

### Studio outputs of a private chat stay with it in Zero

**Why.** `podcast_runs`, `video_presentation_runs` and `deliverable_jobs`
(`bySpace`, `byId`) checked only the workspace, so every member synced the
title, the brief, the progress and the error of outputs made in another
member's private chat, or by their Studio request (which runs in a private
thread of its own, `notebooks/studio/start.py`).

**Now.** Each table has a `thread` relationship (`threadId`, already
published) and its queries keep to rows whose thread the user may read,
the chat's rule (`constrainToOutputsOfReadableThreads`,
`soloclef_web/zero/queries/thread-access.ts`). A row with no thread (a
notebook's audio overview, or an output whose chat was deleted) stays the
workspace's, as the REST routes have it. Nothing new is published (c0029
already publishes the thread's `visibility` and `created_by_id`). The rule
runs in the query route; the phone app does not ask for these rows. **Still
visible to every member, by design or REST:** the output itself is an
artifact, a document of the workspace (the Library and `documents.bySpace`
list its title), and `GET /podcasts/{id}` and the artifact routes check the
workspace permission, not the thread; keeping a private chat's outputs out
of the Library is a product decision, not made here. Test:
`soloclef_web/tests/unit/zero-access/studio-output-access.test.ts` (fails on
the old queries, 3 of 6).

### SSO: no link to an account signed up in the same moment; SSO-linked accounts verified (c0030)

**Why.** When an address got an account between SSO's lookup and its
creation (a second tab), the sign-in linked to that account whatever it was,
so a password sign-up in that moment (unverified) received the person's SSO.
And accounts SSO linked by email before the verified-account rule may be
unverified (the owner's own, `daniel@danvics.com`, was), while agent sign-in
(MCP), which matches by email when its subject was never linked, and SSO by
email now need a verified account.

**Now.** The race decides as any account found by email (`decide()`), so an
unverified one is refused (`OIDC_ACCOUNT_NOT_VERIFIED`) and a second tab's
(made verified) is linked. Accounts an SSO sign-in makes were already made
verified (`UserCreate(is_verified=True)`, `safe=False`); the owner's account
predates that path. **Automatic, once:** `c0030_verify_sso_linked_accounts`
marks verified every active account with an SSO link (`oauth_account`,
`oauth_name` = `OIDC_PROVIDER_NAME`, `authentik` when unset) whose address is
the one Authentik last vouched for (`account_email`, rewritten at each SSO
sign-in; compared without case). An address changed after linking is left
alone. It logs a count only. A read-only count on the live database
(2026-10-11) found one active unverified account with an Authentik link, the
owner's `daniel@danvics.com`, of five accounts, four already verified; the
deploy steps list who it changes before it runs. Its downgrade changes
nothing (the list is in `docker/backups/`). Tests:
`tests/integration/auth_oidc/test_oidc_sign_in.py` (the race),
`tests/integration/test_verify_sso_linked_accounts_c0030_migration.py`
(who it verifies and who not; agent sign-in with an unlinked subject refused
before it and acting for the account after it).

### The billing admin, by email, only on a verified account

**Why.** `is_config_admin` made whichever account held an address in
`BILLING_ADMIN_EMAILS` an administrator (billing, announcements, the MCP
allowlist, community prompts). An account made with the password sign-up
form is never verified, so someone who signed up with the administrator's
address first held those pages.

**Now.** The address counts only for an account whose address is verified
(`ledger.is_listed_admin`); superusers are unchanged, and the admin users
list marks admins the same way. The administrator here (`danitex@`,
"Accounts" below) is verified: the one unverified account is the owner's
daily one, which c0030 verifies. Test: `tests/unit/billing/test_admin_identity.py`.

### Home search snippets in time proportional to the note

**Why.** The notebooks home search flattened every matching note's whole
text with link and image patterns that, from each `[` with no `]` after it,
scanned to the end and failed (20,000 characters of `[` took a second,
200,000 about a minute and a half), and compared every hit with every other
to find the densest window (5,000 hits: half a second), on the request's
event loop.

**Now** (`app/notebooks/search/text.py`): a label stops at the next `[`, an
address at the next `]`, a tag at the next `<`; the window is found in one
pass. A label holding `[` keeps its text from the last `[`, and an address
holding `]` is left as written. Tests:
`tests/unit/notebooks/test_home_search_text.py` (eight crafted
200,000-character bodies under one second; the one-pass window equals the
old one on random texts).

### An MCP server's edit view asks the server for its config

**Why.** Since c0029 Zero carries no connector config, so a workspace MCP
server's live row has `{}` until the REST list has it: its edit view could
open on an empty form, and a change made elsewhere (a tool trusted from a
chat, a revoke, a teammate's edit) never reached a view already open.

**Now.** The view asks `GET /search-source-connectors/{id}` (secrets masked)
each time it opens, never on a window focus, and shows "Loading this
server's settings…" with Save off until the answer is in; a failed request
shows the row as before. Save already asked for it again (the update
mutation's invalidation), and revoking a trusted tool now does
(`connector-configs/views/editable-connector.ts`, `hooks/use-editable-connector.ts`).
Account-wide connections are unchanged. Test:
`soloclef_web/tests/unit/connectors/editable-connector.test.ts`.

### The phone app's update (`fix/app-zero-security`, off `feat/sn-ship`)

JavaScript only; fingerprints unchanged (iOS
`c318ec88645f808a398943c00fd6585aa5d7a690`, Android
`045907f6ded804422dfcd485e0b8138a98cb426c`); `npm run check` passes.

- The Zero copy re-synced from this branch's `soloclef_web/zero` (the app
  branch carries the same web files, so the copy test holds): no
  `search_source_connectors.config`; the threads' `visibility` and
  `created_by_id` and the access rule; the outputs' thread relationships.
- The phone's Zero context keeps `ownedSpaceIds` from `/zero/context` (the
  chat queries run on the phone too; without it a workspace's owner would
  not see a creatorless thread's messages there).
- `mathAsText` reads 2,000 characters and `plainText` turns links into words
  first, as the website and backend do (`docs/limits.md`, "Notes").

**An installed phone without the update, after the server deploy:** it keeps
working and keeps syncing, by polling. zero-cache refuses its schema copy
(it names `search_source_connectors.config`): `SchemaVersionNotSupported`,
Zero disables its client group and calls `onUpdateNeeded`, and the app hands
over to polling for that run ("zero-cache needs a newer app",
`lib/live/zero/session.ts`), again at each start until the update. What
polls: a notebook's sources every 5 s while one is indexing, a chat turn the
phone is not streaming (`GET /threads/{id}` every 4 s), an automation's runs
every 4 s; nothing on screen changes but the push. No screen breaks for
Zero. What does not work from the installed app, Zero aside: connecting
Google Drive ("Update the app to connect Google Drive from it, or connect it
on the website", 400 `HANDOFF_CHALLENGE_REQUIRED`), as connecting a Microsoft
account already does on the live server; Drives already connected keep
working. The hand-off confirmation is in the release's app update
(`release/app-2026-10-11` `3ac6be2e8`, on top of `fix/app-zero-security`),
which fixes both; an updated phone also says it is on Zero in Report an
issue (`Live data: Zero (connected)`, `7df8c50e2`). **With the update on an older server** (no c0029) the new copy
names thread columns that server does not publish: refused the same way,
polling. So any mix degrades to polling, and the update goes out after the
server.

### Deploying the security fixes

With the rest of the release of 2026-10-11: "Deploying the release of
2026-10-11" near the top of this file has the order, the checks and an undo
that works (a code-only revert does not, once c0029 and c0030 have run).

## Chat turns run in the background (2026-10-09)

A chat reply keeps running when the page that asked for it is closed,
reloaded or put to sleep; it saves its answer and any open page shows it.
Stop (`POST /threads/{id}/cancel-active-turn`) ends it from any page. The
deploy gate above also waits for chat turns that are still writing. Timers,
caps and what each one does when hit: `custom/docs/chat-turns.md`.

## Push notifications for the phone app (`c0025_push_devices`, 2026-10-10)

The phone app registers its Expo push token (`POST /api/v1/push/devices`) and
keeps per-kind switches on the server (`/api/v1/push/preferences`). When an
answer, approval, Studio output, automation run, source, mention,
announcement, accepted invite or empty credit balance happens while nobody is
looking, a Celery task sends one generic line through Expo's push service
("Your audio overview is ready") with an in-app path; never any content, and
no titles unless `PUSH_INCLUDE_TITLES=TRUE` (then only the name of the
notebook the push opens, or an announcement's title). Off until
`PUSH_ENABLED=TRUE` in `docker/.env`; phones register either way, each with
its sign-in (`session_id`, required): a sign-in that is not live is refused
with 401, so a phone signed out from the devices list cannot come back on its
last access token. Before turning push on, turn on Expo's "Enhanced push
security" and set `EXPO_PUSH_ACCESS_TOKEN`. Every rule, limit and table:
`custom/docs/push-notifications.md`.

**Every push names the server it comes from** (2026-10-11,
`app/push/server_origin.py`, not deployed): `data["server"]` is the address
phones are set to for this server, `SOLOCLEF_PUBLIC_URL` and nothing else,
in the form the app compares (`scheme://host[:port]` as `URL.origin` gives
it: no path, no trailing slash or dot, lower case, no default port, an
international name in its `xn--` form), on every kind. The phone app, set to
another server under **Change server**, ignores a push (no toast, no
navigation) whose `server` is not that server's origin, because the old
server may still hold its Expo token until the token is dropped; a push with
no `server` (servers before this) it handles as before. Here it is
`https://notes.danvics.com`, from `SOLOCLEF_PUBLIC_URL`, which the Celery
worker (the sender) reads from `docker/.env` (this server sets none of
`BRAND_SITE_URL`, `NEXT_FRONTEND_URL`, `BACKEND_URL`).

Why that one variable (a review of 2026-10-11): the first version took
`app.brand.BRAND_SITE_URL` (`BRAND_SITE_URL`, else `NEXT_FRONTEND_URL`, else
`SOLOCLEF_PUBLIC_URL`), so a server whose `BRAND_SITE_URL` named a
marketing site sent pushes every phone dropped. The app sends everything to
one address (API calls, `/auth/desktop/login`, the SSO start, the website
pages it opens), which only the single public address serves. A server that
phones could not match sends **no** `server`: `SOLOCLEF_PUBLIC_URL` unset,
`http://` (the app is set to https servers only, so an http origin would make
it drop every push) or a host the app refuses (underscores, IPv6, port 0,
over 253 characters; `soloclef_mobile` `src/features/server/address.ts`).
With `PUSH_ENABLED=TRUE` each of these is a `[startup] push:` warning in the
backend's log (`app.py` lifespan) and the Celery worker's (`worker_ready`).

## Owed: the chunks.workspace_id NOT NULL backstop

`c0031_chunks_workspace_id` leaves `chunks.workspace_id` **nullable on purpose**.
Compose runs the `migrations` service to completion while the old containers are
still serving, so setting NOT NULL in that migration would fail any chunk INSERT
from code that does not yet stamp the column.

**On the next deploy after c0031 is live**, add the follow-up migration. Without
it, a write path that forgets the column writes a chunk that no workspace-scoped
search will ever return -- silently, with nothing logged. The exact statements
are in c0031's docstring under "Step 5, on the next deploy".

## pgbouncer in front of Postgres

Postgres has 200 connections (`docker/postgresql.conf`) and the app asks for
far more than it uses: each uvicorn worker keeps its own SQLAlchemy pool
(`DB_POOL_SIZE` 20 + `DB_MAX_OVERFLOW` 30) and its own LangGraph checkpointer
pool (10), and every Celery task opens a connection of its own (`NullPool`).
One API replica can therefore reserve 200 on its own, which is the wall a
second replica hits. `pgbouncer` (service `pgbouncer`, `docker/pgbouncer/`)
multiplexes them: in **transaction** pooling mode a client only holds a
Postgres connection while a transaction is open, so idle app connections cost
nothing and the number of Postgres backends follows concurrent transactions
instead of connected processes. It is capped at 100 (`max_db_connections`),
leaving room for zero-cache's 50 and the migrations service.

What the pooler multiplexes is transactions, not connections: 150 app
connections that have finished their transactions needed 5 Postgres backends
on a throwaway pair, but a connection that leaves a transaction open holds a
Postgres connection for as long as it does — and SQLAlchemy opens one on the
first statement, so a session kept alive across a long model call counts. With
150 of those the pool's 100 ran out and clients waited. That is the number to
watch (`maxwait` below); it is not a regression — those sessions each hold a
Postgres connection today as well — but under the pooler it is a shared
ceiling rather than per process.

Through the pooler: `backend`, `celery_worker`, `celery_heavy`, `celery_beat`,
`mail_sender`. Directly on `db`, and they must stay there: `migrations`
(Alembic takes a session advisory lock and runs DDL) and `zero-cache` (logical
replication, which transaction pooling cannot carry at all).

Two app-side settings go with it, both set by compose:

- `DATABASE_URL` for those five services points at `soloclef-pgbouncer-1:6432`
  (`DATABASE_URL_DIRECT`, or `DB_POOLER_HOST` / `DB_POOLER_PORT`).
- `DB_PGBOUNCER_TRANSACTION_POOLING=TRUE` changes how asyncpg connects
  (`app/db_pooling/connect_args.py`): prepared statements off
  (`statement_cache_size` and `prepared_statement_cache_size` 0, and the
  statement name `None`, which is what makes asyncpg prepare anonymously —
  a named statement does not survive the next transaction landing on another
  server connection, which fails with `prepared statement
  "__asyncpg_stmt_2c6__" does not exist` under load), and no
  `idle_in_transaction_session_timeout` in the startup packet, which pgbouncer
  refuses outright.

That timeout therefore moved into `docker/postgresql.conf`
(`idle_in_transaction_session_timeout = 3600000`), where it survives the
pooler's `DISCARD ALL`. It is one value for everyone now, the more generous of
the app's two (`DB_CELERY_IDLE_IN_TX_TIMEOUT_MS`); the API's tighter 15 minutes
(`DB_IDLE_IN_TX_TIMEOUT_MS`) only applies on a direct connection. pgbouncer
cannot keep the per-client values: `track_extra_parameters` works only for
parameters Postgres reports back, and this is not one (measured on 1.26.1 —
listing it is accepted and then silently never applied).

The config is rendered at container start by `docker/pgbouncer/render-config.sh`
from `pgbouncer.ini.template` and the `DB_*` settings, into a container-local
`/tmp/pgbouncer` (userlist 0600). No credential is in the repository, and none
is in the rendered `pgbouncer.ini`, so

    docker exec soloclef-pgbouncer-1 cat /tmp/pgbouncer/pgbouncer.ini

is safe to read. Pool sizes, timeouts and why each is what it is live in the
template.

**Deploying it.** Nothing to add to `docker/.env`; the compose defaults build
the pooled URL from the `DB_*` lines already there.

    C="docker compose -f docker-compose.yml -f docker-compose.override.yml"
    (cd docker && $C up -d pgbouncer)                 # does not touch the app
    docker exec soloclef-db-1 psql -U surfsense -d surfsense -c 'SELECT pg_reload_conf()'
    docker exec soloclef-db-1 psql -U surfsense -d surfsense \
      -At -c 'SHOW idle_in_transaction_session_timeout'   # expect 1h
    python3 soloclef_backend/scripts/deploy_gate.py --wait && \
      (cd docker && $C up -d backend celery_worker celery_heavy celery_beat mail_sender)

The Postgres reload is the step to not forget: without it the app has no
idle-transaction reaper at all once it stops asking for one per connection.
`up -d pgbouncer` alone is safe at any time — nothing uses it until the app
services are recreated.

**Checking it.** The pooler's own view, with the app's credentials (they are a
`stats_users`, so `SHOW` works and `PAUSE` does not):

    docker exec soloclef-pgbouncer-1 sh -c \
      'PGPASSWORD="$DB_PASSWORD" psql -h 127.0.0.1 -p 6432 -U "$DB_USER" -d pgbouncer -c "SHOW POOLS"'

`cl_active` is connected app connections, `sv_active` the Postgres connections
actually in use; `maxwait` above zero means the pool is the bottleneck and
`default_pool_size` should rise (while `max_db_connections` plus zero-cache's
50 stays under 200). And from the database side:

    docker exec soloclef-db-1 psql -U surfsense -d surfsense -c \
      "SELECT application_name, state, count(*) FROM pg_stat_activity GROUP BY 1, 2 ORDER BY 3 DESC"

pgbouncer names its backends after the pooled client
(`application_name_add_host`), so zero-cache's and the migrations service's own
connections stay distinguishable.

**Testing it.** The integration suite connects wherever `TEST_DATABASE_URL`
points, with the app's own asyncpg settings
(`tests/integration/engine_connect_args.py`), so a throwaway pooler in front of
a throwaway Postgres runs it against the real pooled path. Build the pooler
from this repo's own template, or the run proves nothing about what is
deployed:

    docker network create ivtest-net
    docker run -d --name ivtest-db --network ivtest-net -p 127.0.0.1:55432:5432 \
      -e POSTGRES_USER=surfsense -e POSTGRES_PASSWORD=surfsense \
      -e POSTGRES_DB=surfsense_test pgvector/pgvector:pg17 \
      -c password_encryption=scram-sha-256 -c idle_in_transaction_session_timeout=3600000
    docker run -d --name ivtest-pgbouncer --network ivtest-net -p 127.0.0.1:56432:6432 \
      -v "$PWD/docker/pgbouncer/pgbouncer.ini.template:/etc/pgbouncer/pgbouncer.ini.template:ro" \
      -v "$PWD/docker/pgbouncer/render-config.sh:/usr/local/bin/render-pgbouncer-config:ro" \
      --entrypoint /usr/local/bin/render-pgbouncer-config --ulimit nofile=8192:8192 \
      -e DB_HOST=ivtest-db -e DB_PORT=5432 -e DB_NAME=surfsense_test \
      -e DB_USER=surfsense -e DB_PASSWORD=surfsense -e PGBOUNCER_PORT=6432 \
      edoburu/pgbouncer:v1.26.1-p0

Then run the suite twice — port 55432 with the flag unset, port 56432 with
`DB_PGBOUNCER_TRANSACTION_POOLING=TRUE`. A test that passes direct and fails
pooled is a production bug, not a test problem. Run `alembic upgrade head`
against the direct port first: on an empty database it builds the head-shape
schema and stamps, so the suite's own `create_all` finds the migrated tables
already there.

**Bypassing it.** Set both in `docker/.env` and recreate the app services:

    DATABASE_URL_DIRECT=postgresql+asyncpg://surfsense:<password>@soloclef-db-1:5432/surfsense
    DB_PGBOUNCER_TRANSACTION_POOLING=FALSE

They move together. Pointing the app at pgbouncer with the flag `FALSE` fails
at connect with `unsupported startup parameter`, and the flag `TRUE` against a
direct Postgres silently leaves the idle-transaction reaping to
`postgresql.conf` (which, after this change, is set there anyway).

**One rough edge.** `setup_indexes()` in `app/db.py` runs `SET lock_timeout`
and then its `CREATE INDEX CONCURRENTLY` statements in autocommit: under
transaction pooling those are separate transactions, so the `SET` may land on
a different server connection than the index builds and the fail-fast
lock_timeout is no longer guaranteed to apply to them. The pooler's
`server_reset_query_always = 1` keeps the stray `SET` from leaking onto the
next client, and the builds themselves are `IF NOT EXISTS` no-ops once the
indexes exist. The clean fix is for boot-time DDL to use a direct connection
of its own (or for `DB_BOOTSTRAP_ON_STARTUP` to go `FALSE` here, once every
index in `_INDEX_DEFINITIONS` is covered by a migration); neither is done yet.

## Background workers and queues

Three Celery queues (`app/celery_app.py`): `surfsense` for quick, user-facing
work (uploads, document processing, reindexing), `surfsense.connectors` for
long connector indexing, and `surfsense.heavy` for minutes-long generation
(`HEAVY_TASKS`: podcast transcript and audio, video presentations, Studio
deliverables). On this host `celery_worker` consumes the first two with up to
`CELERY_FAST_MAX_WORKERS` (16) processes, and `celery_heavy` consumes the heavy
queue with up to `CELERY_HEAVY_MAX_WORKERS` (4), so a burst of podcasts never
holds up uploads (2026-11-06). Both minimums are settable too
(`CELERY_FAST_MIN_WORKERS`, `CELERY_HEAVY_MIN_WORKERS`). A worker started with
no `CELERY_QUEUES` consumes all three, so a single-worker deployment still runs
everything.

## Names on the shared networks

The compose project is `soloclef`: containers are `soloclef-<service>-1`,
images `soloclef-*`, the default network `soloclef_default` and the data
volumes `soloclef-*`. The volumes kept their `soloclef-*` names after the
2026-09-26 project rename and were copied to the new names on 2026-11-06; the
old `soloclef-*` volumes are left in place as a fallback until deleted by hand.

The backend and both Celery services also join `danvics_litellm` and
`danvics_mcp`, where other stacks' containers answer to generic names: LiteLLM's
Postgres is `db`, its Redis is `redis`, the quiz app's backend is `backend`, and
the clinical MCP server is `mcp`. So anything those three services reach inside
this stack is addressed by container name: `DB_HOST=soloclef-db-1` and
`REDIS_URL=redis://soloclef-redis-1:6379/0`. These are the compose defaults, and
docker/.env sets the same. A bare `db` there once connected the app to LiteLLM's
database ("password authentication failed"). `searxng` and `litellm` are safe
as they are: the first is unique, and the second is meant to be LiteLLM. Services
only on `soloclef_default` (proxy, mcp, zero-cache, frontend) may keep using
service names.

## Local migrations — `c0001_notebooks` and after

Notebooks added tables upstream does not have. Their migration is named
`c0001_notebooks` rather than taking upstream's next number, so an upstream
`192` cannot collide with it, and the `migrations` service runs our image
(`docker-compose.override.yml`), because upstream's image does not know
that revision and would refuse to start the stack.

`c0002_notebook_chat_settings` follows it (a notebook's "Configure chat"
settings and the per-user `notebook_features` pins behind the home's
"Featured notebooks" row); later local revisions chain on the newest `c000N`.
`c0004_workspace_quick_model` stores the per-workspace quick-tasks model
(Settings -> Models) in its own table rather than a column on upstream's
`workspaces`, so upstream's model keeps its shape. `c0006_workspace_studio_model`
does the same for the Studio outputs model (a default plus per-output-type
overrides, as JSON) and adds `notebook_outputs.generated_by`, the model that
wrote each Studio output. `c0007_long_tool_call_ids` widens upstream's
tool-call id columns to TEXT (Gemini ids carry a long thought signature),
with hash indexes and an md5 idempotency key for deliverable jobs; an
upstream migration that touches those columns or indexes needs a look.
`c0022_chat_web_search_prices` seeds the `exa_search` and `tavily_search`
prices for the chat's Exa and Tavily tools (see "Web search in chat").

`c0011_drop_upstream_payments` drops what upstream's removed payment code
stored: `credit_purchases`, `page_purchases`, `user_incentive_tasks`, their
enum types (`creditpurchasestatus`, `pagepurchasestatus`, `incentivetasktype`)
and the five `user.auto_reload_*` columns. All were empty or at their defaults;
a root-only backup (`surfsense-20260925T203723Z-before-c0011-drop-upstream-payments.*`
in `docker/backups/`: a `pg_dump -Fc` of the tables, the user columns as CSV,
their schema and row counts) was taken first. Its downgrade recreates the
structure; restore data from the dump. Kept: `user.stripe_customer_id`,
`user.credit_micros_balance` and `credit_micros_reserved`, `signup_credit_claims`,
and `billing_ledger`'s `incentive` rows.

`c0031_chunks_workspace_id` adds `chunks.workspace_id` (a copy of the owning
document's workspace) so a workspace-scoped vector search has its filter and
its HNSW index on one table. With the filter on `documents` instead, a forced
HNSW plan on a synthetic corpus returned **0 rows for a LIMIT 30** — the index
offered its 40 candidates, the filter removed all 40, nothing was logged. It
backfills in committing batches and ends with `SET NOT NULL`; the old
containers are still up while `migrations` runs, so for the seconds between
that and their restart a chunk INSERT from the old code fails. That is an
indexing task, retried by the next sync — but ship this with the code that
matches it, behind the deploy gate. The backfill is slow per row on a large
`chunks` (every row version has to be re-pointed from the HNSW index on
`embedding`): measured at ~175 rows/second on a 60k-row, 1024-dimension
corpus, so budget accordingly once the table is large. Every other step took
under a second there, `SET NOT NULL` included, because it goes through a
validated CHECK rather than a table scan.

`c0033_chunks_document_workspace_fk` closes the one corruption `c0031` and
`c0032` cannot catch. Both of their constraints read `chunks.workspace_id`
alone — the FK only asks that the workspace exists, NOT NULL only that a value
is there — so a chunk stamped with a *real but wrong* workspace passes
everything and is then invisible to its own workspace's search and offered to
another workspace's, with nothing raised and nothing logged. The revision adds
a unique key on `documents (id, workspace_id)` (built `CONCURRENTLY`, then
adopted with `ADD CONSTRAINT ... UNIQUE USING INDEX`, so there is no second
blocking index build) and a composite FK `chunks (document_id, workspace_id)`
against it, `NOT VALID` then `VALIDATE`d as `c0031` does. `ON UPDATE CASCADE`,
so a document that ever moves workspace restamps its chunks instead of being
refused; `ON DELETE CASCADE` to match the `document_id` FK. A row that already
disagrees fails the `VALIDATE` rather than being rewritten — the migration
cannot know which side is right.
`SELECT count(*) FROM chunks c JOIN documents d ON d.id = c.document_id WHERE
c.workspace_id IS DISTINCT FROM d.workspace_id` is **0** on this server, so the
validate passes. Cost: one more btree on `documents` and one index probe per
chunk INSERT, against an HNSW insert on a 1024-dimension vector in the same
statement. Two ORM consequences, both in `app/db.py`: `Document.chunks` /
`Chunk.document` now name `foreign_keys="Chunk.document_id"` (two FK paths link
the tables), and a `select(...).join(Document)` with no onclause became
ambiguous — `app/services/connector_service.py` names its join.

`c0034_folder_sibling_name_uq` creates `uq_folder_space_parent_name`, the
unique index `create_folder`, `update_folder` and `move_folder` have each caught
a 409 for since migration 109 and which **did not exist**. 109 created it in raw
SQL; every database built since is created by `Base.metadata.create_all` and
stamped (`alembic/env.py`'s fresh-database fast-forward), and the model never
declared it — so the live `folders` table carried nine indexes and none of them
was this one, the three 409 branches were dead, and duplicate sibling names were
accepted. They must not be: a folder's knowledge-store path is built from its
name with nothing to break a collision (`app/knowledge_store/paths/legacy.py`
suffixes a *document* whose title is taken and does nothing of the kind for a
folder), so two siblings called "Reports" occupy one `/documents/Reports` path
and a rename of either moves the other's git subtree. The index is now declared
on `Folder.__table_args__` as well, which is what a new database actually gets.
It is an expression index on `(workspace_id, coalesce(parent_id, 0), name)`
because `parent_id` is NULL at the root and a plain unique key would exempt
every top-level folder — 109's own reasoning. Built `CONCURRENTLY` and then
checked for `indisvalid`, because a failed concurrent build leaves an invalid
index behind rather than raising. **Existing duplicates fail it**, so the
migration counts them first and stops with the list rather than half-building:
`SELECT workspace_id, coalesce(parent_id, 0), name, count(*) FROM folders GROUP
BY 1,2,3 HAVING count(*) > 1` is **0 rows on this server** (`folders` holds one
row). A deployment where it is not has to rename one of each pair first; which
row keeps the name is not a decision a migration can make.

Two local branches that each add a revision on the same parent merge without
a conflict and leave two heads too: `feat/chat-search-tools` added
`c0022_chat_web_search_prices` and `feat/notes-page` added
`c0023_notebook_note_origin`, both on `c0021_scrape_crawl_billing`. The
release that merged both (`release/2026-10-09`) re-chained `c0023` onto `c0022`
(its `down_revision` and its `Revises:` line), so the chain is
`c0021 -> c0022 -> c0023`. Do the same for the next pair: re-chain one onto
the other; `tests/unit/db/test_alembic_heads.py` fails until there is one head.

When an upstream pull brings a migration that also revises `191`, the history
has two heads and `alembic upgrade head` stops. Fix it with a merge revision
in `soloclef_backend/alembic/versions` (`alembic merge -m "merge upstream"
<upstream-rev> <newest c000N>`), rebuild, and deploy. Take a
`pg_dump -Fc` into `docker/backups/` (ignored by git) before applying any
migration to the live database.

## Notes page and automation notes (`c0023_notebook_note_origin`, 2026-10-09)

**Why.** "Daily unread emails summary" (automation 3, workspace 1) ran every
day, but its task only said "save the summary as a note" and it had no "Save
results to notebook", so its agent wrote a Markdown file each day
(`/documents/notes/Email_Summary_<date>.md`: documents 199-207, type `NOTE`,
folder 1 "notes"). No Studio panel shows such files, so it looked as if it
had never run. The chat's automation drafter taught that mismatch: its
examples saved notes "in folder N", while the form saves into a notebook.

**The Notes page** (rail: Notes, `/dashboard/<id>/notes`; backend
`app/workspace_notes`, web `features/notes`) lists every note the person can
see, newest first (by when it was made):

- every notebook note (Add note, Save to note, an automation's "Save results
  to notebook"), and
- every note document (`documents.document_type = 'NOTE'`) that no notebook
  note stands for: files the assistant wrote in a chat or in an automation
  run. A converted note's document is listed once, as its note.

The page groups the list where each note lives (`features/notes/lib/
note-groups.ts`): each notebook's notes under the notebook (the notebook with
the newest note first), then the note documents under their folder of the
documents tree, named by its path ("Projects / notes") and in the tree's
order (depth first, siblings by `folders.position`, as `GET /folders` lists
them; `app/workspace_notes/folder_places.py`), then those at the top of the
tree under "Not in a folder". Every row in the list carries `folder`
(`id`, `name`, `path`, `tree_order`; `null` at the top and for notebook
notes). Groups without a note are not shown; a group's heading folds it away
(not remembered). Once an automation has made a note, "Automation notes"
switches to one group per automation (a deleted one under its last name, or
"Deleted automation").

Each row says its notebook (or "Not in a notebook"), what made it ("By
<automation>", "From a chat", "Written by hand"), the notebooks it is a source
of, and when; a row leaves out what its group's heading already says. Search matches the title and the text in any case, on the
server, 300 ms after typing stops (`NOTES_SEARCH_DEBOUNCE_MS`). There is no
cap on how many notes are listed. The preview under a title is the first 240
characters with Markdown marks removed (`PREVIEW_CHARS`, display only; the
whole note loads when opened).

- A **notebook note** opens in the Studio editor (saves as you type) with
  Convert to source, Delete, Open in notebook, and Copy to a notebook (a copy
  with the same text, origin and automation; never a source there).
- A **note document** opens with a Save button (each save reindexes it, so it
  does not save as you type), Delete, Convert to source (adds the document as
  a source of the notebook you pick) and **Add to a notebook**, which moves
  it: it becomes that notebook's note with the same text, dates, origin and
  automation, and the document is deleted (recorded in the knowledge store
  like any note delete). A document a notebook uses as a source (itself or
  through a source folder at any depth) is not deleted: the note is a copy,
  tied to it ("Added as a source") when the source is in that same notebook.
  A note still being processed is refused with "still being processed" (409).
- Who: listing needs notebook read access (`chats:read`); note documents are
  included only with `documents:read`. Add to a notebook needs `chats:update`
  and `documents:update` (Owners and Editors), and deletes the document only
  for a caller with `documents:delete` (Owners by default). For anyone else,
  Editors included, it is a copy and the document stays: the notebook note
  can be deleted with `chats:update` alone, so a move must not be a way
  around "Documents (no delete)". Deleting a note document
  (`DELETE /api/v1/workspaces/{id}/notes/{doc}`) needs `documents:delete`;
  the web shows its Delete only to those who have it. Viewers read.
- API: `GET /api/v1/workspaces/{id}/all-notes?q=`,
  `POST /api/v1/workspaces/{id}/all-notes/documents/{doc}/move`
  (`{"notebook_id": N}`), `GET .../notebooks/{nb}/notes/{note}`,
  `POST .../notebooks/{nb}/notes/{note}/copy` (`{"notebook_id": N}`).
  `POST .../notebooks/{nb}/notes` takes `origin` `hand` (default) or `chat`;
  the web's Save to note sends `chat`.

**`c0023_notebook_note_origin`** adds `notebook_notes.origin` (`hand`, `chat`
or `automation`; default `hand`; check constraint) and
`notebook_notes.automation_id` (FK to `automations`, `ON DELETE SET NULL`, so
deleting an automation keeps its notes). Schema only: existing rows become
`hand`. Not in the Zero publication. Copy notebook copies both columns.

**Automations now:**

- "Save results to notebook" notes carry `origin = 'automation'` and the
  automation's id.
- A step with "Save results to notebook" checks that the notebook still
  exists before its agent runs (it used to fail after a paid model call), and
  the agent is told its final answer is saved there as a note and not to also
  write a file (which gave the same note twice).
- The note files a run's own agent writes are stamped when they are written:
  the step puts the stamp in its agent's config (`configurable`, key
  `soloclef_automation_note_stamp`), and the agent's end-of-turn persistence
  adds `automation_id`, `automation_name` and `automation_run_id` to the
  metadata of each note document that turn creates
  (`app/workspace_notes/automation_stamp.py`). Nothing is matched by time, so
  a chat note or another automation's file written in the same minutes is
  never claimed. Limits: only files written with the agent's file tools are
  stamped (a CSV saved by the `export_run` tool is not); a file the run only
  edits keeps what it said; a step
  cut off before its agent finishes persists no files at all; on a workspace
  using the git knowledge store the stamp goes on the rows the turn's commit
  projects, and if that projection stands aside (a rebuild holds the index
  lock) the indexer writes the rows later without it (the note then reads
  "From a chat"; a failure to stamp is logged and never fails the turn).
- The chat's `create_automation` drafter is given every notebook of the
  workspace (no cap) and the notebook the chat belongs to. "Keep it as a note"
  becomes `save_to_notebook_id`: the notebook the person names, else the
  chat's own; with neither, it is left out and the description says to pick
  one. Its examples no longer save notes into folders.

**One-off for notes written before this** (`scripts/attach_automation_notes.py`,
logic in `app/workspace_notes/attach_automation_notes.py`). Report-only unless
`--apply`; safe to re-run. For each automation (or each `--automation N`):

1. takes the note documents stamped with it, when the stamp names one of its
   runs (or no run); a stamp naming another automation's run is left and
   reported;
2. lists, as **matched by time only**, the unstamped note documents its owner
   created in its workspace between a run's start and end (a run with no
   recorded end counts until its timeout, `execution.timeout_seconds`, else
   600 s). A chat note or another automation's file written in those minutes
   matches too (the report says when a match also falls in another
   automation's run), so the script does nothing with one unless it is named
   with `--document` (`199`, `199,200` or `199-207`; needs exactly one
   `--automation`). Named ones are stamped with the automation and that run;
   an id that is not one of its matches is reported and not touched;
3. moves the stamped and named documents into its "Save results to notebook"
   notebook, or into `--notebook N` (needs exactly one `--automation`), with
   the Notes page's move (the document is deleted once it is a notebook
   note); documents in use as a source stay and are reported;
4. labels the notebook notes "Save results to notebook" wrote before
   `c0023` (title "<automation name> — YYYY-MM-DD" in a notebook one of its
   runs saved into).

Live data on 2026-10-09: automation 3 wrote documents 199-207 (runs 6-14),
automation 4 "Test: unread email summary" wrote 198 (run 4); neither has a
"Save results to notebook". Automation 5 saved notebook note 8 into notebook
18 "Email Search Assistance". After the deploy:

    docker exec soloclef-backend-1 python scripts/attach_automation_notes.py
    # check the titles it lists as matched by time only, then, for the
    # daily summary, into the notebook you choose:
    docker exec soloclef-backend-1 python scripts/attach_automation_notes.py \
        --automation 3 --notebook 18 --document 199-207 --apply
    # label notebook note 8, which automation 5 saved:
    docker exec soloclef-backend-1 python scripts/attach_automation_notes.py \
        --automation 5 --apply

Setting "Save results to notebook" on automation 3 (Edit) fixes its future
runs; its files written after this deploy are stamped and move without
`--document`.

The mobile app has no notes screens (its home search shows note passages and
opens their notebook); it is unchanged.

## Notes: drawn once, as the chat draws them; titles as words (2026-10-10)

**Why.** Daniel: "notes flickering and doesn't display markdown well",
"initially bad display then flickers and shows nothing". A note kept from a
chat answer was titled "…Pearson's \\(r = 0.463\\), statistical…" and its text
showed "Pearson's (r = 0.463)". Seen on the e2e stack with the answer's shape
and the phone app's note fixtures (`soloclef_web/tests/fixtures-files/notes`):

- Opening a note showed the list, a placeholder, a second placeholder while
  the editor's code loaded, then the note; the note editor's preset had no
  tables or maths, so a table showed one cell under another and every
  formula as nothing ("A grant of $5 … $10" lost its words between the
  dollars, read as a formula); `\(…\)` was read as an escaped "(".
- Clicking into a note saved it: the editor reported every change, a click
  included, and its rewriting of the note (backslashes gone, `\[citation:12]`,
  money dropped) went out with the autosave. A note document's Save turned on
  by itself.
- Typing while a save was on its way: the save's answer loaded the older
  text into the editor, dropped the last words and put the caret at the
  start.
- A note with an HTML tag and an autolink (`<https://…>`, which MDX cannot
  read) showed nothing after the tag: Plate's own fallback keeps only the
  text before the first unclosed tag. A list inside a quote and a reference
  link lost their words too.

**Now** (web, `features/notes`, `features/notebooks/ui/studio/note-editor.tsx`,
`components/editor`): the Notes page keeps the list until the note's text and
the editor's code are both there (`hooks/use-note-opening.ts`; the code is
fetched as the page mounts, `components/editor/plate-editor-module.ts`), then
draws the note once. The note editors use the `note` preset (tables and
maths) and read maths as the chat does (`utils/chat-math.ts`: `\( \)`,
`\[ \]`, `$$ $$`; a single `$` is text). KaTeX draws with the formula
(`components/ui/katex-html.ts`). The editor reports only real edits
(`utils/edited-markdown.ts`), writes citation markers as the chat does
(`utils/citation-markers.ts`), and falls back to plain Markdown on the whole
text when MDX fails (`utils/safe-deserialize.ts`, with `quoted-lists.ts` and
`reference-links.ts`; a `<br>` typed in code stays `<br>`). Citation markers
show as the chat's chips, which can be deleted but not edited, and are
written back as markers (`utils/citation-nodes.ts`, the `chatCitations` prop;
a marker of several ids is written as one marker per id). No new limit or
timer.

After review: the editor shows the note's newest text (a read again, an
earlier save landing) until the person types, then keeps its own; it used to
take the text it opened with, so a newer one was overwritten by the next
keystroke. A notebook note's save (`features/notebooks/lib/note-saves.ts`)
puts the text in both of its caches, the Studio list and the Notes page's
note, as it starts; a read of notes asked while a save is on its way waits
for it, one already on its way is called off; a note's saves reach the
server one after another, in the order made. An action such as Convert to
source waits for the last keystrokes' save.

Titles: Save to note (web), the backend (`app/workspace_notes/readable_text.py`:
a `chat` note's title, a note document named from its text, the Notes list's
preview) write maths as words; money such as "$3 and $5" stays. Only a
text's first 2,000 characters are read (`MATH_TEXT_MAX_CHARS`, web and
backend alike, since 2026-10-11: "Security fixes of 2026-10-11"), after
every link and image of the whole text has become its words
(`links_as_words`, the web's `linksAsWords`), so a long pasted image at the
top no longer leaves its markup as the title.

**Existing titles** (one-off, `scripts/clean_note_title_math.py`, logic in
`app/workspace_notes/clean_math_titles.py`): run after the backend with it
is deployed. A dry run on 2026-10-10 (read-only, live database) listed
**1 title** (1 notebook note, 0 note documents). Note texts are not touched;
the same read-only check counted 1 note whose title still has `\(` but
whose text no longer does: the old editor rewrote it when it was opened, and
its backslashes cannot be put back from the database (the answer is still in
its chat).

```bash
# 1. report only (read-only transaction)
docker exec soloclef-backend-1 python scripts/clean_note_title_math.py
# 2. back up the old titles and apply, then keep the backup
docker exec soloclef-backend-1 python scripts/clean_note_title_math.py \
    --apply --backup /tmp/note-title-math-backup.json
docker cp soloclef-backend-1:/tmp/note-title-math-backup.json docker/backups/
# undo, if ever needed (only titles still as written by step 2)
docker exec soloclef-backend-1 python scripts/clean_note_title_math.py \
    --restore /tmp/note-title-math-backup.json
```

## Sign-in with Authentik (OIDC, `app/auth_oidc`, 2026-10-09)

The sign-in page offers **Continue with Danvics SSO** above the password
form. It signs in through Danvics SSO (`sso.danvics.com`,
application slug `notes`, client `soloclef-notes`; blueprint
`/home/danvics/docker/authentik-soloclef/blueprints/danvics-notes.yaml`).
Who may use it is decided in Authentik, in "any" mode: the group
`notes-users` (danitex and daniel@danvics.com), or the policy
`danvics-accounts-only` (named `soloclef-accounts-only` until the 2026-10-09
rename; since 2026-10-09: any @danvics.com address except members of
`app-reviewers`, the same rule as Nextcloud and Immich, and bound to them
too). Everyone else, the App Review account and the gmail friends included,
is stopped at Authentik's "permission denied" page and never reaches Notes.
That policy must stay: deleting it locks ifee@ and akadmin out of Notes. An
unused policy of the same name on PedsHub's Authentik was a different object
and was deleted on 2026-10-09.

It is a second door, not a replacement. `AUTH_TYPE` stays `LOCAL`, so the
password form, "Forgot password?", and the `/auth/desktop/*` routes the
mobile app (and Apple's reviewers) sign in through all keep working.

**Flow.** `GET /auth/oidc/authorize-redirect` -> Authentik (email, then the
emailed code) -> `GET /auth/oidc/callback` -> Notes sets its usual session
cookies and redirects to `/dashboard` (or to the page the person was sent
away from, when it is a path on this site). Authorization code flow with
PKCE (S256), a nonce, and the client secret sent as HTTP Basic. The
id_token is checked against Authentik's published keys (RS256; `none` and
HMAC are never accepted): issuer, audience, expiry (60 s clock leeway) and
nonce. After the callback Notes never talks to Authentik again: the session
is Notes' own, with the same lifetimes as a password sign-in
(`ACCESS_TOKEN_LIFETIME_SECONDS`, `REFRESH_TOKEN_LIFETIME_SECONDS`,
`REFRESH_ABSOLUTE_LIFETIME_SECONDS`), so no `offline_access` scope is needed
and signing out of Authentik does not sign anyone out of Notes.

**Which account.** A sign-in seen before goes to the account it was linked
to, found by Authentik's subject id (`sub_mode: hashed_user_id`, so an email
change in Authentik does not break it). A first sign-in is linked to the
Notes account with the same email, only if Authentik marks the email
verified (the "Danvics email profile" mapping does), whatever its domain,
and only if that account's own address is verified (`is_verified`; else
`OIDC_ACCOUNT_NOT_VERIFIED`, see "Security fixes of 2026-10-11").
With no such account, an address at a domain in `OIDC_AUTOCREATE_DOMAINS`
(`danvics.com`) gets a new account on that first sign-in, made exactly as
sign-up makes one (`UserManager.create`, then `on_after_register`: signup
credit, "My Workspace" with its default roles and Owner membership, the
default prompts). It is verified, not an admin (admin comes from
`BILLING_ADMIN_EMAILS`), and has a random password nobody knows; the person
sets one with "Forgot password?" to use the password form or the mobile
app. Any other address is refused ("No account for this address"); make
its account with `scripts/create_account.py` and SSO then links to it.
Notes does not read group names: keeping the App Review account
(appreview@danvics.com) out is Authentik's binding, above. The rule is
`app/auth_oidc/account_creation_rule.py`. Links
are rows in `oauth_account` (`oauth_name` = `OIDC_PROVIDER_NAME`,
`account_id` = the subject). The provider's tokens are not kept:
`access_token` is stored empty, because Notes never uses it and Authentik's
are ~2,300 characters, longer than the column's 1,024.

**Settings** (`docker/.env`; the backend reads all of them, the frontend
container gets `OIDC_ENABLED` and `OIDC_BUTTON_LABEL` through
`docker-compose.yml`, and the frontend build gets the non-secret ones as
`NEXT_PUBLIC_OIDC_*` for the docs' wording; see "This server's own settings"):

| Setting | Value here | Meaning |
|---|---|---|
| `OIDC_ENABLED` | `TRUE` | Mounts the two routes and shows the button. `FALSE` removes both; password sign-in is unaffected. |
| `OIDC_DISCOVERY_URL` | `https://sso.danvics.com/application/o/notes/.well-known/openid-configuration` | Its `issuer` must equal this URL minus the suffix, or sign-in is refused. |
| `OIDC_CLIENT_ID` | `soloclef-notes` | |
| `OIDC_CLIENT_SECRET` | (secret) | Same value as `NOTES_OIDC_CLIENT_SECRET` in `authentik-soloclef/.env`. |
| `OIDC_BUTTON_LABEL` | `Continue with Danvics SSO` | Default "Continue with single sign-on". |
| `OIDC_AUTOCREATE_DOMAINS` | `danvics.com` | A verified address at one of these domains with no Notes account gets one on its first sign-in. Comma or space separated; exact domains, no wildcards (`sub.danvics.com` and `evil-danvics.com` do not match). Empty (default): no account is made this way. |
| `OIDC_ALLOW_SIGNUP` | `FALSE` (default) | `TRUE` makes a Notes account for a verified email that has none, at any domain (`OIDC_AUTOCREATE_DOMAINS` then makes no difference). |
| `OIDC_SCOPES` | default `openid email profile` | `openid` is always added. |
| `OIDC_PROVIDER_NAME` | default `authentik` | Stored on each link; changing it orphans every existing link. |
| `OIDC_LOGIN_ATTEMPTS_PER_MINUTE` | default `30` | See limits below. |

The callback URL is `{BACKEND_URL}/auth/oidc/callback`
(`https://notes.danvics.com/auth/oidc/callback`), which must be the
provider's only (strict) redirect URI. If `OIDC_ENABLED=TRUE` but a setting
is missing, the backend logs `OIDC_ENABLED=TRUE but ... not set` naming it
and starts without the routes.

**Limits and automatic behaviour**

- *Sign-in window: 10 minutes.* `state`, nonce and PKCE verifier live in a
  signed, HttpOnly cookie `notes_oidc_login` (path `/auth/oidc`, SameSite
  Lax) for `LIFETIME_SECONDS` = 600 (`app/auth_oidc/login_attempt.py`).
  A callback after that, or in a browser that did not start the sign-in,
  is refused with "Sign-in expired". Not configurable; it is a security
  check, not a quota.
- *30 attempts a minute per client IP* (`OIDC_LOGIN_ATTEMPTS_PER_MINUTE`),
  counting starts and callbacks together (a sign-in is two), in Redis like
  the password limits (scope `oidc_login`). A guard against hammering
  Authentik's token endpoint through Notes. When hit, the sign-in page says
  "Too many attempts".
- *Discovery document and keys are cached for an hour* (`CACHE_SECONDS`,
  `app/auth_oidc/discovery.py`); an unknown key id re-reads the keys at
  once, so a key rotation in Authentik needs no restart.
- *Provider timeout: 10 s* per request (`FETCH_TIMEOUT_SECONDS`). If
  Authentik cannot be reached the page says "Single sign-on unavailable"
  and the password form still works.
- *A first sign-in may create an account* (automatic): only for a
  verified address at a domain in `OIDC_AUTOCREATE_DOMAINS` (or any address
  with `OIDC_ALLOW_SIGNUP=TRUE`), and only for people Authentik lets reach
  Notes. On start-up the backend logs the rule in force, e.g. `OIDC sign-in
  on; a first sign-in with no account creates one for verified addresses at
  danvics.com`. Two first sign-ins at once (two tabs) make one account; the
  second is linked to it.
- `REGISTRATION_ENABLED=FALSE` (the kill switch, see above) closes these
  routes too.

Every refusal lands on `/login?error=<code>` and the page words it
(`soloclef_web/lib/auth-errors.ts`; codes in `app/auth_oidc/errors.py`).

**Turn it off**: `OIDC_ENABLED=FALSE` in `docker/.env`, then after the
deploy gate `docker compose ... up -d backend frontend`. Nothing else
changes; existing links stay in `oauth_account` for when it is turned on.

**History.** A first OIDC sign-in (2026-09-21) switched `AUTH_TYPE` to
GOOGLE through build-time patches and replaced the password form; Daniel
had it removed the same day because it meant signing in twice. This one
sits beside the password form and lands straight on the dashboard.

## The phone app signs in (`app/auth_oidc/app_sign_in.py`, `c0024_app_sessions`, 2026-10-10)

The Soloclef phone app (`soloclef_mobile/`, branch `feat/soloclef-native`)
signs in through the same Danvics SSO client as the website, but it cannot
read the cookies a web sign-in ends with, so it asks for a different ending
(plan items B1 and B3; PedsHub's `src/utils/appSignIn.js` is the pattern).
Not deployed yet: it is on `feat/sn-backend-account`.

**Flow.** The app opens, in the phone's browser sheet,
`GET /api/v1/auth/oidc/app/start?app_redirect=soloclef://auth&challenge=<S256>&challenge_method=S256&device=<label>[&choose_account=1]`.
The backend checks `app_redirect` against the allow-list
(`OIDC_APP_REDIRECTS`, default `soloclef://auth`: a start naming any other
address gets a 400 and no redirect), keeps the app's request in the same
signed cookie as a web sign-in (`login_attempt.py`), and sends the browser to
Authentik. `choose_account=1` ("Use a different address" in the app) adds
`prompt=select_account`; it is never `prompt=login`, which Authentik 2026.8
loops on (PedsHub, 2026-10-08). Authentik comes back to the one registered
callback, `/auth/oidc/callback`; for an app attempt the callback runs the
usual account linking and then redirects to `soloclef://auth?code=…` with a
one-time code (60 s, single use, stored hashed in Redis under
`soloclef:app_sign_in:*`; a Redis that is down falls back to the worker's
memory with a warning) or to `soloclef://auth?error=<code>` with the same
codes the login page explains (`OIDC_NO_ACCOUNT` and so on). The app spends
the code with `POST /api/v1/auth/oidc/app/exchange {code, verifier,
device_label}`; the verifier must hash (S256) to the challenge the start was
given, so another app on the phone that catches the redirect holds a code it
cannot spend. The answer is `/auth/desktop/login`'s (`RefreshTokenResponse`)
plus `session_id` (the token family) and `end_session_url` (Authentik's
end-session page from its discovery document, when it publishes one; the
app's "Also sign out of Danvics SSO" on Android opens it). Both routes share
the OIDC rate limit (`OIDC_LOGIN_ATTEMPTS_PER_MINUTE` per IP) and the
`REGISTRATION_ENABLED` kill switch, and exist only when OIDC is on.

**App sessions (B3).** A refresh token now has a `kind` (`web` or `app`) and
a `device_label` (`refresh_tokens`, migration `c0024_app_sessions`; a check
constraint keeps `kind` to those two). An `app` family lives
`APP_SESSION_LIFETIME_SECONDS` (default 365 days) from the sign-in with no
idle timeout, the rule PedsHub Tools uses: each rotated token lives as long
as the family. `web` families keep the sliding `REFRESH_TOKEN_LIFETIME_SECONDS`
/ `REFRESH_ABSOLUTE_LIFETIME_SECONDS` rule. The SSO exchange always makes an
`app` family; `POST /auth/desktop/login` makes one when the body carries
`device_label` (the phone app's password sign-in, App Review's included) and
a `web` one without it (the desktop app). Every token answer now names the
family as `session_id`.

**Signed-in devices.** `GET /auth/devices` lists the account's live families
(label, kind, signed in, last used, expires; newest use first) and
`DELETE /auth/devices/{session_id}` revokes one; both session-only (no API
keys). The app shows them under More → You → Signed-in devices and marks its
own row by the `session_id` its sign-in returned. "Sign out everywhere"
(`/auth/jwt/logout-all`) still ends every family at once. "Signed in" is the
family's `signed_in_at` (`c0027_refresh_token_signed_in_at`), which every
rotated token copies, because the daily `purge_refresh_tokens` (03:41)
deletes a family's older rows; rows from before `c0027` are dated by their
earliest row.

**Offboarding.** Who may sign in is decided at sign-in, as on the website:
the same Authentik client (its Notes bindings), then `account_for` and
`is_active`. Nothing asks Authentik again afterwards, and an app session
lasts `APP_SESSION_LIFETIME_SECONDS` (365 days; a web one at most
`REFRESH_ABSOLUTE_LIFETIME_SECONDS`, 30 days). So removing someone from
Notes in Authentik does not end their phone's session: **also deactivate
their Notes account**, which refuses every request from the next one (there
is no page for it: the `is_active = false` update under "Cut an agent off"
in the AI agents section below), or **sign them out everywhere** (the
person's own Signed-in devices list or "Sign out everywhere"), which also
ends their phones' pushes.

**The return address is a custom scheme.** `soloclef://auth` can be claimed by
another app on Android. PKCE keeps a code the real app's sign-in produced
useless to it, but such an app can start its own sign-in with its own
challenge, and a browser already signed in to Danvics SSO with consent given
completes it without a click (RFC 8252 §8.6). Planned: an App Links /
Universal Links `https://` return (verified with the app's signing key)
instead of the scheme, or a consent page on `/app/start` that names the app;
until then this is a known limit of the scheme.

**Settings.** `OIDC_APP_REDIRECTS` (comma or space separated; default
`soloclef://auth`) and `APP_SESSION_LIFETIME_SECONDS` (default 31536000).
Nothing else changes; the web's sign-in is untouched: a provider's refusal
(`?error=`) that comes back without this browser's sign-in cookie (older than
its 10 minutes, another browser, blocked cookies) still lands on
`/login?error=<the provider's code>` as before; only a callback whose cookie
names an app sign-in goes back to the app.

**Tests.** `tests/unit/auth_oidc/test_app_sign_in.py` (the start's checks, RFC
7636's own S256 example, codes spent once and dead after a minute, the
cookie, `prompt=select_account`), `tests/unit/utils/test_refresh_token_lifetimes.py`
(the two lifetimes, the devices grouping). Run them as `tests/README.md`
says, or sealed: the `soloclef-backend-src` image with this checkout mounted
read-only, `--network none`, pytest installed from downloaded wheels.

## Sign-in options (`app/sign_in_options`, 2026-10-11, not deployed)

`GET /api/v1/sign-in-options` tells anyone, without a sign-in, how this
server can be signed in to. The phone app reads it before anyone signs in:
when someone types another server's address under **Change server**, to
check that it is a Soloclef server at all (`service`: an address that
doesn't answer this "isn't a Soloclef server"), and on the sign-in screen,
to offer only the ways this server takes and to name its single sign-on
(the button, and sentences such as "Danvics SSO can't be reached right
now"). Read-only, public like `/support-contact`, no migration; nothing in
it is secret (no client id, no provider address). The answer here:

```json
{
  "service": "soloclef",
  "password": true,
  "forgot_password": true,
  "sign_up": false,
  "sso": { "label": "Continue with Danvics SSO", "name": "Danvics SSO", "app": true }
}
```

It reports the settings the website's login page is drawn from, read from
the same `docker/.env` (once, when the backend starts, as `app.py` reads
them to mount the routes):

| Field | From | Meaning |
|---|---|---|
| `service` | fixed | Always `"soloclef"`, whatever `BRAND_NAME` says. |
| `password`, `forgot_password` | `AUTH_TYPE` not `GOOGLE`, and `REGISTRATION_ENABLED` | The email-and-password form, and Forgot password beside it. The kill switch off turns every way off. |
| `sign_up` | `NEXT_PUBLIC_SIGNUPS_OPEN` exactly `true` (the web's own switch, `soloclef_web/lib/signups.ts`), with password sign-in on | Off (here): the app offers **Request access** (the website's `/contact`); on: **Create an account** (`/register`). One `docker/.env` line for both: the backend reads it at start (`env_file`), the website build gets it from `docker-compose.yml` (rebuild the frontend after changing it), so the app and the website offer the same. |
| `sso` | `OIDC_ENABLED` and every setting the OIDC sign-in needs (null otherwise: the routes are then not mounted) | `label`: `OIDC_BUTTON_LABEL` (default "Continue with single sign-on"); `name`: the label without "Continue with ", "Sign in with " or "Log in with " ("single sign-on" for any other wording); `app`: whether `soloclef://auth` is in `OIDC_APP_REDIRECTS` (the app hides its button when not). |

**Sign-ups closed, sign-in open (this server).** `REGISTRATION_ENABLED`
cannot close sign-ups: it is the kill switch for every sign-in, so `FALSE`
answers `password`, `forgot_password` and `sign_up` false and `sso` null,
and nobody can sign in. This server keeps it `TRUE`, refuses
`POST /auth/register` and `/api/v1/auth/register` at Caddy (Accounts,
below) and leaves `NEXT_PUBLIC_SIGNUPS_OPEN` unset, so `sign_up` is false
and the app offers **Request access** beside the password form and the SSO.
No backend setting refuses the sign-up itself; the proxy rule does, and the
backend cannot see it, so keep the two in step (`self-hosting.mdx`,
"Closing sign-ups").

**Google cannot be closed at all** (checked 2026-10-11). Google sign-in
makes an account for any Google account the first time it signs in
(`routes/auth_routes.py` `resolve_google_user` -> fastapi-users'
`oauth_callback`, which creates the user): through `/auth/google/callback`
when `AUTH_TYPE=GOOGLE`, and through the desktop app's
`/auth/desktop/session` whenever `GOOGLE_DESKTOP_CLIENT_ID` is set, whatever
`AUTH_TYPE` is (that route has no `registration_allowed`, so the kill switch
does not stop it either). There is no domain or address allow-list, and a
proxy cannot refuse only new accounts (the callback looks the same for
both). `self-hosting.mdx` therefore says: turn Google off (`AUTH_TYPE=LOCAL`,
`GOOGLE_DESKTOP_CLIENT_ID` unset) and use SSO with its own rules, or limit
who Google admits with an **Internal** consent screen (Google Workspace).
This server's `docker/.env` runs `AUTH_TYPE=LOCAL` and sets neither
`GOOGLE_OAUTH_CLIENT_ID` nor `GOOGLE_DESKTOP_CLIENT_ID` (2026-10-11), so
neither way in exists here.

It may be cached for 60 s (`Cache-Control: public, max-age=60`); a change
to `docker/.env` shows after the backend restarts. Tests:
`tests/unit/sign_in_options/test_sign_in_options.py` (this server's setup
among them, and the answer the route builds from the environment).

**Deploy it before the phone app's update that reads it.** Until this
route is live on notes.danvics.com, the app's sign-in screen there falls
back to offering every way under the generic button "Continue with single
sign-on" (soloclef_mobile/docs/auth.md, "The server"). It goes out with the
release of 2026-10-11 ("Deploying the release of 2026-10-11", near the top),
whose checks read this route before the app's update is published.

## AI agents sign in with Authentik (MCP OAuth, `app/agent_sign_in`, 2026-10-09)

claude.ai (and Claude Desktop, mobile and Cowork, which share its connectors)
and ChatGPT connect to the MCP server at `https://notes.danvics.com/mcp` by
**signing in through Authentik**, the MCP authorization spec, because their
custom-connector forms cannot send an API key header. It is what lets a
person use their Soloclef workspace from inside Claude or ChatGPT: search
and read its documents, add notes, run the research scrapers and academic
searches (the MCP tools; not notebooks' chats or Studio). Claude Code,
Cursor, Codex, VS Code and the other coding agents keep using API keys
(`Authorization: Bearer ss_pat_...`), unchanged.

**Flow.** The client posts to `/mcp` without a token and gets `401` with
`WWW-Authenticate: Bearer resource_metadata="https://notes.danvics.com/.well-known/oauth-protected-resource/mcp", scope="openid email profile offline_access"`.
That document (RFC 9728, served by the MCP container; the bundled proxy routes
`/.well-known/oauth-protected-resource*` to it) names one authorization
server, `MCP_OAUTH_ISSUER`. The client reads Authentik's metadata
(`/application/o/soloclef-mcp/.well-known/openid-configuration`, or the RFC
8414 form `/.well-known/oauth-authorization-server/application/o/soloclef-mcp/`),
signs the person in with PKCE S256, and sends the access token on every
`/mcp` request. The MCP server asks the backend's `GET /verify-token` whether
the token is good before letting the request through, so an expired or bad
one gets a `401 invalid_token` the client acts on (it refreshes or signs in
again). The backend checks the token on every call a tool makes:

- RS256 signature against the application's JWKS (asymmetric algorithms
  only; `none` and HMAC never), `iss` = `MCP_OAUTH_ISSUER`, `aud` (and `azp`)
  = `MCP_OAUTH_CLIENT_ID`, `exp`/`iat`/`sub` present, 60 s clock leeway. A
  token Authentik issued to any other application, the web sign-in's
  `soloclef-notes` included, is refused: Authentik ignores RFC 8707 resource
  indicators, so the audience (one client used only for agents) is what ties
  a token to this server.
- **Who may connect**: everyone who may use Notes, that is every Notes
  account Authentik lets into the `notes` application (today `notes-users`,
  or any @danvics.com address except the App Review account through
  `danvics-accounts-only`). Notes adds no group or domain rule of its own.
  Authentik's answer is in the token: the claim `soloclef_notes_access`
  (`NOTES_ACCESS_CLAIM`, `app/agent_sign_in/access_token.py`), made by the
  blueprint's "Soloclef Notes access (agents)" mapping every time Authentik
  issues a token, refreshes included. Only `true` is accepted; `false`, a
  missing claim or any other value is `403 This sign-in may not use
  Soloclef Notes` (no challenge, so the client does not loop). The person
  needs a Notes account first: an @danvics.com address without one signs in
  on the web once (Continue with Danvics SSO creates it, above), since an
  agent sign-in never creates one.
- **Which account**: the same rule as "Continue with Danvics SSO", read-only.
  An identity the web sign-in linked (`oauth_account`, `oauth_name` =
  `OIDC_PROVIDER_NAME`, same `hashed_user_id` subject) acts as that account;
  otherwise the account with the same email, only if `email_verified` is true
  (the "Danvics email profile" mapping asserts it) and that account's own
  address is verified (`is_verified`). So daniel@danvics.com acts
  as daniel's account and danitex@danvics.com as danitex's, an administrator
  through `BILLING_ADMIN_EMAILS` exactly as on the web. No account: `403 No
  Soloclef Notes account matches this sign-in` (no challenge, so the client
  does not loop). An agent sign-in never creates an account or a link.
- **Gated like an API key** (`AuthContext.method = "agent"`, `is_gated`): it
  sees only workspaces with **API key access** on, and every session-only
  action refuses it (creating API keys, adding MCP servers, the Connect your
  agent secrets). Within a workspace it has the person's own role.

**Why the person pastes a client ID and secret.** Authentik 2026.8 has
dynamic client registration, but only with an initial access token
(`goauthentik.io/oidc/dcr` scope, `authentik/providers/oauth2/views/dcr.py`),
which claude.ai and ChatGPT do not have; it has no client ID metadata
documents (CIMD) and does not advertise RFC 9207 issuer identification. So
one confidential client, `soloclef-mcp`, is pre-registered, and each person
pastes its ID and secret into their connector: Claude's **Use your own OAuth
client** (or Advanced settings), ChatGPT's **Advanced OAuth settings**
(developer mode; token method `client_secret_post`). "Connect your agent"
shows both to every signed-in account (`GET /api/v1/agent-sign-in`, session
only), because every account may connect its own Claude or ChatGPT. The
secret alone signs nobody in: Authentik returns codes only to the callbacks
in the blueprint, PKCE is required by both clients, the person logs in
themselves and must approve a consent page naming the application **at every
connect** (the blueprint's own flow, below), and only people Authentik lets
into Notes may use it. The page does not say which Claude or ChatGPT app
asked, so a person who approves a link someone else sent them gives that
person's app access; the docs page tells people to approve only a
connection they started. Exact
per-app ChatGPT callbacks instead of the pattern close even that (see the
blueprint). Covered: claude.ai, Claude Desktop/mobile/Cowork,
ChatGPT apps. Not covered: agents that only do CIMD or DCR (Claude Code and
the like); they use API keys. A DCR shim was ruled out: it would mean Notes
publishing its own authorization-server metadata whose issuer is not the
issuer of the tokens. Do not give the Authentik provider a DCR configuration:
Authentik would advertise `registration_endpoint` and Claude would try it and
fail.

**Authentik side** (not applied by this repository): the blueprint
`custom/authentik/soloclef-mcp.yaml`, the same pattern as
`danvics-notes.yaml`. Provider `soloclef-mcp`, confidential,
`authorization_code` + `refresh_token` only, RS256 (authentik Self-signed
Certificate), subject `hashed_user_id`, claims in the token, access token 1 h,
refresh 30 days, scopes Danvics OpenID/email/profile plus
`offline_access`. Authorization flow `danvics-mcp-authorization`, made by
the blueprint, with one consent stage `danvics-mcp-consent` in mode
`always_require` (named after the 2026-10-09 rename's `danvics-*` rule for
stages and flows; the client ID and slug stay `soloclef-mcp` like every
other client ID). The shared flows do not fit:
`danvics-provider-authorization`, which the other applications use, has no
consent stage, and Authentik's default explicit-consent flow is `expiring`
(4 weeks, `consent_expire_in`) and skips the page while a stored consent is
current. Either would let a link someone else started go through unseen.
`always_require` stores nothing and asks every time the flow runs; refreshes
never run it, so connected agents are not interrupted. Redirect URIs:
`https://claude.ai/api/mcp/auth_callback`,
`https://claude.com/api/mcp/auth_callback`,
`^https://chatgpt\.com/connector/oauth/[A-Za-z0-9_-]+$` (regex: ChatGPT gives
each app its own callback) and `https://chatgpt.com/connector_platform_oauth_redirect`.
Application `soloclef-mcp` ("Soloclef Notes for Claude & ChatGPT", the
Notes icon `app-icons/soloclef-notes.svg`, group "AI & Research", tile opens
`/docs/connect-your-agent`), with the `notes` application's two bindings,
"any" mode: `notes-users` (order 10) and `danvics-accounts-only` (order 20).
They decide who may connect; keep them the same as `notes`'s. The
blueprint's scope mapping "Soloclef Notes access (agents)" (scope `openid`,
no description, so the consent page does not list it) runs Authentik's
policy engine on the `notes` application itself for the person, with the
settings of Authentik's own access check (no cache), and puts the answer in
the token as `soloclef_notes_access`. A missing `notes` application, an
inactive user and the anonymous user give `false`; an error in the mapping
leaves the claim out, which Notes refuses too. Because two mappings now have
scope `openid`, Authentik's discovery document lists `openid` twice in
`scopes_supported`; that is harmless. Every name the blueprint looks up is
the renamed one (`danvics-sign-in`, the "Danvics ..." mappings,
`danvics-accounts-only`). It was applied to a throwaway Authentik 2026.8.3
with stand-ins for those objects (2026-10-09): it applied cleanly, connects
and refreshes gave `true` for a `notes-users` member and for an @danvics.com
address outside the group, `false` for an outsider and the App Review
account, and `false` at the first refresh after taking someone out of
`notes-users`, after putting an @danvics.com address in `app-reviewers`,
and after changing only the `notes` application's bindings; Notes' own
verifier accepted the real tokens that said `true` and refused the others.

**Settings** (`docker/.env`; the backend reads its own through `env_file`;
the `mcp` container has no `env_file` and gets exactly `MCP_OAUTH_ISSUER`,
`SOLOCLEF_PUBLIC_URL`, `SOLOCLEF_MCP_PUBLIC_URL` and `MCP_OAUTH_SCOPES`
through `environment:` in `docker-compose.yml`; anything else put in
`docker/.env` never reaches it):

| Setting | Value here | Meaning |
|---|---|---|
| `MCP_OAUTH_ISSUER` | `https://sso.danvics.com/application/o/soloclef-mcp/` | Exactly as Authentik prints it, trailing slash included. Unset: agent sign-in is off, `/mcp` takes API keys only and its 401 has no challenge. |
| `MCP_OAUTH_CLIENT_ID` | `soloclef-mcp` | Tokens are accepted only for this audience. Backend only. |
| `MCP_OAUTH_CLIENT_SECRET` | (secret) | Only shown in Connect your agent; Notes never uses it. Same value as `MCP_OIDC_CLIENT_SECRET` in `authentik-soloclef/.env`. |
| `MCP_OAUTH_REQUIRED_GROUP` | not set | No longer read (the first version's group check, never deployed). Who may connect is Authentik's `notes` access, above. If it is set, the backend logs `MCP_OAUTH_REQUIRED_GROUP is no longer read` and carries on. |
| `OIDC_PROVIDER_NAME` | default `authentik` | Shared with the web sign-in: where linked identities are looked up. |
| `SOLOCLEF_MCP_PUBLIC_URL` | default `${SOLOCLEF_PUBLIC_URL}/mcp` | The `resource` in the document; must be the URL people paste. MCP container only. |
| `MCP_OAUTH_SCOPES` | default `openid email profile offline_access` | Advertised to clients (space-separated). Keep `openid` (it carries `soloclef_notes_access`; without it every agent is refused), `email` (the account match) and `offline_access` (refresh). MCP container only. |

**Limits and automatic behaviour**

- *A "yes" is remembered for 60 s* by the MCP server (`ACCEPT_CACHE_SECONDS`,
  `soloclef_mcp/mcp_server/core/auth/sign_in_check.py`), or until the token
  expires if sooner; at most 4,096 tokens (`MAX_CACHED_TOKENS`, oldest
  forgotten first). Refusals are not remembered.
- *An access token is good until it expires (at most an hour)*: Notes checks
  its signature and expiry, not Authentik's database, so deleting a sign-in in
  Authentik stops the next refresh, not the token already issued.
- *Authentik's refresh grant never re-checks the application's access
  policy* (`authentik/providers/oauth2/token/refresh_token.py` checks only
  that the refresh token exists, is current and is not revoked), and with
  `refresh_token_threshold` 0 every refresh issues a new 30-day refresh
  token, which also outlives the person's SSO session. So the application's
  bindings only decide who may connect. Scope mappings do run again for
  every refreshed token, so `soloclef_notes_access` says whether Authentik
  lets the person into `notes` at that moment: once their Notes access is
  taken away (for example out of `notes-users` with an address outside
  @danvics.com, or a binding removed from `notes`), the next refresh says
  `false` and the agent is refused (`403`, no sign-in prompt), within an
  hour. Given back, the next refresh says `true` again.
- *Backend check timeout: 10 s* (`CHECK_TIMEOUT_SECONDS`). Only a `401`
  from the backend becomes `401 invalid_token`; unreachable, or any other
  answer (a 5xx, a 404), gives `503 temporarily_unavailable`, not a sign-in
  loop.
- *Authentik's discovery and keys are cached for an hour* (the web sign-in's
  `ProviderDirectory`; a failed refresh is not served from the old copy). When
  the cache runs out while sso.danvics.com is down, or its key set cannot be
  read, the backend answers `503` (`AgentProviderUnavailableError`), not
  `401`: tokens that may be good are not thrown away, and agents carry on
  once Authentik is back. A token with an unknown key id re-reads the keys at
  most once a minute (`KEY_REFRESH_COOLDOWN_SECONDS`,
  `app/agent_sign_in/access_token.py`), so forged key ids cannot turn into a
  stream of requests to sso.danvics.com.
- *Clock leeway: 60 s* (`LEEWAY_SECONDS`).
- *Token lifetimes are Authentik's*: access 1 h (Claude refreshes up to five
  minutes before expiry), refresh 30 days.
- Per-caller state on the MCP server (the chosen workspace) is keyed by the
  account (`user:<id>` from `/verify-token`), not the token, so it survives a
  refresh; it is lost when the `mcp` container restarts, as for API keys.
  Every signed-in agent of one person (Claude web, Desktop, ChatGPT, every
  conversation) shares that one choice; `docs/connect-your-agent.mdx` says so.
  At most 2048 callers are remembered (`_MAX_TRACKED_IDENTITIES` in
  `soloclef_mcp/mcp_server/core/workspace_context.py`); past that the
  choice made longest ago is dropped, as if the server had restarted.

**Cut an agent off** (any one of these):

- *At once*: deactivate the person's Notes account; every agent call is
  refused from the next request (`403`), whatever token it holds. There is
  no page for it: `docker exec soloclef-db-1 psql -U surfsense -d surfsense
  -c "UPDATE \"user\" SET is_active = false WHERE email = '...'"` (back the
  database up first; `true` undoes it). Deleting the account (the person's
  own Settings) does the same.
- *Within an hour*: take away the person's Notes access in Authentik. A
  `notes-users` member whose address is outside @danvics.com loses it when
  taken out of the group; an @danvics.com address keeps it through
  `danvics-accounts-only` whatever its groups, so for one of those use the
  Notes account (above) or one of the next two. Or in Authentik, Directory
  → Users → the person → **OAuth Refresh Tokens**, delete the tokens for
  "Danvics Soloclef Notes for AI agents" (no more refreshes), or deactivate
  the Authentik user (Authentik then deletes all their access and refresh
  tokens). The access token already
  issued keeps working until it expires: Notes checks its signature, not
  Authentik's database.
- The person can also remove the connector in Claude or ChatGPT.

**Turn it off**: unset `MCP_OAUTH_ISSUER`, then after the deploy gate
`up -d backend mcp`.

## MCP servers people add themselves (`app/mcp_security/server_origin.py`, 2026-10-09)

A workspace **Owner or Editor** (the `connectors:create` permission) can add
an MCP server to that workspace with **their own key**, on their own bill,
from Connectors → MCPs; no administrator needed. Only in a signed-in session:
API keys and agent sign-ins cannot add servers. Viewers cannot. Editing stays
with the person who added it or an administrator. Deleting: the Owner
(`connectors:delete`) deletes any server; the person who added a `member`
server deletes their own while they are a member, whatever their role (the
Editor role has no `connectors:delete`), so they can always stop their key
being spent (`_require_mcp_remover`).

**When the adder's membership ends** (they leave, or are removed), their
`member` servers in that workspace are deleted in the same transaction and
the workspace's cached MCP tools dropped (`app/mcp_security/member_servers.py`,
called from `leave_workspace` and `remove_member` in `rbac_routes.py`), so
nobody keeps using a former member's key. A refused leave (their only
workspace) deletes nothing. `site` servers, and every row from before
2026-10-09, stay. Deleting an account deletes all its servers anyway
(`user_id` cascade). A role change keeps the servers: the person is still a
member and can delete them.

**Two kinds**, stored by the server as `config.server_origin` when the row is
created (never taken from a request; rows from before 2026-10-09 have none
and count as `site`):

- `member`: added by anyone who is not a site administrator. Any host that is
  `https`, on the default port 443, named by a public domain (not an IP
  address; no single-label name such as `litellm` or `backend`; not
  `.local`, `.localhost`, `.internal`, `.lan` or `.home.arpa`), without
  credentials in the URL, and whose every address is public (no private,
  loopback, link-local, CGNAT/tailnet, Docker or metadata range). The
  connection may reach that host only.
- `site`: added by an administrator (`BILLING_ADMIN_EMAILS` or superuser):
  the services the site provides on its own keys (Exa, Tavily, Browserbase).
  Same checks, and the host must also be on the allow-list (`billing_settings`
  row `mcp_allowed_hosts`, default the three hosts; edited with
  `GET/PUT /api/v1/admin/mcp/allowed-hosts`; cached 60 s at call time).

Both are checked when saved, tested, discovered and on every tool call; each
connection resolves the name itself, connects to the address it just checked
(DNS-rebinding safe), follows no redirects and ignores proxy settings
(`url_guard.py`). Keys are write-only: every reader sees `••••` plus the last
four characters (values of 12+ characters), and a masked value sent back is
kept only for the same host. Costs fall on the key in the server's config; a
workspace's own Exa or Tavily key (oldest such row) is also what its research
web search uses. Limits unchanged: 10 connection tests a minute per person
(`_MCP_TEST_LIMIT`), 30 s discovery timeout, tool list cached 5 minutes,
every tool asks before it runs unless trusted, and URL-opening tools always
ask.

## Billing — plans, credits, card and crypto (`app/billing`, `c0009_billing_plans`)

Soloclef Notes sells plans plus credits. Everything a person sees or pays is
in the database and edited on the billing admin pages (User settings ->
Billing -> Admin, shown to superusers and `BILLING_ADMIN_EMAILS`); the
defaults in `app/billing/defaults.py` are only what the migration seeded.

**Workspace-owner billing** (`c0019_workspace_payer_billing`): a member's
metered action inside someone else's workspace spends that workspace's
*owner's* credits, not their own, resolved once in
`app.billing.meter._resolve_payer` and applied everywhere `workspace_id` is
already threaded (it was, almost everywhere, before this). Uncapped per
member, no UI anywhere (the owner didn't want either). See
`custom/WORKSPACE_CREDITS.md` for the full design and what's still a gap
(research-link ingestion). The other gap it used to list, the separate
`wallet_credit.py` scrape/crawl wallet, closed in `c0021`: web crawl and the
platform scrapers (Reddit/SERP/Maps/YouTube/Amazon/Instagram/TikTok/Indeed/
Walmart) now go through `app.billing.meter` like everything else, one
`billing_action_price` row per platform/kind — see `custom/WORKSPACE_CREDITS.md`.

**Unit.** Every amount is integer micro-USD of retail value; a credit is
10,000 micros (1 credit = 1 US cent). Prices and grants are shown in credits.

**Wallet.** Two buckets: *plan credits* (`billing_accounts.plan_credits_micros`,
the plan's monthly allowance, granted at each period start; what is left
expires — no rollover) and *top-up* (`user.credit_micros_balance`: packs,
signup bonus, incentives, admin grants; never expires). Spending takes plan
credits first. Every change goes through `ledger.post`, which writes a
`billing_ledger` row with the bucket balance after it; `(kind, reference)` is
unique, so a replayed grant is a no-op. `billing.check_ledger` (daily) logs
any account whose balances differ from its ledger. All wallet functions lock
the user and account rows first, so parallel reserves serialize.

**Meter.** `app/billing/meter.py`: `begin` (check the plan, hold the price) ->
`settle` (debit) or `release` (give back; refunds if already settled).
Modes (admin setting `mode`, env default `BILLING_MODE`): `off` (nothing
recorded), `shadow` (everything recorded in `billing_usage` with what it would
cost and why live would refuse it; nothing refused or charged), `live`
(enforced; refusals are HTTP 402 with `error.code`). Exempt accounts (admins,
or the per-account switch) are recorded as `exempt` and never refused.

**Gates.** Chat turn (`premium_quota.py`: `chat_standard` / `chat_premium` by
the model's tier, set per model id on the admin page), uploads (parser and
vision checked at the route; pages checked before parsing and charged after,
`EtlCreditService`), research imports (Cloud parsing only if the plan has it,
otherwise local), vision (`QuotaCheckedVisionLLM`, per image, billed to the
uploader), chat image generation, Studio start (`notebooks/routes/studio.py`
and the chat video tool: kind, running-jobs limit, and the plan's video cap
passed to the executor as `max_seconds`); the hold is named after the job and
settled when the job is ready, released when it fails or is cancelled
(`deliverables/jobs/service.py`, `podcasts/service.py`). Calls inside an
already-charged output are audited in `token_usage` but not charged again.

**Stripe** (`app/billing/stripe_billing`): Checkout for plans (subscriptions,
monthly Price created on demand per plan price) and packs (one-off, amount from
the pack row), Customer Portal, plan change (prorated) and cancel at period
end. Webhook `POST /api/v1/billing/stripe/webhook`: signature (5-minute
tolerance) plus event-id idempotency (`billing_webhook_events`). Plan credits
are granted by `invoice.paid` (create/cycle), keyed by invoice id. Test vs live
is only the key (`sk_test_`/`sk_live_`); ids the code keeps are re-created on
demand when the key cannot see them.

**Crypto** (`app/billing/crypto`): a provider interface; the implementation is
the owner's BTCPay Server (Greenfield API, https://pay.danvics.com): Bitcoin
(`BTC-CHAIN`) and USDT on Tron and Polygon (`USDT-TRON`, `USDT-POLYGON`, the
Tether "USDt" plugin), limited through `checkout.paymentMethods`. It buys packs
and prepaid plan months (1/3/12, admin discounts) — no crypto subscriptions.
The app creates the invoice (amount from the database, USD) and sends the buyer
to BTCPay's checkout. Webhook `POST /api/v1/billing/crypto/webhook` checks
`BTCPay-Sig` (HMAC-SHA256 of the raw body with the webhook secret), records the
(original) delivery id once, then re-reads the invoice from BTCPay before
acting: Settled grants, Expired+PaidPartial is `partial` (payable on BTCPay's
page), Expired/Invalid close it. BTCPay's store tolerance and speed policy
decide when an invoice is paid. When BTCPay has no free watch-only address the
buyer is told crypto is busy (HTTP 503 `CRYPTO_BUSY`).
`billing.reconcile_payments` (every 10 min) polls missed Stripe checkouts and
BTCPay invoices; `billing.release_stale_holds` (15 min) frees holds a crash
left; `billing.roll_periods` (hourly) renews non-Stripe periods.

Upstream's desktop-licence sales (`app/license`) and the old `/stripe/*` credit
routes are no longer mounted.

**Which methods people see** (`app/billing/payment_methods.py`, since
2026-09-25). A method is offered only when it is switched on *and* fully set
up: card needs the Stripe secret key and webhook signing secret; crypto needs
the BTCPay URL, store id, API key and webhook secret. Otherwise it is
"Pending" on the admin page (Mode & payments shows Active / Pending / Switched
off and what is still missing), and people see nothing of it: `/billing/me`
and `/billing/public/catalog` (`payments: {card, crypto, cryptoMonthOptions}`)
report it off, so the Billing page hides its buttons, the portal link and its
copy, and the public pricing page neither names it nor enables "Choose"/"Buy"
(those also need mode `live`). With no method ready the Billing page shows
"Payments aren't available yet" and no Upgrade or Buy buttons at all.

State on 2026-09-25: mode `live`; Stripe not set up (no keys), so card is
pending; BTCPay not finished (no store id, API key or webhook secret; crypto
switched off), so nothing is offered yet. The launch is crypto only: plans are
bought as prepaid 1, 3 or 12 months and packs as one payment, and the Billing
page, pricing page, docs and privacy page say so. Turning Stripe on later
needs nothing but its keys and the switch; card copy and buttons come back by
themselves.

**BTCPay connected 2026-10-01.** Store "Soloclef store" (store id ends
`...RETSC`) on the owner's own BTCPay Server (pay.danvics.com). The API key
is scoped to create/view/modify invoices, store settings and webhooks only —
no wallet or payout permissions, so it can never move funds. Webhook at
`https://notes.danvics.com/api/v1/billing/crypto/webhook`, verified with a
$1 test invoice (Settled, then marked Invalid once confirmed working — not a
real sale). Store id, API key and webhook secret are in the admin page (Mode
& payments), not here. Crypto payments are not yet switched on pending the
owner's go-ahead (see the checklist above).

### Going live (checklist)

1. Look at shadow data first: Billing -> Admin -> Report (what each action
   would have cost and refused, with provider cost beside it).
2. Stripe: create the account; in test mode copy the secret key (`sk_test_...`);
   add a webhook endpoint `https://notes.danvics.com/api/v1/billing/stripe/webhook`
   with events `checkout.session.completed`, `checkout.session.async_payment_succeeded`,
   `checkout.session.async_payment_failed`, `checkout.session.expired`,
   `invoice.paid`, `invoice.payment_failed`, `customer.subscription.updated`,
   `customer.subscription.deleted`; copy its signing secret (`whsec_...`).
   Enable the Customer Portal (Settings -> Billing -> Customer portal). Put the
   keys in the admin page (Mode & payments) and switch "Card payments" on. For
   live, repeat with the live key and a live webhook endpoint; nothing else.
3. BTCPay (https://pay.danvics.com): register, create the store "Soloclef
   Notes", connect your own wallets (BTC, and the USDt plugin's Tron and
   Polygon watch-only address lists), set the speed policy. Create a
   store-scoped API key with "view invoices", "create invoice" and "modify
   invoices"; add a webhook (Store -> Webhooks) to
   `https://notes.danvics.com/api/v1/billing/crypto/webhook` with all invoice
   events and copy its secret. In the admin page enter the store id, API key
   and webhook secret, tick the payment methods, switch "Crypto payments" on.
4. Mark model tiers (Admin -> Model tiers), check plans, prices, packs.
5. Switch mode to `live`. Admins (`BILLING_ADMIN_EMAILS`) are exempt; since
   2026-09-25 that is only `danitex@danvics.com`, and the owner's daily
   account `daniel@danvics.com` is an ordinary Free-plan account.
6. Other people need models: done on 2026-09-25, see "Shared models" below.

## Report Issue address and the Connected repo page (2026-09-27)

"Report Issue" (error toasts and error pages) and the public contact page mail
the support address. An admin changes it on
User settings -> Billing -> Admin -> Settings ("Report Issue email"); it is the
`support_email` key in `billing_settings`, read by anyone from
`GET /api/v1/support-contact` (`app/support_contact`). Only one plain address is
accepted. Since 2026-10-11 there is no built-in default: unsaved, it is
`SUPPORT_EMAIL` from `docker/.env` (here `hello@danvics.com`), else none
(`{"email": ""}`; see "This server's own settings" above). The website build
gets the same line as `NEXT_PUBLIC_SUPPORT_EMAIL`, used until the setting loads
and in the site's JSON-LD.

Workspace settings -> Connected repo is removed: git storage is off. The old
address redirects to General; the backend (`app/knowledge_store/remote`) and
the `GitRemoteSettings` component stay in the tree, unused.

## Accounts: admin, password reset, Turnstile (2026-09-25)

**Two accounts.** `danitex@danvics.com` is the admin: an ordinary user (not a
superuser) listed in `BILLING_ADMIN_EMAILS`, so it sees Billing -> Admin and
is never charged. `daniel@danvics.com` is the owner's daily account and is
treated exactly like any customer (Free plan, charged, no admin pages).
Registration stays closed at Caddy (`POST /auth/register` and
`/api/v1/auth/register` -> 403, the `@signup` rule in the notes.danvics.com
block of `/etc/caddy/Caddyfile`), so accounts are created with

    docker exec soloclef-backend-1 python scripts/create_account.py someone@example.com

which goes through the same `UserManager.create` as a sign-up (hashed
password, verified, a workspace on the shared defaults, prompts, welcome
credit) with a random password nobody sees; the person sets theirs with
"Forgot password?". Admin rights only ever come from `BILLING_ADMIN_EMAILS`,
and only to an account whose address is verified (`is_verified`, since the
2026-10-11 follow-up; `ledger.is_listed_admin`): `create_account.py` and SSO
make verified accounts, the sign-up form unverified ones.
`REGISTRATION_ENABLED` must stay `TRUE`: despite its name it is the kill
switch for every sign-in (`app.py` `registration_allowed`), so `FALSE` would
close sign-ups only by locking everyone out. The website and the app offer
**Request access** because `NEXT_PUBLIC_SIGNUPS_OPEN` is unset; opening
sign-ups means setting it to `true`, rebuilding the frontend, restarting the
backend, and removing the Caddy rule, together.

**Password reset.** Sign-in -> "Forgot password?" (`/forgot-password`) ->
email -> `/reset-password#token=...`. The backend is fastapi-users'
`/auth/forgot-password` and `/auth/reset-password`; the email is
`app/password_reset` over the existing SMTP settings (`SMTP_*` in
`docker/.env`; SMTP2GO, same host/port/user as Ped AI), from
`Soloclef Notes <SMTP_FROM>` (override with `SMTP_ACCOUNT_FROM`). Tokens are
signed, bound to the current password hash (so a used link is dead) and live
`PASSWORD_RESET_TOKEN_LIFETIME_SECONDS` (1800). Same 202 whether or not the
address exists; the mail is sent after the response; per IP 3 requests and 5
new-password attempts a minute, per address `PASSWORD_RESET_EMAILS_PER_HOUR`
(3) mails an hour; a reset signs out every other session. The token is never
logged. New passwords: 8-256 characters, not the email.

**Turnstile** (Cloudflare) guards sign-in (`/auth/jwt/login`), registration
and both reset routes: the web renders the widget when `TURNSTILE_SITE_KEY` is
set, sends the token as `X-Turnstile-Token`, and the backend verifies it with
siteverify when `TURNSTILE_SECRET_KEY` is set (fails closed: 503 if Cloudflare
cannot be asked). Both empty = off, which is how it ships until a widget
exists. The desktop and mobile apps sign in through `/auth/desktop/login`,
which has no widget and stays on its 5/minute limit.

To turn it on: Cloudflare dashboard -> Turnstile -> Add widget, name
"Soloclef Notes", hostname `notes.danvics.com`, mode Managed; copy the site
key and secret key into `docker/.env` as `TURNSTILE_SITE_KEY=` and
`TURNSTILE_SECRET_KEY=` (back the file up first), then, after the deploy gate,
`docker compose ... up -d backend celery_worker celery_beat frontend`. (Caddy's
Cloudflare token has DNS rights only, so this cannot be done from here.) For
tests, Cloudflare's always-pass keys are site `1x00000000000000000000AA`,
secret `1x0000000000000000000000000000000AA` (the E2E stack uses them).

## Nextcloud: one-click Connect (Login Flow v2, 2026-09-26)

Nextcloud is a native connector like Google Drive. In Connectors it sits
right after Google Drive as "Connect your Nextcloud (cloud.danvics.com)", and
a notebook's Add sources -> From a connector offers the same Connect, then
the file picker. Nobody types a server, user or password any more.

**Why Login Flow v2, not a Nextcloud OAuth2 client.** Login Flow v2 is how
Nextcloud's own desktop and mobile apps get an app password: no admin setup,
it works with the Authentik SSO login (the person just signs in on
cloud.danvics.com), and it yields exactly what WebDAV needs. An OAuth2 client
would need registering in Nextcloud admin and a token refresh path, and
Nextcloud's OAuth2 tokens are app passwords underneath anyway.

**How it runs** (`app/connectors/nextcloud/login_flow/`):

1. `POST /api/v1/workspaces/{id}/nextcloud/login` (a session, not a PAT; the
   `connectors:create` permission). The backend POSTs
   `{NEXTCLOUD_BASE_URL}/index.php/login/v2`, keeps the poll token in Redis
   (`soloclef:nextcloud:login-flow:<flow id>`, encrypted with `SECRET_KEY`,
   20-minute TTL like Nextcloud's own, bound to the user and workspace) and
   returns only a random flow id and the login URL.
2. The page shows "Sign in to cloud.danvics.com", a plain new-tab link. The
   person signs in and clicks Grant access there.
3. The page polls `POST .../login/{flow id}/poll` every 2 s (5 s after two
   minutes); each call polls Nextcloud once. A flow id belongs to whoever
   started it, in that workspace: anyone else gets 404.
4. When Nextcloud hands over `loginName` + `appPassword`, the backend asks
   OCS (`/ocs/v2.php/cloud/user`) for the user id and display name, proves the
   login with a WebDAV PROPFIND of the files root, and saves it encrypted in
   the person's own `NEXTCLOUD_CONNECTOR` row (`auth_method: login_flow`,
   `username` = user id for WebDAV paths, `login_name` for Basic auth). If the
   check fails the new app password is revoked and nothing is saved.

Limits per user: 10 new sign-ins per 10 minutes
(`NEXTCLOUD_LOGIN_START_LIMIT`), 90 polls a minute. The poll token and the app
password are never logged or sent to the browser.

**Reconnect** runs the same flow; the login replaces the one in the same row
(imported files stay linked, refresh finds them by account) and the old app
password is revoked. **Disconnect** (`DELETE
/api/v1/connectors/{id}/nextcloud/connection`, the owner, or anyone with
`connectors:delete`; the Connectors page and the dialog use it) revokes the app
password with `DELETE /ocs/v2.php/core/apppassword` and deletes the row;
imported documents stay. Deleting the connector through the generic route
revokes too. In Nextcloud each connection shows as a "Soloclef Notes" device
under Settings -> Security.

**The server is admin config.** `NEXTCLOUD_BASE_URL=https://cloud.danvics.com`
in `docker/.env`; change it there and restart the backend and workers. Unset,
the Connect card hides and the API answers 503. A stored login for any other
server is refused ("needs reconnect"), and the generic connector routes no
longer accept a Nextcloud config: create is refused and an update keeps the
stored login (a rename only).

**The owner's older row** (workspace 1, "Nextcloud files (picker)", saved from
a pasted app password for `ifedan`) keeps working unchanged: it names
cloud.danvics.com, so it is used as is. Clicking Reconnect once upgrades it in
place to a Login Flow app password and retires the pasted one.

**Tests.** Backend: `tests/unit/connectors/nextcloud/login_flow/` and
`tests/integration/connectors/nextcloud/test_login_flow_routes.py` (Nextcloud
faked at the HTTP boundary by `fake_login_server.py`, real Postgres);
`test_login_state_in_redis.py` runs against `TEST_REDIS_URL`. Web:
`tests/unit/nextcloud-connect/` and the Playwright journey
`tests/app/nextcloud-connect.spec.ts`, which runs against the e2e stack's
stand-in Nextcloud (`tests/e2e/fakes/nextcloud_server.py`, service
`nextcloud-stub`, its sign-in page proxied at `/__nextcloud__/`).

**The nextcloud-surfsense sidecar is retired.** The container that synced a
Nextcloud folder into Soloclef was removed on 2026-09-26; its files remain in
`/home/danvics/docker/nextcloud-surfsense` for reference only and nothing uses
them. The picker plus Connect replaces it. The separate "Nextcloud (Open
Notebook folder)" MCP connector that workspace 1 once had is gone too
(connector 1 no longer exists, checked 2026-10-09).

## Connectors: only what works; Google Drive on `drive.file` (`c0012_drop_removed_connectors`, 2026-09-26)

**Removed, not hidden.** The owner's rule: a connector either works here or
is gone. Deleted from the code (modules, routes, indexers, Celery tasks,
settings, catalog, forms, icons, translations, tests): Notion, Slack, Discord,
the GitHub connector, Linear, Jira, Confluence, ClickUp, Airtable, OneDrive,
Teams, Dropbox, Composio, BookStack, Elasticsearch, Luma, Circleback,
Obsidian, Gmail, Google Calendar, and the retired search-API connector types
(Serper, Tavily, SearXNG, Linkup, Baidu), plus the hosted-MCP OAuth flow that
served the branded ones. Kept: Google Drive, Nextcloud, MCP servers (Exa,
Tavily and Browserbase are ordinary MCP rows), the web
crawler, and web search (bundled SearXNG, Google Search scraper). Workspace
git remotes (a GitHub App) are not a connector and stay. None of the removed
types had a row on the live database.

`c0012_drop_removed_connectors` rebuilds `searchsourceconnectortype` and
`documenttype` with the kept values only. It refuses to run (and changes
nothing) if any row still uses a removed value. The Obsidian plugin's two
partial indexes go first; `documents.document_type` is in the Zero
publication's column list, so both tables leave the publication for the type
change and `apply_publication` restores its canonical shape in the same
transaction (zero-cache may resync once). Tested against a schema dump of
live: upgrade, downgrade, upgrade, publication identical. `LOCAL_FOLDER_FILE`
stays: the Upload tab's folder picker creates it.

**Why Gmail and Calendar went too.** Gmail's read scopes are *restricted*:
a public app needs Google's yearly CASA security assessment. Calendar's are
*sensitive*: listing any of them makes the whole consent screen need Google's
scope verification (justification and a demo video), where Drive on
`drive.file` alone needs none. Neither is a research source worth that for a
notebook app.

**Google Drive.** One scope, `https://www.googleapis.com/auth/drive.file`
(non-sensitive). The app sees only files the person picks in the Google
Picker (`setAppId` grants each pick to the app) or that it created. Picking a
folder would grant the folder but not its files, so the Picker offers files
(folders can be opened, not picked). The account's address comes from Drive's
`about` resource, so no profile scope. `drive.readonly` was not an option: it
is a restricted scope (CASA), as is `drive`.

- Connect (Connectors card, or Add sources -> From a connector) goes to
  Google's consent page and back to the same page; the grant (refresh token,
  access token, client secret) is stored encrypted in the person's own
  connector row; connecting again reuses the row and revokes the old grant.
  Code: `app/connectors/google_drive/connect/`.
- Add sources -> "Choose files from Google Drive" opens the Picker with a
  short-lived access token (owner only), then
  `POST /connectors/{id}/google-drive/import` creates pending rows linked to
  the notebook at once and queues the Drive indexer, which downloads (Google
  Docs/Sheets/Slides are exported), parses through the ETL pipeline and
  charges the billing meter per page. Picks are remembered on the connector
  (`selected_files`); the daily periodic sync re-reads only files whose md5 or
  modified time changed. Code: `app/connectors/google_drive/notebook_import/`.
- Disconnect (or deleting the connector) revokes the grant at Google;
  imported files stay in the library and their notebooks.
- Server settings: `GOOGLE_OAUTH_CLIENT_ID`/`SECRET` (a Google "Web
  application" client), `GOOGLE_DRIVE_REDIRECT_URI`, `GOOGLE_PICKER_API_KEY`;
  `GOOGLE_PICKER_APP_ID` defaults to the client ID's project number. Until
  all are set the card says "not set up on this server yet".
- Tests: `tests/integration/connectors/google_drive/` against the stand-in
  Google (`tests/e2e/fakes/google_server.py`, served in-process; real OAuth
  library, googleapiclient and token refresh), and the web e2e
  `tests/app/google-drive-connect.spec.ts` (stand-in Google service
  `google-stub`, and a stand-in Picker served in place of
  `apis.google.com/js/api.js`). The `GOOGLE_OAUTH_*_URI` and
  `GOOGLE_DRIVE_API_ENDPOINT` overrides exist only for that stand-in.

## Notifications: Delete and Clear all (`c0028_announcement_cleared_reads`, 2026-10-10, not deployed)

Daniel: "no way to clear notifications or inbox in soloclef". The inbox only
had list, counts and mark read; now a notification can be deleted, and a
view cleared.

- `DELETE /api/v1/notifications/{id}`: deletes one of the caller's
  notifications (`{success, message}`); another account's, or one already
  gone, is a 404.
- `DELETE /api/v1/notifications`: deletes every notification the list
  (`GET /api/v1/notifications`) shows for the same filters: `workspace_id`
  (that workspace's and the ones with no workspace), `type`, `category`,
  `source_type`, `filter` (`unread`, `errors`), `search`, and the list's
  own hiding of expired announcements; `{success, message, deleted_count}`.
  No filters clears the caller's whole inbox. Unlike the list, a filter it
  does not know is a 422, never ignored, so a typo cannot widen what goes.
  Both share one WHERE clause (`app/notifications/api/scope.py`). No cap: it
  is one `DELETE … RETURNING`.
- A running job's notification (metadata `status` `in_progress`: an upload
  being read, a connector syncing) is never deleted: the job still holds the
  row and updates it as it goes, and an UPDATE of a deleted row fails the
  job (an upload that indexed fine would be marked failed, a Drive sync
  would never record its time). Clear all leaves it in the inbox, and
  `DELETE /{id}` on it is a 409; the website and the app offer no Delete on
  it. It can go once the job has finished, or once
  `cleanup_stale_indexing_notifications` (every 5 minutes) has marked a job
  that died as failed (`deletion.py`, `JOB_RUNNING`).
- Only the caller's rows are touched. An announcement is one row per
  recipient, so deleting yours leaves the announcement and everyone else's
  copy. A read copy deleted is added to
  `site_announcements.cleared_read_count`, so the Sent list's "read by N of
  M" does not drop when a reader tidies up (an unread copy deleted is not
  counted as read).
- Rows are deleted outright and `notifications` is in the Zero publication,
  so the delete reaches every open tab: the web's inbox drops a row Zero's
  complete result no longer has (`hooks/use-inbox.ts`). The phone app reads
  the inbox over REST and sees it at its next refresh (within a minute).
- The website: each item's ⋯ menu has Delete (with a mouse it shows on hover
  or focus; on a touch screen, phone or iPad, always); "Clear all" sits next
  to "Mark all read" and asks in the panel's header first ("Clear all mentions?"),
  clearing the open tab (on Unread, only the unread ones). The web's status
  inbox also takes credits notices again (`insufficient_credits`), as the
  backend's status category always did; before, they vanished from the
  panel once Zero synced.

**Migration:** `c0028_announcement_cleared_reads` adds
`site_announcements.cleared_read_count INTEGER NOT NULL DEFAULT 0` (a
catalogue-only change on PG 11+, `lock_timeout` 5 s: run it again if it
times out). Not in the Zero publication. Take the usual `pg_dump -Fc` into
`docker/backups/` first.

**Deploy:** both backend images and the frontend (all changed), the
`pg_dump`, then the gate, `python3 soloclef_backend/scripts/deploy_gate.py
--wait`, before `up -d backend celery_worker celery_beat frontend` (the
`migrations` service runs `alembic upgrade head` first: the backend waits
on it). The phone app's half is
on `fix/app-inbox-clear` (an update, not a new build) and should go out after
the backend, since its Delete and Clear all call these routes.

**Tests:** `tests/integration/notifications/test_inbox_clearing.py` (one,
another account's, twice; a whole view, a tab, Unread, a workspace and the
global rows, the same rows as the list, expired announcements kept, unknown
filters refused; a running job kept by Clear all and its next update
landing, a 409 for it alone; an announcement left with the others and its
read count kept), `tests/integration/site_announcements/test_cleared_reads_migration.py`,
the web's `tests/unit/inbox-clearing/` and `tests/app/notifications-clear.spec.ts`
(Delete and Clear all in the panel, desktop and phone, and the delete
reaching a second open tab through Zero; a running job's item with no ⋯ and
kept by Clear all; the ⋯ shown with no hover on a touch screen as wide as an
iPad; it seeds through the test-only `POST /__e2e__/notifications`,
`tests/e2e/notifications.py`, whose `running` rows get a 10-minute Redis
heartbeat so the stale-job cleanup leaves them alone during a test).

## The phone app's browser hand-off and its Drive Picker (`app/app_handoff`, 2026-10-10, not deployed)

Plan items B2 and B6 of the Soloclef phone app (written on
`feat/sn-backend-sources`, merged with the connector sign-ins below into one
`app/app_handoff` on `feat/sn-backend`: one code store, one pair of routes,
three purposes). Google's Picker only runs on a web page, in a
browser; the app cannot put its session in the phone's browser sheet, and
no token may travel in a URL. So:

1. `POST /api/v1/app/handoff {purpose: "drive_picker", workspace_id, notebook_id?}`
   with the app's bearer token returns `{code, expires_in: 60, path}`, the
   path ending `?for=drive_picker` (not a secret: it only tells a spent code's
   page which answer to give). It
   checks first what the import will need (workspace access,
   `documents:create`, the notebook's `chats:update`, the person's own Drive
   connection, the server's Drive settings), so the app hears
   `google_drive_not_connected` (404), `google_drive_reconnect` (400) or
   `google_drive_not_configured` (503) before any browser opens.
2. The app opens `https://<server>/api/v1/app/handoff/{code}` in the browser
   sheet. The page spends the code (once; a second open says "This link has
   run out", 410), takes a fresh Picker token for the person's own grant (as
   `…/google-drive/picker-session` does), and runs Google's Picker at once
   (the web's settings: files only, several at once, shared drives, at most
   50).
3. The picks go to `POST /api/v1/app/handoff/drive-picker/import {ticket, files}`.
   The ticket is in the page's body only (10 minutes, one import) and stands
   for the person, the workspace, the notebook and the Drive connection; the
   import is the web's own `import_picked_files`, so the files become pending
   notebook sources and the Drive indexer reads them.
4. The page goes back to `soloclef://drive/done?count=N&unsupported=M`,
   `?error=cancelled`, `?error=google_drive_reconnect` (also when Google
   stopped accepting the grant between the code and the page), or says what
   went wrong with Back to the app (`?error=picker_failed` /
   `import_failed`); Try again is offered only when the Picker itself failed,
   since a failed import has spent the page's ticket.

**Limits:** a code lives 60 s (`CODE_LIFETIME_SECONDS`) and opens one page; a
ticket lives 600 s (`TICKET_LIFETIME_SECONDS`) and makes one import; both are
random, stored only as their SHA-256 in Redis (`soloclef:app_handoff:code:…`,
`…:ticket:…`, and a Microsoft or Drive connect hand-off's `…:completion:…`,
5 minutes, one save; this worker's memory if Redis is down, with a warning). 20 hand-offs,
30 pages, 30 imports and 20 completions a minute from one address. Each
purpose returns to a fixed app address; the request cannot name one.

**The page:** `Cache-Control: no-store`, `X-Frame-Options: DENY`,
`Referrer-Policy: strict-origin` (Google checks the Picker's API key against
the origin; the path, which names the spent code, never leaves), and a CSP
whose scripts must carry the page's nonce (`'strict-dynamic'` lets Google's
loader bring the Picker's own scripts). The page is served from the API's
origin, which on notes.danvics.com is the website's, so a
referrer-restricted `GOOGLE_PICKER_API_KEY` that allows the website allows it.

**Purposes:** `drive_picker` (here), `drive_connect` and `microsoft_mail`
(the next section); anything else is refused with
`HANDOFF_PURPOSE_UNSUPPORTED` (400). Spike S9 (does Google's Picker work in
the iPhone's and Android's browser sheet?) needs a phone; if it fails, the
app's "Import from Drive on the website" stays the way in.

**Tests:** `tests/unit/app_handoff/` (codes and tickets spent once and gone
after their lifetime, stored hashed, Redis down; the page's JSON can't close
its script, nonce-only scripts, the headers; the purposes' return
addresses) and `tests/integration/app_handoff/` (the whole round trip on the
stand-in Google: code, page, ticket, import into the notebook, each spent
once; a purpose the server doesn't know; no connection; another workspace's
notebook; a grant Google stopped accepting).

## Email (IMAP): Microsoft accounts through Microsoft's sign-in (2026-10-09)

Outlook.com, Hotmail, Live and Microsoft 365 mailboxes can't sign in to IMAP
with a password any more, so the Email (IMAP) card offers **Connect a
Microsoft account** beside **Add account**: Microsoft's sign-in and consent
page (OAuth 2.0 authorization code + PKCE, tenant `common`), then back to the
Connectors page with the mailbox listed as "address · Microsoft". IMAP then
signs in to `outlook.office365.com:993` with XOAUTH2. Reading stays
read-only. The tokens are stored encrypted (`SECRET_KEY`) in the person's own
Email (IMAP) row and refreshed by themselves; when Microsoft stops accepting
one, the row shows **Reconnect**. No route returns a mailbox's password or
token, even encrypted, and only the Email (IMAP) and Microsoft routes write
mailboxes: the generic connector routes mask them, refuse them on create and
keep the stored ones on update (password accounts too). Code:
`app/connectors/email_imap/microsoft/`;
design, every limit and automatic behaviour, and the full Entra steps:
`custom/docs/email-imap-connector.md` ("Microsoft accounts").

Off until the owner registers an app in Microsoft Entra: without
`MICROSOFT_MAIL_CLIENT_ID` and `MICROSOFT_MAIL_CLIENT_SECRET` there is no
button and the card says "Outlook / Microsoft 365: Microsoft accounts aren't
supported yet."

**Limits** (all in code comments too): a connect must finish within 10
minutes in the browser that started it (`connect_attempt.LIFETIME_SECONDS`);
access tokens are refreshed when under 5 minutes are left
(`REFRESH_MARGIN_SECONDS`); a token request times out after 10 s
(`TOKEN_TIMEOUT_SECONDS`); Microsoft ends a grant unused for 90 days, when
the person removes the app's access, or when their sign-in sessions are
revoked (then: Reconnect). A password change alone does not end it: this app
is a confidential client (it has a client secret), and Microsoft keeps those
grants through a password change. The client
secret expires (24 months at most): when it does, every Microsoft mailbox
says "The administrator needs to renew the Microsoft client secret."

**Deploy** (no migration; `docker/.env` reaches backend and Celery through
`env_file`, nothing for the frontend):

1. Entra app (once): the steps in `custom/docs/email-imap-connector.md`.
   Redirect URI (platform Web):
   `https://notes.danvics.com/api/v1/auth/microsoft/mail/connector/callback`.
   Account types: any organizational directory and personal Microsoft
   accounts. Delegated Microsoft Graph permissions: `IMAP.AccessAsUser.All`,
   `openid`, `email`, `profile`, `offline_access`.
2. `docker/.env`: `MICROSOFT_MAIL_CLIENT_ID=<Application (client) ID>` and
   `MICROSOFT_MAIL_CLIENT_SECRET=<secret Value>`. Optional:
   `MICROSOFT_MAIL_TENANT` (default `common`), `MICROSOFT_MAIL_REDIRECT_URI`
   (default derived from `BACKEND_URL`).
3. Build the backend images and the frontend, then after the gate
   `up -d backend celery_worker celery_beat frontend` (commands under
   "Deploying: wait for the gate" and in `AGENTS.md`).
4. Check: Connectors → Email (IMAP) → Connect a Microsoft account → sign in
   → "Microsoft account connected" → **Test connection** on the new row says
   how many folders it found.

## The phone app connects Drive and Microsoft mail (`app/app_handoff`, 2026-10-10, not deployed)

The phone app (`soloclef_mobile`) holds a bearer token, but the browser sheet
it opens for Google's or Microsoft's consent does not, and no token may
travel in a URL. So the app asks for a **one-time code** with its token and
opens the code's page in the sheet, where the server starts the flow as the
website's own Connect buttons do (plan items B2 and B10):

```
POST /api/v1/app/handoff   {purpose: drive_connect | microsoft_mail, workspace_id, account_id?,
                            code_challenge (required for both since 2026-10-11, S256)}
     -> {code, expires_in: 60, path}    (bearer; the website's checks: connectors:create, provider set up)
GET  /api/v1/app/handoff/{code}         (the sheet, no token)
     -> 303 to Google's or Microsoft's consent page, the attempt's cookie set in the sheet
...  the provider's callback (unchanged paths)
     -> 303 soloclef://connectors/done?purpose=drive_connect&status=confirm&completion=…
     -> 303 soloclef://connectors/done?purpose=microsoft_mail&status=confirm&completion=…
POST /api/v1/app/handoff/complete  {completion, code_verifier}   (bearer)
     -> {purpose: drive_connect | microsoft_mail, status: connected}   saved now
```

- **Codes:** random, 60 s (`codes.CODE_LIFETIME_SECONDS`), spent by the first
  open, stored only as their SHA-256 in Redis (`soloclef:app_handoff:code:*`,
  `SET … EX NX` / `GETDEL`; the same store as the Picker's); without Redis,
  in the worker's memory with a warning. An unknown, spent or old code sends
  the sheet back with `status=expired` (the Picker's page, whose address
  says `?for=drive_picker`, shows its 410 page instead); a provider not set
  up, with `status=not_configured`. 20 hand-offs and 30 pages a minute from
  one address, as for the Picker.
  The page answers `Cache-Control: no-store` and `Referrer-Policy: no-referrer`.
- **Google Drive** (since 2026-10-11, "Security fixes of 2026-10-11"): the
  page sets the Drive connect cookie (`google_drive/connect/connect_attempt.py`)
  in the sheet, with `app` and the app's challenge; the callback holds the
  grant, encrypted (`held_grant.py`), under a completion exactly as for
  Microsoft below, and the app's `POST /app/handoff/complete` saves it as the
  website's connect does (account-wide row, switched on in the workspace it
  was connected from). Refusals come back as the website's `google_drive=`
  outcome in `status` (`google_drive_oauth_denied`,
  `google_drive_scope_missing`, …). Reconnect is the same purpose:
  connecting again reuses the person's row.
- **Microsoft mail:** the page itself sets the signed connect cookie
  (`connect_attempt.py`) in the sheet that goes on to Microsoft, so the
  callback's same-browser rule holds; the attempt carries `app` and the app's
  PKCE challenge, and refusals come back as `microsoft_mail=` outcomes in
  `status`. `account_id` preselects the mailbox being reconnected.
  **The callback saves nothing for the app.** The page takes no credentials,
  so the browser that goes through Microsoft is whichever one opened the
  link: a link sent to someone else (or an account's page that mints a fresh
  code for every visitor, within the 20 a minute) would otherwise connect the
  visitor's mailbox to the link's account. So the mailbox is held, encrypted
  with `SECRET_KEY` (`held_mailbox.py`), under a one-time **completion**
  (random, stored as its SHA-256 in Redis `soloclef:app_handoff:completion:*`,
  **5 minutes**, `codes.COMPLETION_LIFETIME_SECONDS`, spent by the first try
  right or wrong, 20 tries a minute per address), and the sheet comes back
  with `status=confirm&completion=…`. The app saves it with
  `POST /api/v1/app/handoff/complete {completion, code_verifier}`: its bearer
  token must be the account that asked for the code (else 403
  `HANDOFF_WRONG_ACCOUNT`), the verifier must hash to the code's challenge
  (else 403 `HANDOFF_WRONG_VERIFIER`), and that account must still have
  `connectors:create` in the workspace; an unknown, spent or old completion is
  410 `HANDOFF_EXPIRED`. A `microsoft_mail` or `drive_connect` hand-off asked
  for without `code_challenge` (an app build from before this) is refused
  with 400 `HANDOFF_CHALLENGE_REQUIRED`, whose message tells the person to
  update the app or use the website.
- **Where the sheet returns:** always `APP_HANDOFF_RETURN` (default
  `soloclef://connectors/done`), never an address a request names. Change it
  only together with the app's scheme.

Tests: `tests/unit/app_handoff/` (codes; both flows through the HTTP doors,
Microsoft's whole round trip on the stand-in token endpoint, held until the
app confirms; a link opened by a second browser; a wrong verifier, another
account, an old completion), `tests/integration/app_handoff/test_microsoft_mail_handoff.py`
(the same on real Postgres: nothing lands on either account when a second
browser opens the link) and
`tests/integration/connectors/google_drive/test_app_handoff.py` (the whole
Drive round trip on the stand-in Google: held, then saved once the app
confirms). **Deploy:** no
new setting needed; the app's Connect buttons use it once the backend is
rebuilt. Until then they fall back (Drive: the website's own flow;
Microsoft: "needs a server update"). **The app's side** of the confirmation
(make a verifier, send its S256 `code_challenge`, and on `status=confirm`
post `completion` with the verifier and its token) came with the app's
update of 2026-10-11 (`release/app-2026-10-11` `3ac6be2e8`,
`features/workspace/connectors/handoff.ts`), for Microsoft and for Drive
(confirmed since that day's security fixes); an app without it hears
`HANDOFF_CHALLENGE_REQUIRED` and shows its message (update the app, or
connect on the website).

## Web search in chat: the Tools menu, Exa, Tavily, Browserbase (`c0022_chat_web_search_prices`, 2026-10-09)

**What each web tool uses.** A notebook chat answers from its sources; the
composer's **Tools** menu adds one web specialist for one message
(`app/notebooks/chat/pins.py`, `app/agents/chat/retrieval_scope.py`).

| Tool | Specialist -> capability | What runs | Credits |
| --- | --- | --- | --- |
| Google Search | `google_search` -> `google_search.scrape` | google.com rendered in patchright Chromium (Scrapling's `AsyncStealthySession`) through `PROXY_URL` (tinyproxy on the home desktop over Tailscale), reCAPTCHA solved by `CAPTCHA_SOLVER_PROVIDER` (2captcha) and the solved IP shared through Redis; `/goto` links resolved by following the redirect. When every IP is walled, the bundled SearXNG (`searxng` container: DuckDuckGo, Brave, Yandex, Yahoo, Wikipedia, Wikidata, Stack Overflow, all through the same proxy; Google and Bing off) answers instead, marked `resultsProvider=searxng`. About 12-16 s a page warm, 40-110 s when a solve is needed. | 0.55 per page of results |
| Web | `web_crawler` -> `web.crawl` | Reads the URLs it is given (or spiders a site); it does not search. Scrapling's ladder: static TLS-impersonated fetch, then a browser, then the stealth browser with Cloudflare/captcha handling, all through `PROXY_URL`; Trafilatura extracts the text. | 0.2 per page |
| Exa, Tavily | `exa_search` / `tavily_search` -> `exa.web_search` / `tavily.web_search` | The provider's search API with the key in the workspace's Exa / Tavily MCP connector (the research box's request, `app/notebooks/research/web_apis`): Exa's `auto` search with highlights, Tavily's `basic` search, 10 results. Straight to the provider, not through the proxy. | Exa 1.5, Tavily 0.8 per search |
| Browserbase | `browserbase` -> `browserbase.read_page` | One page opened in a Browserbase cloud browser with the key in the workspace's Browserbase MCP connector, read over CDP with Playwright (`app/notebooks/research/page_read`, the research box's "Read a web page"). | 0.2 per page |

The research box: "Web" asks SearXNG first (free) and runs the Google Search
capability only when SearXNG has nothing; "Google Search" is the scrape
above; Exa and Tavily use the same APIs and keys as the chat but cost no
credits there; deep research and imports read pages with `web.crawl`; "Read
a web page" uses Browserbase when connected, else the crawler.

**Exa, Tavily and Browserbase in chat.** They are capabilities built per
workspace (`app/capabilities/web_search_api`, `app/capabilities/browserbase/
read_page`), not entries in the global registry, because the key lives in
the workspace's MCP connector; so they have no REST door. They call the
providers' APIs directly rather than the MCP servers' tools: results come
back structured, are recorded in the runs log (`exa.web_search`,
`tavily.web_search`, `browserbase.read_page`) and are cited like every other
web result, a **Web** chip that opens the run. Each specialist exists for a
turn only when its key is in one of the workspace's MCP connectors
(`app/agents/chat/keyed_web_specialists.py`, one query per turn when the
workspace has any MCP connector); the composer lists the matching tools only
then (`soloclef_web/features/composer-tools`, from the research box's
status request, which the menu reuses for up to 60 s). A notebook pick of
one whose key is gone by the time the message is sent (or sent by an API
client without a key) is not built, and the prompt tells the model it is
not available, so the answer says so instead of trying it
(`SourcesOnly.unavailable_picks`, set in `factory.py`). A notebook chat
uses one only when it was picked for that message, and the turn stays
sources-only otherwise. Picking one changes
nothing else about the turn: the MCP servers' own read tools (and
`get_connected_accounts`) stay denied in `mcp_discovery`
(`denied_retrieval_tools`). Mail is not denied in code: when a mailbox is
connected, the `email_imap` specialist is built in notebook turns with its
read tools, as on main, and only the prompt line "Connected-app data is not
retrieved either" holds it back. A plain chat or an automation task that
can use the web has them as ordinary web specialists, used when the
question needs the web.

**Billing.** Gate then charge through `app.capabilities.core.billing`, like
Google Search: `exa_search` and `tavily_search` per search (platform meters,
under `PLATFORM_SCRAPE_BILLING_ENABLED`), a Browserbase page on `web_crawl`
(under `WEB_CRAWL_CREDIT_BILLING_ENABLED`). The payer is the workspace's
owner (`meter._resolve_payer`), so a member's search spends the owner's
credits, as for every paid tool. The charge carries only `workspace_id`
(the tools do not know which member asked), so the usage row records the
owner as `user_id` too, like every capability charge
(`custom/WORKSPACE_CREDITS.md`). The two prices are rows seeded by `c0022`
at the providers' own list price for the search the chat runs (Exa: $5 per
1k searches + $1 per 1k pages of highlights x 10; Tavily: one API credit,
$0.008) and are edited on the billing admin page.

**Limits and automatic behaviour**

- *10 results a search, 400-character query* (`MAX_RESULTS`,
  `MAX_QUERY_CHARS` in `app/capabilities/web_search_api/schemas.py`). A
  search that answered is one billed search even with no results.
- *One page per Browserbase call; 30,000 characters of its text kept by
  default, at most 100,000* (`DEFAULT_MAX_CHARS`, `MAX_CHARS` in
  `browserbase/read_page/schemas.py`); a longer page is cut and marked
  `truncated`. The page load gets 60 s (`BROWSERBASE_BUDGET_S`) and
  Browserbase ends the session itself after 90 s (`SESSION_TIMEOUT_S`); the
  session is always released.
- *Only public addresses.* A private or unresolvable URL is refused before a
  session starts; a page that redirected to one is refused after.
- *No retries, no fallback.* Every attempt spends the key's own credit, and
  the person picked Browserbase, so a failure is reported rather than
  quietly re-read with the crawler.
- *A tool that cannot run says so.* No key, key refused, provider out of
  credit or busy, page not loaded: the tool answers with a sentence
  (`CapabilityUnavailableError`, `app/capabilities/core/unavailable.py`),
  the run is logged as an error, and nothing is charged. Any other error
  from Browserbase's remote browser (a CDP attach that times out, a page
  whose script navigates away while it is read) is logged with its trace
  and answered with "Browserbase couldn't read the page." (`COULD_NOT_READ`
  in `browserbase/read_page/executor.py`), so it never fails the turn.
- *Keys stay server-side*; the composer learns only which tools exist.

**Not changed, for the owner to decide.** Automations that can use the web
also still see the raw Exa / Tavily / Browserbase MCP tools through
`mcp_discovery`, which are not metered. The research box's Exa / Tavily
searches stay free of credits (as `plans-and-credits` says), while the
chat's are charged.

**Tests.** `tests/unit/capabilities/web_search_api/`,
`tests/unit/capabilities/browserbase/`, `tests/unit/capabilities/core/
test_chat_web_tool_billing.py`, `tests/unit/capabilities/access/
test_unavailable_verb.py`, `tests/unit/agents/chat/test_keyed_web_specialists.py`,
the Exa/Tavily/Browserbase cases in `test_notebook_retrieval_scope.py` and
`test_tool_preference.py`; web: `tests/unit/composer-tools/`.

## Shared models (`docker/global_llm_config.yaml`, 2026-09-25)

Every account, including new ones, gets the same models: the 15 that were
enabled on the owner's two LiteLLM connections (13 distinct ids, since
`openai-gpt-6-luna` was enabled on both and `openrouter-bge-m3` is an
embedding model with no per-user role). `docker/global_llm_config.yaml` is
mounted read-only into backend, celery_worker and celery_beat as
`app/config/global_llm_config.yaml`; each entry becomes a server-owned GLOBAL
model, served by `http://litellm:4000/v1` with the key from `OPENAI_API_KEY`
(`api_key: "os.environ/OPENAI_API_KEY"`), which never reaches a browser.
Because the list exists, every workspace counts as ready and there is no model
onboarding; `/onboard` redirects home. Connecting providers is admin-only in
the UI (Models settings, "Manage models"); the API still accepts a user's own
connection. LiteLLM itself is untouched. Adding a model: add an entry with a
new id (never reuse one), add its name below, redeploy.

**Names people see** come from `soloclef_backend/app/model_display_names/names.py`
(the single source; rename there and redeploy). Since 2026-09-25 they are the
owner's short names: a family name, a version only where two of a family are
offered, "Pro"/"Lite" for tiers. They reach every place a model is named: the
chat and image pickers, Settings -> Models (including quick tasks and Studio
outputs), automations, Billing -> Admin -> Model tiers, Studio's "made with",
and a message's model info (which shows a shared model under today's name even
on older messages). The pickers no longer print the provider
("OpenAI-Compatible") beside a shared model; a premium-tier model (by the
admin's tier, Billing -> Admin -> Model tiers, which `/global-model-connections`
applies) carries a "Premium" tag instead. A person's own connection still shows
its provider.

| LiteLLM id | Shown as | Role | id | Tier |
|---|---|---|---|---|
| `ds-deepseek-v4.1-flash` | DeepSeek | chat | -1 | standard |
| `openai-gpt-5.6-luna` | Luna | chat | -2 | standard |
| `openai-gpt-6-luna` | Luna Pro | chat | -3 | premium |
| `google-gemini-3.7-flash` | Gemini 3.7 | chat | -4 | standard |
| `google-gemini-3.8-flash` | Gemini 3.8 | chat | -5 | standard |
| `mistral-medium` | Mistral | chat | -6 | standard |
| `openrouter-llama-3.3-70b-instruct` | Llama | chat | -7 | standard |
| `groq-qwen3.8-27b` | Qwen 3.8 | chat | -8 | standard |
| `groq-qwen3.6-27b` | Qwen 3.6 | chat | -9 | standard |
| `openai-gpt-image-2` | GPT Image | image | -2001 | standard |
| `openai-gpt-image-2.5-sunburst` | Sunburst | image | -2002 | standard |
| `google-gemini-3.1-flash-image` | Gemini Image | image | -2003 | standard |
| `google-gemini-3.1-flash-lite-image` | Gemini Image Lite | image | -2004 | standard |
| `google-gemini-3-pro-image` | Gemini Image Pro | image | -2005 | standard |
| `openrouter-mai-image-2.5-pro` | MAI Image | image | -2006 | standard |
| `openrouter-bge-m3` | Embeddings | (embeddings; not listed) | | |

Tiers: no model had one before (all counted as standard). Billing charges by
the admin's choice (Billing -> Admin -> Model tiers, stored in
`billing_settings.model_tiers`); the YAML's `billing_tier` carries the same
value for Auto and the model picker. Only Luna Pro (`openai-gpt-6-luna`) is
premium (5 credits a turn, Plus and Pro plans); image models are priced per image whatever their
tier. Auto reads the admin's tier, so it never gives a Free-plan account a
model billing would refuse.

**Defaults for everyone** (the owner's own on workspace 1): chat Auto; vision
Gemini 3.7; image GPT Image (`app/workspace_model_defaults`, stamped on
every new workspace); quick tasks DeepSeek
(`app/quick_tasks/resolution.py`); Studio "recommended"
(`app/studio_models/recommended.py`: DeepSeek first for slides, mind map,
flashcards, data table, reports and infographic, Luna first for audio,
video and quiz). Role fallbacks stay `IMAGE_GEN_FALLBACK_MODEL` /
`VISION_FALLBACK_MODEL` and now also find the shared models.

**The owner's private connections are switched off, not deleted**, so
`daniel@danvics.com` sees exactly what everyone sees. To switch them back on:

    docker exec soloclef-db-1 psql -U surfsense -d surfsense -c \
      "update connections set enabled = true where id in (1, 2)"

Their models keep their own enabled flags. On the switch, the owner's
workspaces were moved onto the shared models: workspace 1 from vision 72 /
image 160 (the same models on connection 2) to -4 / -2001, workspace 2 from
Auto to the same defaults, and workspace 1's Studio row (identical to the
recommended setup but pinned to models 3 and 2 of connection 1) was deleted so
it follows the recommended setup. To put workspace 1 back as it was, after
switching the connections on:

    update workspaces set vision_model_id = 72, image_gen_model_id = 160 where id = 1;
    update workspaces set vision_model_id = 0, image_gen_model_id = 0 where id = 2;
    insert into workspace_studio_models (workspace_id, default_choice, overrides, updated_at)
    values (1, '{"mode": "recommended", "primary": null, "fallback": null}',
      '{"quiz": {"mode": "model", "primary": 3, "fallback": 2},
        "audio": {"mode": "model", "primary": 3, "fallback": 2},
        "video": {"mode": "model", "primary": 3, "fallback": 2},
        "slides": {"mode": "model", "primary": 2, "fallback": 3},
        "mindmap": {"mode": "model", "primary": 2, "fallback": 3},
        "data_table": {"mode": "model", "primary": 2, "fallback": 3},
        "flashcards": {"mode": "model", "primary": 2, "fallback": 3},
        "report_pdf": {"mode": "model", "primary": 2, "fallback": 3},
        "infographic": {"mode": "model", "primary": 2, "fallback": 3},
        "report_docx": {"mode": "model", "primary": 2, "fallback": 3},
        "report_interactive": {"mode": "model", "primary": 2, "fallback": 3}}', now());

Removing the shared list altogether: delete the three `global_llm_config.yaml`
volume lines in `docker/docker-compose.override.yml` and recreate backend,
celery_worker and celery_beat (after the gate); workspaces on a shared id fall
back to Auto, and a workspace with no model of its own shows the composer's
"chat is unavailable" notice (there is no onboarding page any more).

**Client IP.** The host Caddy's `notes.danvics.com` block drops
`CF-Connecting-IP` and sets `X-Real-IP` to the real peer, so the per-IP limits
on sign-in and reset cannot be dodged with a forged header (the host is not
behind Cloudflare's proxy).

## Automation runs: "If already running", time limits, Cancel (2026-10-09)

**If already running** (builder -> Settings; `execution.concurrency`,
`app/automations/concurrency/`). Every decision is made under a row lock on
the `automations` row (`SELECT ... FOR UPDATE`), so fires that land at the
same moment (two workers, two triggers in one tick, a message delivered
twice) are decided one after the other.

- `drop_if_running`, "Skip if already running" (the default): when the
  automation already has a pending or running run, the fire is dropped. No run
  row is written; the worker logs `automation N: fire from trigger T dropped
  (skip_already_running; ...)`.
- `queue`, "Queue the next run": the run is created Pending and starts only
  when nothing else of that automation is running and no older run is still
  pending (oldest first). The run that ends hands the oldest pending run to a
  worker. At most 20 may wait (`QUEUE_LIMIT`); further fires are dropped and
  logged (`skip_queue_full`).
- `always`, "Always run": no check; runs go side by side.

Run status changes are compare-and-set (`runtime/repository.py`): a run starts
once even if its message arrives twice, and a Cancel or the sweep below is
never overwritten by a late worker.

Lock order (`concurrency/lock.py`): anything that writes both the automation
row and its run rows takes the automation lock first -- launching a run,
starting one, Pause/Archive (`services/automation.py`) and cancelling stranded
waiting runs (below). Writers of run rows alone (Cancel, the executor, the
sweep, which commits one run at a time) never ask for it while holding a run
row. A Pause landing while a worker starts a run therefore waits for it
instead of deadlocking (Postgres would otherwise abort one of them: the Pause
as a 500, or the worker as a crashed task).

**Time limit** (`execution.timeout_seconds`, 600 by default, at most 86400 =
24 h (`MAX_TIMEOUT_SECONDS`, `schemas/definition/execution.py`, the builder's
maximum too), counted from the moment the run starts, not from the fire). A
larger value sent through the API or written by the chat is lowered to 86400
rather than refused, so a definition saved before the cap still loads (the
automation page re-validates stored definitions) and runs with 24 h. The executor stops the plan itself
(`runtime/supervise.py`), adds a step entry for the task it cut off, marks the
run `timed_out`, and sends the "(timed out)" result email when "Email me the
result" is on. A task that runs past its own per-task timeout on its last try
also ends the run `timed_out`. Celery's `time_limit` / `soft_time_limit` for a
run are its timeout plus 120 s (`WORKER_KILL_GRACE_SECONDS`,
`dispatch/enqueue.py`): a backstop for a wedged worker, never the normal stop.

**Overdue sweep** (beat entry `automation-runs-sweep-overdue`, every minute,
`runtime/overdue.py`). A run still `running` 180 s past its limit
(`OVERDUE_AFTER_SECONDS`, `dispatch/enqueue.py`) has lost its worker --
typically a deploy restarted it -- so it is marked `timed_out`, emailed if its
limit passed under an hour ago (`EMAIL_WINDOW`), and the next queued run is
started. A run pending for 5 minutes (`STALLED_AFTER`) with nothing running is
handed to a worker again. The two passes run on separate sessions, so an error
in one never stops the other (`tasks/sweep_overdue_runs.py`), and a run's limit
is read from its snapshot in SQL by `snapshot_timeout_sql`
(`runtime/time_limit.py`): a stored value that is not a positive whole number
reads as 600, one above the cap as 86400, so no stored row can make the query
fail for every workspace.

**Start window** (`START_WINDOW`, 1 hour, `concurrency/handoff.py`). A waiting
run is handed to a worker -- by the hand-off after a run ends or is cancelled,
or by the sweep -- only within an hour of when it could first have started: its
creation, or under `queue` the later of that and when the queue last moved
(the latest end of a run that did start, counted at most 180 s past that run's
own limit, since a longer one had lost its worker and was only marked later).
Past that the run was stranded while nothing could start it, so it is marked
`cancelled` with "Not started: it was still waiting to start 1 hour after it
could have, so it was dropped rather than run late." -- no credits, no note, no
email -- and the next waiting run is considered the same way. A lost message
inside the hour is still re-sent by the sweep after 5 minutes.

The first sweep after deploying this marks any run left `running` from before
as Timed out (those older than an hour get no email) and cancels, as "Not
started", any run left `pending` from before for over an hour, instead of
running it days late. To see beforehand what it will touch (read-only):
`SELECT status, count(*), min(created_at) FROM automation_runs WHERE status IN
('pending', 'running') GROUP BY status;`

**Cancel.** `POST /api/v1/automations/{id}/runs/{run_id}/cancel`
(`automations:execute`: Owners and Editors; the **Cancel** button on a pending
or running run). 409 "This run already finished." otherwise. Cancelling hands
the automation's next waiting run to a worker at once (`services/run.py`), so
under `queue` the run behind a cancelled head, or behind a running run whose
worker was gone, doesn't wait for the sweep; the cancelled run's own late
message does the same, in case that hand-off failed. Pausing or archiving an
automation cancels its pending and running runs. The worker reads
its run's status every 5 s (`STOP_POLL_SECONDS`) on its own connection and
cancels the step in flight, which releases that step's credit hold. Deleting
an automation deletes its runs; a worker mid-run stops on its next check. No
email is sent for a cancelled run.

No migration: `cancelled` and `timed_out` have been in `automation_run_status`
since `144_add_automation_tables`. Deploying needs backend, celery_worker and
celery_beat restarted (new task and beat entry).

## Platform scrapers: checked 2026-10-09 (`fix/platform-connectors`)

Every scraper was run against one small public target from an isolated copy
of the backend (live image, this branch's code, live proxy settings, no
database). Results on the home proxy (`PROXY_URL`, tinyproxy on the desktop
over Tailscale) unless noted:

| Platform / verb | Result |
|---|---|
| YouTube video, search, playlist, comments, transcript | Works (3-8 s) |
| Web crawl | Works |
| Google Search | Works (renders through the home proxy) |
| Google Maps search, reviews | Works |
| Amazon search, Walmart search and reviews | Works |
| Indeed | Works, slowly: each search clears Cloudflare first (~100 s) |
| Instagram single post / reel | Works |
| Instagram profile, details | Was broken, fixed (below) |
| Instagram keyword search | Was empty, fixed (below) |
| TikTok profile, single video | Works |
| TikTok hashtag feed, Explore (trending) | Fails on the home proxy (empty feed); works from the server -> `SCRAPER_DIRECT_PLATFORMS` |
| TikTok comments | Was broken, fixed (below) |
| TikTok account search | Never works: TikTok shows account search to signed-in users only |
| Reddit (subreddit, search, posts, comments) | Fails on the home proxy ("blocked by network security"); works from the server -> `SCRAPER_DIRECT_PLATFORMS` |
| PubMed, arXiv, medRxiv/bioRxiv | Works (1-4 s) |
| Google Scholar | Works (~65 s) |
| "Web" (bundled SearXNG) | Works but thin: only Yandex answers; DuckDuckGo and Startpage answer CAPTCHA, Brave "too many requests" through the home proxy |
| Exa, Tavily, Browserbase | Not run (they spend the owner's keys) |
| Google Drive | Not set up: `GOOGLE_OAUTH_CLIENT_ID`/`SECRET`/`GOOGLE_PICKER_API_KEY` are unset |

There are no X, LinkedIn or Facebook scrapers; those names only appear as
social links the crawler picks out of pages.

**`SCRAPER_DIRECT_PLATFORMS`** (backend env, comma-separated, default empty):
platform scrapers listed there leave from the server's own address instead
of the proxy. Names: `google_search`, `youtube`, `reddit`, `instagram`,
`tiktok`, `google_maps`, `indeed`, `amazon`, `walmart`. The web crawler and
captcha solving never change. Every proxy getter in `app.utils.proxy` takes
an optional `platform`; for a listed platform it returns `None`, which each
scraper already handles as "no proxy" (`app/utils/proxy/direct.py`). On this
server it is `reddit`: the owner uses Reddit search and not TikTok (adding
`tiktok` would bring back its hashtag and Explore feeds).

**Reddit without a proxy.** Reddit used to give up when it had no proxy (no
browser warm, so no `loid` cookie). It now warms and fetches from the
server's address too. Going direct there is no other exit IP, so a refused
warm is retried once (`_DIRECT_MAX_ROTATIONS = 1`), not 8 times. A run where
every target hit the wall now raises (`ForbiddenError`, research shows
"Reddit is blocking automated searches right now") instead of an empty
success; walled comment threads keep the posts already read.

**TikTok without a proxy.** The same holds for TikTok going direct: a
refused or failed page/API fetch is repeated once (`_DIRECT_MAX_ROTATIONS = 1`
in `tiktok/session/client.py`), not 8 times, and an empty hashtag or Explore
feed gets one browser run whatever `TIKTOK_LISTING_MAX_ATTEMPTS` says (the
live env already sets that to 1 for the static home proxy). Every retry
would only hit TikTok again from the server's address, which is what this
setting relies on.

**Instagram profiles from the public pages.** `api/v1/users/web_profile_info/`
answers 401 `require_login` from both addresses, so it is no longer called. A
profile is read from `/<username>/embed/` (counts and the latest posts) plus
`/<username>/` (biography, following, first bio link). Pages that answer
without a profile mean "no such account" (an empty result, not an error);
only when Instagram walls both pages is the target reported as blocked. The
embed carries **6 posts**, so a profile read returns at most the latest 6
(research asks for 3 per profile). Keyword search (Google
`site:instagram.com`) keeps post and reel hits as well as profiles when the
run wants posts or reels, since Google mostly returns posts and reels for a
topic. A one-word query is first read as an account of that name (the read
is reused, not repeated); when there is no such account it is searched on
Google like any other words. With several comma-separated queries, one
walled account lookup does not sink the others. The handle goes into the
URL path, so a name that is not a handle (1-30 letters, digits, `.` or `_`)
is skipped unfetched, and `fetch._build_url` refuses any URL that is not on
instagram.com (handles such as `http.kitchen` used to be fetched as a host).

**TikTok comments over HTTP.** `/api/comment/list/` answers an unsigned GET;
the browser route lands on TikTok's "Oops! Something went wrong" page. The
API goes first (`session/comments_api.py`: 50 per page, at most 20 pages;
the `tiktok.comments` verb itself caps a call at 100); the browser is tried
only when the API gave no answer at all.

**YouTube playlist route.** `/youtube/playlist-videos` looked for
`playlistVideoRenderer`, which YouTube retired for `lockupViewModel`; it now
uses the scraper's parser. Nothing in the web app calls this route today.

**Research errors.** A platform that refuses every attempt
(`ForbiddenError` from its executor) now reaches the research box as
`source_blocked` (its existing message) instead of "The search didn't go
through".

## Workspaces: roles, invites, public links (`c0010_workspace_sharing`, 2026-09-25)

**Roles.** Owner, Editor, Viewer. The Owner is whoever created the workspace;
nobody is ever made Owner through an invite or a role change (the full-access
role cannot be granted at all). Anyone else may only give a role whose
permissions they hold themselves, and may only change or remove members at or
below them, never the Owner or themselves (`app/workspace_roles/grants.py`).
Only the Owner writes custom roles, never with `*`; the UI no longer offers
custom roles. There is no ownership transfer yet.

**Invites** (`app/workspace_invites`, Team page). Two kinds, both needing a
signed-in registered account to accept (sign-ups are closed; create accounts
with `scripts/create_account.py`):

- *By email*: an Owner or Editor enters an address and a role (Editor or
  Viewer). An address with no account is refused with "sign-ups are closed".
  The invitation email (same SMTP settings as the password reset) says who
  invited them to which workspace, as what, with the link `/invite/<code>`.
  Only that address can accept (case-insensitive); one use; resend once a
  minute.
- *Invite link*: no address; anyone signed in who opens it joins, within its
  expiry and optional maximum uses.

Expiry 7 / 30 days / never; pending invites are listed on the Team page with
copy link and revoke. Invites that granted a full-access role were switched off.

**Public links** (read-only, `app/public_sharing`): only Owners create them by
default (the Editor role lost `public_sharing:create` in c0010), for a
notebook's chat (notebook menu -> Share chat publicly) or a finished Studio
output (its viewer -> Share). Expiry 7 days (default) / 30 days / never; an
expired link shows "This link has expired" and its files 404. Workspace
settings -> Public links lists them with creator, created and expiry, copies
and revokes them (Owners, or the link's creator). A snapshot carries no
account ids, avatars, storage keys or server paths; a shared output is a
frozen copy of its title and Markdown plus its artifact's files, opened in
the owner's viewer for its kind (see "Shared Studio outputs open as their
owner sees them" below), and files a browser would run (HTML, SVG) are
served as sandboxed downloads. Nobody can publish someone else's private
chat.

**Messaging channels are removed** (upstream's Telegram/WhatsApp/Slack/Discord
gateway: `app/gateway`, its routes, Celery tasks and `.gateway` queue, the
WhatsApp bridge, the settings page and the `external_chat_*` tables, dropped
in c0010). They could not be set up here.

**Connected git repository** (Workspace settings -> Connected repo): only
the workspace Owner may connect, sync, resolve or disconnect one (the API
refuses everyone else, whatever their role's permissions), and the page warns
that content leaves the app. It only works for a git-native workspace, which
needs `KNOWLEDGE_STORE_ENABLED=TRUE` and the per-workspace seed
(`scripts/migrate_knowledge_store.py`); that switch is off on this host, so
the page says so. Providers: a GitHub App installation or gitlab.com with a
token. Fixed on 2026-09-25: the first sync of a workspace that was empty when
it connected deleted every document written since (it read "no base" as "the
current head").

**Hidden, not removed.** Community prompts and agent permissions are shown
only to administrators (`BILLING_ADMIN_EMAILS`); the API refuses publishing a
community prompt to anyone else, since it reaches every account. API keys
were hidden too until 2026-10-09, while "Connect your agent" (shown to every
account) told people to create one there; the page is now open to everyone,
as `POST /api/v1/pats` always was (a signed-in session, no admin check).

## Shared Studio outputs open as their owner sees them (`fix/shared-outputs`, 2026-10-11, not deployed)

Daniel: "shared stuff from studio appear differently than it appears to the
person who shared". A link made with **Share publicly** (website or app; both
call `POST .../studio/outputs/{id}/public-link`, `app/public_sharing/output_links.py`)
used to open a generic page: the main file (or a Download of it), then all
the output's text. Flashcards and a quiz showed every answer at once, a mind
map a picture of the fully open map, a slide deck no slides (its PDF
preview could not be reached), a data table a description of the workbook.
Now the link opens the owner's own viewer for the kind:

- `GET /api/v1/public/{token}` sends, for an output, `files` (every file of
  the shared artifact in the in-app manifest's shape, the main file first,
  each at `/api/v1/public/{token}/artifacts/{artifact_id}/files/{file_id}/content`)
  and `generation`, read when the link is opened
  (`app/public_sharing/output_files.py`). `file` (the main file alone) stays
  for app builds made before `files`. Links made before this show their
  output whole: nothing needs re-making.
- The new file route serves any file of an artifact the snapshot's
  `artifact_ids` names (the allowlist, as before; another artifact's file is
  a 404 without a query; an expired link serves nothing), with byte ranges
  and ETags (`artifact_file_response`), `Cache-Control: private, no-cache`
  (every use revalidates, so a revoked link stops at once) and
  `Content-Security-Policy: sandbox` for anything but a PDF or MP4. The
  viewers read JSON, workbooks and HTML with `fetch`, so the attachment
  disposition does not get in their way.
- The website's page (`features/public-sharing/ui/public-output-view.tsx`)
  is the owner's output view without what acts on the notebook: "Studio ›
  Kind", the title, "Shared read-only · expires …", then
  `ArtifactRenderer` (mind map, flashcards, quiz, slides via the PDF
  preview, data table, infographic, video), the report reading view with
  contents (`ReportBody` with `shared`: no suggestions, no Explain), or the
  podcast player with its transcript. Download in the header for the kinds
  the owner can download. Flashcards and quiz answers stay in the visitor's
  browser (the viewers' local mode for `workspaceId` 0). No prompt and
  sources chip, Explain, Share or report suggestions.
- The page's `<title>` and link preview (Open Graph) name the output
  ("Planets flashcards", "Flashcards · shared read-only from Soloclef Notes.")
  instead of the site's own card, never indexed, `no-referrer`
  (`app/public/[token]/page.tsx`, `features/public-sharing/lib/`). The title
  is read on the server per request, uncached, and the page falls back to
  the site's name after **2.5 s** (`SUMMARY_TIMEOUT_MS`) or when the link is
  gone.
- **Sharing an unchanged output again** returns the same link, now with the
  output's current title (a rename used to keep the old title on the link),
  and the expiry just chosen: re-sharing a "never" link with the default
  7 days makes it a 7-day link, from the website or the app alike (unchanged
  behaviour, now written down). A changed output (new generation or text)
  gets a new link; the old one keeps its copy.
- Quiz and flashcard text (`app/artifacts/verification/formats/study_text.py`):
  brackets and parentheses in plain text are no longer escaped as `\(`,
  `\)`, `\[`, `\]`, which the website and the app read as maths ("(0.39
  AU)" showed as an equation). `[` is written `&#91;`, and a `)` after a
  line's leading digits `&#41;` (it would start a numbered list). A quiz's
  choices and "Why each option" are a list (`- A. …`), so they no longer run
  into one paragraph. This changes the stored Markdown of new outputs only;
  existing ones keep theirs (their viewers read the JSON, which was right).
- The phone app's half is `fix/app-shared-outputs` (an update, not a new
  build): its public page uses the same viewers as its Studio
  (`soloclef_mobile/src/features/public/SharedArtifact.tsx`), and works on a
  server without `files` (main file only).

**Deploy:** the backend before the frontend (the new public page reads
`files`), then the app update; no migration of its own. It goes out with the
release of 2026-10-11 ("Deploying the release of 2026-10-11", near the top),
whose step 6 restarts the server and then the website. The app reads
`files` when the server sends it, so its update could also go first.

**Tests:** `tests/unit/public_sharing/test_shared_output_files.py`,
`test_output_link_reshare.py`, the escaping and list tests in
`tests/unit/artifacts/test_flashcards_contract.py` and
`test_quiz_contract.py` (now marked `unit`, so CI's `pytest -m unit` runs
them), the web's `tests/unit/public-sharing/shared-output.test.ts`.

## Automations: how runs fire, and the result email (2026-10-09)

**Runs.** Celery beat ticks `automation_schedule_select` every minute
(`app/automations/triggers/builtin/schedule/source.py`); each enabled
schedule trigger whose `next_fire_at` has passed gets a run, and
`automation_run_execute` walks the plan (`app/automations/runtime/executor.py`).
The only action is `agent_task` (one agent turn). There is no "Run now"
(`TriggerType.MANUAL` is reserved, not registered): to try an automation,
schedule it a few minutes ahead. Runs are metered against the workspace
Owner's credits. Owners and Editors create and edit automations; Viewers
cannot. An automation that reads mail uses its creator's own Email (IMAP)
connection, and its "Can use" setting must include connected apps or the run
stops as "needs attention".

**"Email me the result"** (builder -> Settings; `execution.email_result`,
off by default). Automations saved before 2026-10-01 predate the switch and
have it off; edit one, switch it on and save. When on, every finished run sends
one email (`app/automations/notify`): the run's final text on success
(subject `<name> — YYYY-MM-DD`), or a short note with the reason on failure,
"needs attention" or a time-out (`... (failed)` / `... (needs attention)` /
`... (timed out)`); a cancelled run sends none (see "Automation runs" above).
Nothing is re-sent or batched. The chat drafter sets it only when asked ("email me the
result").

It goes only to the account email of the person who **created** the
automation, also when an Editor switches it on for someone else's automation.
There is no way to send to any other address: no "send email" action exists,
and the Email (IMAP) connector is read-only, so nothing is ever sent from a
connected mailbox. The mail uses the same SMTP settings and sender as password
reset and invites (`SMTP_*` in `docker/.env`; `Soloclef Notes <SMTP_FROM>`, or
`SMTP_ACCOUNT_FROM`), so there is nothing to set per person; with
`SMTP_ENABLED` off nothing is sent.

**Tracing a missing email.** The worker logs every outcome with the automation
and user ids, never the address:

- `Automation N result email (succeeded) sent to user ...` - the mail server accepted it
- `... not sent: SMTP is off` - `SMTP_ENABLED` is not true in the worker
- `... not sent: creator account ... has no email`
- `... failed: MailerUnavailableError` / `MailerRejectedError` - the server was unreachable, or refused it
- `... could not be sent: ValueError: SMTP_HOST is required ...` - SMTP settings are incomplete

None of them changes the run: it is already saved as succeeded or failed when
the email goes out. No log line at all means the switch is off.

## MCP: tools the agent calls, and Soloclef as an MCP server (2026-10-09)

Two different things share the name.

**In-app MCP servers** (Connectors -> MCP server; tools the Soloclef agent
can call). Who adds what and where it may connect is in "MCP servers people
add themselves" above. In short:

- A workspace Owner or Editor adds their own server (a `member` server: any
  public `https` host, on their own key); an administrator's is a `site`
  server, held to the allow-list. The allow-list is the `mcp_allowed_hosts`
  row of `billing_settings`; with no row it is `mcp.exa.ai`,
  `mcp.tavily.com`, `mcp.browserbase.com`. It has no page in the app: an
  administrator reads and replaces it with `GET` / `PUT
  /api/v1/admin/mcp/allowed-hosts` (`{"hosts": [...]}`) from a signed-in
  session. No stdio, ever.
- Keys are static headers only (url, headers, transport); the hosted-MCP
  OAuth flow was removed in `3bc6b7864` (2026-09-27), so servers that need
  an OAuth sign-in (Notion's or GitHub's hosted MCP, for example) cannot be
  added yet. Keys are write-only: every read shows them masked.
- A server belongs to one workspace and is deleted with it; unlike Nextcloud
  and Email it does not follow its owner into other workspaces. Every member
  of that workspace uses it, on the key (and the bill) of whoever added it.
  A member's own servers are deleted when they leave or are removed.
- "Loaded N MCP tools" in the logs rebuilds the tools from the list saved
  when the server was added; it is not a fresh connection.

On this host the three servers in workspace 1 (Exa, Tavily, Browserbase) were
added by `daniel@danvics.com` before 2026-09-27. They carry no origin marker,
so they count as `site` servers: daniel, who added them, may still edit
them, but only to hosts on the allow-list. As the Owner of workspace 1,
daniel can add his own servers there without an administrator.

**Soloclef as an MCP server** (`soloclef_mcp`, container `mcp`, served at
`https://notes.danvics.com/mcp` through `reverse_proxy /mcp*` and
`/.well-known/oauth-protected-resource*` in `docker/proxy/Caddyfile`). It has
no shared key (`SOLOCLEF_API_KEY` unset). Coding agents (Claude Code,
Cursor, Codex) send their own `ss_pat_` key as `Authorization: Bearer`;
claude.ai and ChatGPT sign in through Authentik ("AI agents sign in with
Authentik" above). Without a credential the answer is `401`, with a
`WWW-Authenticate` challenge pointing at the sign-in metadata when
`MCP_OAUTH_ISSUER` is set. Either way an agent sees only workspaces whose
General settings have API key access on (off by default). Anything placed in
front of the site (for example a forward-auth proxy) must let `/mcp`,
`/.well-known/oauth-protected-resource` and `/api/v1` through, or every
agent stops working.

## Text-to-speech: Kokoro 82M through the proxy (`feat/kokoro-voice`, 2026-11-08)

Production TTS was `openai/openai-gpt-4o-mini-tts`. Every `openai-*` model on
the proxy now answers

```
429 litellm.RateLimitError: OpenAIException - You have no credits remaining.
```

so podcasts and video narration failed. The replacement is Kokoro 82M, served
by OpenRouter and reached the same way as every other hosted model: through
the proxy, with no provider credential in the backend. `docker/.env` is already
switched (owner, 2026-11-08):

```
TTS_SERVICE=openai/openrouter-kokoro-82m-tts
TTS_NARRATION_VOICE=af_heart
TTS_PODCAST_VOICES=af_heart,am_adam
```

and was proved against the running backend: 66,476 bytes for `af_heart`,
66,092 for `am_adam`.

**Nothing in code names a proxy.** The address is `TTS_SERVICE_API_BASE` and
the API dialect is the prefix of `TTS_SERVICE`, so moving from LiteLLM to
Bifrost is that one address. The adapter's file and class are still called
`litellm` for continuity; the only logic anywhere that assumes LiteLLM is the
`/model/info` lookup in `app/podcasts/voices/openai_roster.py`, which asks the
proxy which voices an `openai/*` model has. The Kokoro path does not use it
(its roster is the catalog's), so it is not on the live path; against a proxy
without that endpoint it falls back to a static list and logs.

### The roster bug the live fix worked around

`provider_from_service` mapped a service string to a voice roster by its
prefix, so `openai/openrouter-kokoro-82m-tts` was read as an OpenAI model and
the picker offered `coral` and `ash` -- names Kokoro rejects. The live fix
works only because the voices were set to Kokoro names by hand. A Kokoro voice
name carries its language (`af_*` American female, `bm_*` British male, `ef_*`
Spanish female), so the roster is not cosmetic: the wrong one narrates in the
wrong language.

Kokoro is now matched by model name rather than by prefix, wherever it is
served from (`local/kokoro`, `openai/openrouter-kokoro-82m-tts`,
`openai/local-kokoro-tts`). All 23 Kokoro voices in the catalog are in
OpenRouter's `supported_voices` for `hexgrad/kokoro-82m` (checked 2026-11-08
against `https://openrouter.ai/api/v1/models?output_modalities=speech`, which
lists 54).

### A deadline a render queue can survive

The adapter ran `timeout=600, max_retries=2`: up to about thirty minutes on one
segment, against a route that has been reported to hang for hours, with the
renderer waiting on every segment. `litellm.speech` takes `timeout` as a float
**or an `httpx.Timeout`**, and forwards both it and `max_retries` to the
client, so a read deadline is expressible on the proxy path and nothing needed
to be called directly.

Attempts are now counted in `app/podcasts/tts/attempts.py` rather than inside
the client (`max_retries=0`), so the cap is a number an operator can see:

| Setting | Default | Meaning |
|---------|---------|---------|
| `TTS_REQUEST_TIMEOUT_SECONDS` | 120 | read deadline for one attempt (connect is fixed at 10s) |
| `TTS_MAX_ATTEMPTS` | 3 | attempts per segment, 2s then 4s apart |

Worst case per segment is the product plus backoff -- 366s at the defaults,
against 1800s before -- and the whole sequence also sits under a hard
`asyncio.timeout` at that figure, so a client that never returns is still
bounded. A value that is not a positive number fails naming its setting.
Retried: 408/409/425/429/500/502/503/504/529, connection and timeout faults,
and an answer that carries no audible audio. Not retried: any other status, so
a rejected voice or an unknown model costs one call.

### An answer with no audio is a failed attempt

An empty body was already an error. Two cases were not, and both would have
rendered as a silent slide in a finished video, which is worse than a failed
job:

- a transfer that stopped partway, leaving a short but well-formed MP3;
- an error page read as headerless samples, because the adapter falls back to
  raw PCM for bytes in no container it recognises (Grok Voice needs that), and
  28 bytes of JSON "decode" to a few milliseconds of noise.

`app/podcasts/tts/decoded_length.py` now measures what the provider sent --
decoding it, as the video path measures narration, and by arithmetic for
headerless samples -- and anything under 0.05s, shorter than any spoken line,
is a failed attempt. The measurement is taken before transcoding, because an
encoder pads its last frame and would hide it.

Duration downstream is unchanged: `container` is still `mp3`, so narration is
still ffprobed in the sandbox and podcast segments are still concatenated by
FFmpeg. `app/deliverables/video/narration_budget.py` still assumes 2.4 words
per second, measured against `tts-1-hd` and `gpt-4o-mini-tts`; it is
conservative and the overrun path re-aims from the measured duration, but it is
worth re-measuring for Kokoro.

### No voice nobody chose

`TTS_NARRATION_VOICE` and `TTS_PODCAST_VOICES` used to be replaced by
`LEGACY_NARRATION_VOICE_ID` or the catalog's own pick when the active provider
did not have the configured name, silently -- so a deploy that changed only
`TTS_SERVICE` narrated in a voice nobody chose. A name the provider does not
have now raises, naming the setting. Their defaults (`coral`, `coral,ash`) are
gone from code as well, because a voice name only means something for one
provider: unset means "catalog, you choose", which is a different statement
from a name that cannot work.

A voice that exists but does not speak the language asked for is still a reason
to pick another, not an error -- otherwise a Japanese deck could not be
narrated by a deployment configured with an English voice.

### Switching provider

`TTS_SERVICE` plus the two voice settings. Every proxied model stays
selectable; no code change, no migration, no frontend build, no proxy change.
The on-box pipeline (`local/kokoro`) was also selectable when this was
written; it is gone -- see "No local inference" below.

## No local inference: torch, Kokoro and Faster-Whisper are gone (`feat/drop-torch`, 2026-11-08)

Two capabilities were **removed**, not refactored:

- **On-box text-to-speech.** `TTS_SERVICE=local/kokoro` ran the `kokoro`
  package in this process. The adapter and the package are gone.
- **On-box speech-to-text.** `STT_SERVICE=local/<size>` ran Faster-Whisper in
  this process, and an **unset** `STT_SERVICE` silently meant `local/base`.
  The service and the package are gone.

Nothing was lost in this deployment, which has reached both through the proxy
since 2026-11-08 (`TTS_SERVICE=openai/openrouter-kokoro-82m-tts`,
`STT_SERVICE=openai/groq-whisper-large-v3`). Hosted Kokoro is untouched: its
voice roster, its `provider_from_service` match by model name and all 23
voices stay exactly as they were. A deployment that wants Kokoro or Whisper on
a box again names a served copy of it, as every other model is named.

Both settings now refuse a local name at startup, in the register
`app/embedding_roles/settings.py` already used for a bare embedding model
(`app/speech/hosted_only.py`):

```
TTS_SERVICE='local/kokoro' names a speech model on this box. This server has
no local inference: every speech model is reached through a provider, and ours
go through the proxy at TTS_SERVICE_API_BASE. Set it to a served model, for
example TTS_SERVICE=openai/openrouter-kokoro-82m-tts.
```

An unset `STT_SERVICE` no longer falls back to a local model: an audio upload
fails naming the setting.

### What that removed from the image

`kokoro` was the last package that wanted torch (`unstructured-inference`
went earlier with `unstructured`), so the `cpu` extra, the `pytorch-cpu`
index and the `[tool.uv.sources]` condition that pinned torch to it are all
gone from `pyproject.toml`, together with the `--extra cpu` export and the
second index in the Dockerfile. Resolution proves nothing else wants it:
removing `kokoro` and `faster-whisper` leaves torch in the graph required by
**nothing but this project's own `cpu` extra**. `rapidocr` is ONNX Runtime,
`rerankers[api]` is `requests`, `faster-whisper` was ctranslate2.

24 packages left the lock (335 to 309): torch, torchvision, transformers,
safetensors, kokoro, misaki, phonemizer-fork, espeakng-loader, num2words,
curated-transformers, curated-tokenizers, spacy-curated-transformers,
segments, csvw, faster-whisper, ctranslate2, av, soundfile (only the Kokoro
adapter used it), joblib, loguru, addict, docopt, dlinfo, win32-setctime.
`espeak-ng` and `libsndfile1` left the apt layer with them, and the EasyOCR
model pre-download went too -- EasyOCR is torch-based and only ever arrived
through `unstructured`, so that step was baking 100 MB of weights for a
package the image does not install. RapidOCR's pre-download stays: it is the
OCR backend `pymupdf4llm` actually uses, and it is ONNX.

**Do not re-add the extra without the index condition.** Without
`[tool.uv.sources]`, uv resolves torch from PyPI, which on Linux pulls the
`nvidia-*` CUDA wheels -- gigabytes on a box with no GPU. The way back is the
whole pattern or none of it.

Measured on one daemon, both trees built from matched `git archive` contexts
with `docker build --no-cache --target production`, read from `docker images`
SIZE once the daemon had finished unpacking: **7.26 GB before, 5.46 GB after**
(-1.80 GB). `docker inspect -f '{{.Size}}'` reports a different number for the
same two images -- 1,890,399,240 and 1,400,461,982 bytes -- so the two are not
comparable with each other; the figures above are the `docker images` ones.
The `custom` image on the new base is 5.48 GB.

### The one thing that had to be fixed, not just removed

`EMBEDDING_TOKENIZER` may name a **directory** holding `tokenizer.json` -- which
is exactly what the `custom` image bakes at `/opt/chunk-tokenizer` so no cold
start fetches a vocabulary. Chonkie's `AutoTokenizer` reads a *name*, not a
path; a path resolved only through `transformers`, which was installed because
`kokoro` wanted it. Removing kokoro would therefore have stopped this
deployment at startup with

```
EMBEDDING_TOKENIZER='/opt/chunk-tokenizer' could not be loaded ... 'transformers' library not found.
```

So `app/config/chunk_sizing.py` now opens a baked tokenizer with `tokenizers`
itself (already installed, `chonkie[tokenizers]`) and hands chonkie the
instance. A hub name still loads as before, and a path holding no tokenizer
still stops the server naming the setting. This was caught by probing the
built `custom` image, not by the unit suite.
