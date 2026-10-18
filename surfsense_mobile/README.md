# Soloclef Notes mobile

The Android and iOS app for a self-hosted Soloclef Notes. It asks for your
server address on first launch. Notebooks-first, like the web app: a notebook
has its sources, one chat, and a Studio of generated outputs.

Expo SDK 57 · React Native 0.86 · TypeScript · expo-router · TanStack Query · zod.

**iPhone beta:** <https://testflight.apple.com/join/1tjGWu1Q>

## What it does

| Screen | |
|---|---|
| Sign in | Server address (asks on first launch, or set `EXPO_PUBLIC_DEFAULT_SERVER_URL`), email + password. Same session flow as the desktop app: `POST /auth/desktop/login` → access + refresh token, kept in the platform keystore (expo-secure-store). Refresh via `POST /auth/jwt/refresh` (rotating, one shared refresh for concurrent 401s, refreshed ahead of expiry); sign-out revokes the device's refresh token (`POST /auth/jwt/revoke`). |
| Notebooks | All / Recent, search, create (opens an untitled notebook, as on the web), rename, delete (confirmation inside a sheet). Pull to refresh. |
| Notebook → Sources | Enable/disable each source or all; indexing status (polls every 5 s while anything indexes, as the web does); add a web link (imported like a research result; YouTube links as videos); upload files from the device with the web's Local/Cloud parser choice and its file-type rules; long-press to remove; tap for the source guide. |
| Notebook → Chat | One chat per notebook (latest thread, created if none). Streams `POST /api/v1/new_chat` over `expo/fetch` with the web stream-engine's event rules; numbered citations open the cited chunk; reasoning and tool steps shown compactly; stop; retry. Empty state with the notebook's three suggested questions (or the guided starts), follow-up suggestions under the latest answer, `@` to narrow a question to one of the notebook's sources. |
| Notebook → Studio | Every output type with the web's option dialogs (described cards, one-line pills with "Default: X" beside the label, report templates, language, source picker, focus box with three rotating ideas + "More ideas"). Output list polls every 4 s while generating; rename / delete / try again. |
| Output viewers | Audio overview: downloaded once, played on device with background playback and lock-screen controls, speed, skip; transcript below. Video: native player (fullscreen, PiP). Flashcards and quiz: native. Mind map: native collapsible outline. Infographic: native image. Interactive report: WebView fed the fetched page. Documents, slides, tables: text shown natively, "Open in another app" and Share. |
| Settings | Account, server, workspace (when there is more than one), theme (system/light/dark), language (device or one of the web's six). |

Strings come from the web's `surfsense_web/messages/*.json` (copied by
`npm run sync-messages`; a test fails when the copy is stale). App-only strings
are in `src/lib/i18n/mobile-en.ts` and `mobile-translations.ts`.

## Your own server

The app is not tied to the original server: on the sign-in screen, **Change
server** takes any https address that runs this backend. **One build works
for every server**; you do not need a separate build per server. To make
your server the default in a build of your own, set it when you build (Expo
inlines `EXPO_PUBLIC_*` at build time):

```bash
EXPO_PUBLIC_DEFAULT_SERVER_URL=https://notes.example.com \
  npx eas-cli build -p ios --profile production
```

The same variable works for `scripts/build-android.sh` and for
`npx expo run:{android,ios}`. The backend must answer
`GET /api/v1/sign-in-options`; an older server that does not is refused as not
a Soloclef server. Push notifications need `SURFSENSE_PUBLIC_URL` on the
server to be the exact https address people type here. The bundle id
(`com.example.soloclefnotes`) and app name are placeholders; change them in
`app.json` before publishing your own build.

## Develop

```bash
npm ci
npm run check            # typecheck + lint + unit tests
npx expo run:android     # needs a local Android SDK, or use the Docker build below
```

Tests:

- `npm test` — unit tests (API client and refresh, SSE parsing, the stream
  message builder, chat/Studio request bodies, Markdown, mind map, i18n key
  coverage) plus a **contract test** that checks the zod schemas, the routes
  and the request bodies against the backend's OpenAPI document
  (`src/lib/api/__tests__/openapi.snapshot.json`). Refresh the snapshot from a
  running backend with `node scripts/fetch-openapi.mjs http://127.0.0.1:8000`.
