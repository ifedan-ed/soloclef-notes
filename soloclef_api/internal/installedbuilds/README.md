# Installed phone builds

A phone keeps the app it installed from the App Store (or the Android preview
APK) until its owner updates it, and an over-the-air update only replaces the
JavaScript bundle within one native build, at the app's next start. So a
server change can break a phone the project no longer ships to, and while an
update is new, phones run both its bundle and the one before. This package
holds the server to every bundle still installed, and the tests fail before a
phone does.

## What is held

`snapshots/<build>-<commit>/` has one directory per installed bundle: a
build and the commit its update was published from (or will be, while
`build.json` says `"published": false`):

| File | What it is |
| --- | --- |
| `build.json` | Which build, which platform and version, the commit the update was published from (`source_commit`), the branch the next update comes from, whether it is published, the update's ids once it is (`update_ids`, what phones running it report), and the sha256 of its `openapi.json` |
| `openapi.json` | The app's own `soloclef_mobile/openapi.json` at that commit, byte for byte: the document the build's client was typed from. Its sha256 must equal the app's `src/lib/api/openapi.sha256` |
| `calls.json` | Every route the build's source calls, with the query values it sends where the source pins them (the Inbox's `category=status|announcements`), and where it calls each |
| `resolutions.json` | The paths the source mentions that the extractor cannot read as calls (a stream, a download, a page opened in the browser), resolved by hand with the reason. A resolution matches its file and text, so the next update's snapshot reuses it where lines moved |
| `exceptions.json` | Optional: the documented ways the server's answer to this bundle differs from the snapshot on purpose (below) |

Only the calls pin the server. An operation in `openapi.json` the build never
calls does not.

The tests in `cmd/api`:

- `TestEveryRouteAnInstalledBuildCallsIsServed`: every call is a served route
  (`servedRoutes()`). No database.
- `TestEveryCallOfAnInstalledBuildIsInTheContract` and
  `TestEveryContractCaseIsACallABuildMakes`: every call, and every pinned
  query value, is sent by a case of the contract test, and every case is a
  call a build makes. No database.
- `TestInstalledBuildsAgainstTheRealServer`: every case is sent through
  `serve()`, the whole server, against a migrated throwaway Postgres and
  Valkey. An answer must be the status the build needs, never a 5xx (unless
  the case names a service the test does not run, such as the model proxy),
  and every build that makes the call must be able to read it: each field its
  `openapi.json` marks required is there, with a type it allows, and enum
  values it knows. The cases are in `cmd/api/installed_builds_area_*_test.go`.

And here:

- `TestEveryCompatibilityAnswerIsNeededByAnInstalledBuild` and
  `TestCompatibilityCodeIsMarkedWhereItIs`: see below.

A failure names the build that breaks, the request, and what it would read:

```
GET /api/v1/notifications/unread-counts-batch (inbox): iPhone 1.1.0 (App Store build 4),
running the update from 731477d8a (snapshots/ios-1.1.0-build4) breaks:
announcements: is missing; the build requires it
```

## Running the contract test

It needs a Postgres migrated with the migrate command (the server checks the
schema version at start) and a Valkey, both throwaway: every account in the
database is deleted.

```bash
docker run -d --name contract-pg -p 127.0.0.1:55432:5432 --tmpfs /var/lib/postgresql/data \
  -e POSTGRES_USER=contract -e POSTGRES_PASSWORD=contract -e POSTGRES_DB=contract pgvector/pgvector:pg17
docker run -d --name contract-valkey -p 127.0.0.1:56379:6379 valkey/valkey:9.1.2-alpine
export SOLOCLEF_API_TEST_CONTRACT_DATABASE='postgres://contract:contract@127.0.0.1:55432/contract?sslmode=disable'
export SOLOCLEF_API_TEST_CONTRACT_VALKEY='redis://127.0.0.1:56379/0'
printf throwaway-secret > /tmp/migrate-secret
(cd soloclef_schema && SOLOCLEF_MIGRATE_SECRET_KEY_FILE=/tmp/migrate-secret \
  SOLOCLEF_MIGRATE_DATABASE_URL="$SOLOCLEF_API_TEST_CONTRACT_DATABASE" go run ./cmd/migrate up)
(cd soloclef_api && go test ./cmd/api -run InstalledBuild -count=1)
docker rm -f contract-pg contract-valkey
```