- `npm run test:integration` — the real API client against a real backend:
  the hermetic e2e stack (`docker/docker-compose.e2e.yml`: Postgres, Redis,
  FastAPI, Celery, fake LLMs, no egress). It registers a throwaway user, so
  never point it at a real server. The compose file publishes port 8000,
  which the live stack already uses; there, run
  it under another project name and port (a copy of the file with
  `name: surfsense-mobile-e2e` and `127.0.0.1:18000:8000` was used for v1):

  ```bash
  docker compose -f ../docker/docker-compose.e2e.yml up -d --build --wait
  SURFSENSE_E2E_URL=http://127.0.0.1:8000 npm run test:integration
  docker compose -f ../docker/docker-compose.e2e.yml down -v
  ```

- `SURFSENSE_LIVE_URL=https://notes.example.com npm run test:integration -- live`
  — read-only probes of a real server without an account (API reachable,
  unknown credentials refused with `LOGIN_BAD_CREDENTIALS`, an unknown refresh
  token ends the session). Creates nothing.

`npx expo start --web` also runs the app in a browser for quick UI checks
(the session is kept in localStorage there, see `src/lib/storage.web.ts`);
the backend's CORS already allows `localhost` origins.

## Android: local release APK (no Expo account needed)

`scripts/build-android.sh` runs `expo prebuild` on the host and Gradle inside
`reactnativecommunity/react-native-android` (no Android SDK on the host). The
release is signed with a keystore kept **outside** the repo:

```
$HOME/.soloclef-mobile-secrets/
  surfsense-release.jks       # PKCS12, alias "surfsense"
  gradle.properties           # SURFSENSE_RELEASE_* (store path + passwords)
```

The signing hook is `plugins/with-release-signing.js`; without those
properties the build falls back to the debug key. Keep the keystore and its
passwords backed up: Android only accepts updates signed with the same key.

```bash
scripts/build-android.sh
# → $HOME/soloclef-mobile-builds/surfsense-notes-<version>-<date>.apk
```

Install by opening the APK on the phone (allow "install unknown apps" for the
browser/file manager once), or `adb install -r <apk>`.

## iOS (built in the cloud by EAS; no Mac needed)

The project is EAS-ready (`app.json`: bundle id `com.example.soloclefnotes`,
background audio; `eas.json`: `preview` and `production` profiles). What the
owner has to do — none of these accounts exist yet:

1. Create an **Expo account** (expo.dev) and, for App Store / TestFlight, an
   **Apple Developer Program** membership (paid, yearly).
2. From this folder: `npx eas-cli@latest login`, then `npx eas-cli init`
   (links the project; adds `extra.eas.projectId` to `app.json` — commit it).
3. Build: `npx eas-cli build -p ios --profile production`. EAS asks for the
   Apple ID and creates the distribution certificate, the provisioning profile
   and the `com.example.soloclefnotes` App ID for you (let it manage credentials).
   - For devices without TestFlight: `--profile preview` (ad hoc; register the
     iPhone first with `npx eas-cli device:create`).
4. Ship to TestFlight: `npx eas-cli submit -p ios --latest` (needs an App Store
   Connect app record for `com.example.soloclefnotes`; EAS offers to create it).

Android can also be built by EAS (`npx eas-cli build -p android --profile
preview` for an APK, `production` for a Play Store bundle). If you do, upload
the existing keystore with `npx eas-cli credentials` so updates keep the same
signature as the locally built APK.

## Layout

```
src/app/            screens (expo-router)
src/components/     UI kit, chat, sources, studio, viewers
src/lib/api/        client (auth/refresh), endpoints, zod schemas, errors
src/lib/chat/       SSE parser, stream message builder, request body, chat hook
src/lib/studio/     Studio catalog + request (ports of the web's)
src/lib/i18n/       ICU subset, translator, synced web messages
plugins/            config plugins (release signing)
scripts/            build-android.sh, sync-messages.mjs, fetch-openapi.mjs
```

Owner preferences this app follows: no loading flashes (lists keep their data
while refetching; spinners wait 250 ms), no system alert dialogs (confirmations
are steps inside sheets, errors are inline), safe areas on every screen, 16 px
inputs, one-line segmented pills.