Without the two variables `go test ./...` skips it. CI sets them and
`SOLOCLEF_API_TEST_CONTRACT_REQUIRED=true` (`.forgejo/workflows/go.yml`, job
`installed-builds`), which makes a missing one a failure, and a test checks
the workflow still sets all three.

## Compatibility answers

Code the server keeps only for an installed build (a field the current apps
no longer read, a retired tab answered as empty, a removed route answered
with a refusal the build understands) is listed in `compat.go`, with the
calls that need it, why, and the files it is in. Each of those files carries
the line

```go
// installed-builds: <id>
```

beside the code. The tests hold both ways: every answer is needed by at
least one snapshot, and every marker names a listed answer. Remove a
snapshot and the test lists each answer no build needs any more, with every
file to delete it from.

A call may also be answered by not being served, when that is right for the
bundle: `Retired` in `compat.go` lists each such call with the answer it
gets (the router's 404 or 405, or the gate's 401 for a link page asked without a
credential) and what the bundle does with it. A retired call's contract case
expects that answer,
the route test fails if the route is served again, and an entry no snapshot
calls any more fails until it is deleted. Anything else an installed build
calls and the server does not serve is a broken phone.

## Exceptions

A bundle's `exceptions.json` lists the only two ways an answer may differ
from its snapshot:

- `fields`: response fields the snapshot requires that the server no longer
  sends, because the bundle never reads them at run time, or reads them only
  to a result that is right without them. `reads_checked` cites each read
  and that the bundle does not check answers at run time; the bundles from
  the updates of 2026-11-14 on check every answer against their
  `openapi.json`, so for them a missing required field is a broken screen
  and never an exception.
- `status`: an answer the bundle gets instead of the one it needs, because
  the bundle sends something wrong and an update fixes it. `fixed_by` names
  the commit of that update; a phone running the bundle gets the fix at its
  next start once the update is published.

The contract test fails on an exception no answer needed, so each goes as
soon as the server or the bundle stops differing.

## Releasing a phone update or build

Before an over-the-air update is published to an installed build, add a
snapshot for its commit beside the current one (a phone runs the current
bundle until its next start after the update): a directory
`<build>-<commit>` with a `build.json` like the current one's naming the new
`source_commit` and `"published": false`, and the current
`resolutions.json`, then:

```bash
soloclef_api/internal/installedbuilds/takesnapshot/take-snapshot.sh \
  soloclef_api/internal/installedbuilds/snapshots/<build> <soloclef_mobile>/node_modules/typescript
```

It reads the commit with git, never a working tree, refuses an
`openapi.json` the app's types were not generated from, and stops on any
path it cannot read as a call until `resolutions.json` resolves it. Then run
the tests: a call the server does not serve, or answers in a way the bundle
cannot read, fails before the update ships. Once it is published, set
`"published": true` and `update_ids` to the update's ids (one per platform,
from `eas update` or the EAS dashboard), and retire the bundles it replaces
under the rule below.

A new store build gets a snapshot when it is released, the same way.

## Retiring a snapshot

A bundle's snapshot may go when both hold:

1. a newer update has been published to its build (or a newer version is
   in the store), so a phone that starts the app leaves the bundle; and
2. no phone has reported running the bundle for 30 days
   (`appversions.RetirementWindow`).

Phones report what they run when they register for push, on every start of
the app (`push_devices`: `app_version`, `runtime_version`, `update_id`,
`last_seen_at`). A bundle from the updates of 2026-11-14 on reports its
update's id, which `build.json`'s `update_ids` names, so it retires by its
own row. Bundles before then report nothing, and are counted together as
"an older bundle" (no app version): 1.0.0's and 1.1.0's first bundles and
their updates up to 2026-11-14 retire together, once no phone without a
version has been seen for 30 days. A phone that never allowed notifications
never registers and is not counted, which is why the window is long.

```bash
docker exec <api container> soloclef-api app-versions
```

or, in SQL:

```sql
SELECT platform, app_version, runtime_version, update_id,
       count(*) FILTER (WHERE last_seen_at >= now() - interval '30 days') AS seen_in_30_days,
       max(last_seen_at) AS last_seen
FROM push_devices
GROUP BY platform, app_version, runtime_version, update_id
ORDER BY platform, app_version NULLS FIRST, runtime_version NULLS FIRST, update_id NULLS FIRST;
```

To retire: delete the snapshot directory, run
`go test ./internal/installedbuilds ./cmd/api` and the contract test, and
delete every compatibility answer, retirement, exception and contract case
the failures name.
