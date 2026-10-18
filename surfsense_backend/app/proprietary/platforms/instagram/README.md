# Instagram scraper (anonymous)

Platform-native Instagram scraper. **Anonymous-only** and browser-free: every
flow stays on the cheap HTTP tier (`app.utils.proxy` + `scrapling`), and keyword
discovery reuses the `google_search` platform (see below). It exposes a stable
public API whose input/output surface mirrors the public Instagram scraper actor
spec (same `resultsType` / `directUrls` / camelCase field names, additive
`extra="allow"` parity), so callers written against that surface work unchanged.
It is **not** wired into ingestion or Celery — the capability layer under
`app/capabilities/instagram/` turns these primitives into REST + agent + MCP
surfaces.

## Approach

Instagram's public web pages serve anonymous, logged-out data once a session
carries an anonymous `csrftoken` + `mid` cookie pair and the `x-ig-app-id` web
header:

> Warm an anonymous session with one plain GET to `www.instagram.com/` (mints
> `csrftoken` + `mid`), then GET the pages through that same
> Chrome-impersonated, sticky-IP session. Rotate the residential IP + re-warm on
> a login wall (401/403), back off on 429.

Surfaces used:

| Flow | Surface | Extractor |
|---|---|---|
| profile / details | `/<username>/embed/` (counts, the latest **6** posts) + `/<username>/` (biography, following, first bio link) | `parse_profile_embed` + `parse_profile_page` → `merge_profile_users` → `parse_profile` |
| profile feed (posts/reels) | the 6 posts in the same embed page (so at most 6 per profile) | `parse_media` |
| single post / reel | `/p/<shortcode>/` (embedded mobile-v1 `PolarisMedia` JSON, og-meta fallback) | `parse_post` |
| keyword discovery | Google `site:instagram.com <query>` | `resolve_url` |

`api/v1/users/web_profile_info/` answered 401 `require_login` to anonymous
callers from both the home proxy and the server (checked 2026-10-09), so it is
no longer called. Pages that answer without a profile mean "no such account"
(`None`, an empty result); only when both pages are walled does the target
raise `InstagramAccessBlockedError`.

The feed node and the single-post relay blob both carry carousel children
(`images`/`childPosts`), tagged users, coauthor producers, location, product
type, and pin state, when Instagram includes them. Comment **content** stays
login-walled — only the anonymous comment **count** (`commentsCount`) is
exposed, so `firstComment` / `latestComments` are intentionally absent from the
item schema.

**Why anonymous-only is a hard constraint.** Live logged-out probes show that
Instagram walls the interesting endpoints for anyone without a `sessionid`
account cookie: `api/v1/tags/web_info/`, `api/v1/locations/web_info/`, the
comment thread API (`?__a=1`), and `web/search/topsearch/` all **302 to
`/accounts/login/`**. We cannot log in (see below), so hashtag feeds, place
feeds, comment scraping, and IG's native keyword search were **removed** — they
can only ever return a login wall (as `web_profile_info` now does too). What
survives is what a logged-out browser can actually read: a profile's embed and
profile pages, and a public post/reel page's embedded metadata.

## Anonymous only — no authentication, ever

No login, no `sessionid` account cookie, no app password. The only cookies held
are the anonymous `csrftoken` / `mid` minted by the warm-up. There is **no**
authenticate option in the input surface or the fetch layer, by design. A
persistent block after IP rotation surfaces as `InstagramAccessBlockedError`
(mirrors Reddit's `RedditAccessBlockedError`) rather than a silent empty result,
so the capability layer can map it to a `403 INSTAGRAM_ACCESS_BLOCKED`.

## Module map

| File | Responsibility |
|---|---|
| `__init__.py` | Public exports: `InstagramScrapeInput`, item models, `iter_instagram`, `scrape_instagram`, `InstagramAccessBlockedError`. |
| `schemas.py` | `InstagramScrapeInput` (`extra="allow"`, no auth fields) + optional-field item models (`InstagramMediaItem`, `InstagramProfile`) each with `to_output()`. |
| `fetch.py` | The core. Rotate-on-block sticky `_RotatingSession` + `_current_session` ContextVar + `warm_session` (csrftoken/mid) + `fetch_html` (HTML; `fetch_json` is the JSON twin, unused by the flows today) sharing one resilient `_fetch(path, params, extract)` loop. `_build_url` keeps every URL on an Instagram host. |
| `url_resolver.py` | Classify an Instagram URL → `profile`/`post`/`reel`; non-Instagram (and hashtag/place) → `None`. Strips `_u/`, `profilecard/`; story → profile. |
| `parsers.py` | Pure mapping (`parse_media`, `parse_profile`, `parse_profile_embed` / `parse_profile_page` / `merge_profile_users`, `parse_post` [relay `PolarisMedia` JSON, og-meta fallback], `_edges`). I/O-free. |
| `scraper.py` | Orchestrator: `_profile_user`, `_media_flow`/`_details_flow`/`_discover` (+ `_discover_via_google`), `_targets`, `fan_out`, `iter_instagram`, `scrape_instagram`. |

## How it works

1. `iter_instagram` resolves `directUrls` (or runs a discovery `search` per the
   comma-split queries) into targets and fans them out on a pool of warm proxy
   sessions (`fan_out`, 8-way). Each worker opens one sticky-IP session and warms
   `csrftoken`/`mid` once, reusing it across the sequential targets it pulls.
2. `resultsType` selects the flow: `posts`/`reels` → media items,
   `details` → profile metadata. Media items de-dupe by `id` across targets.
   - A **profile** target → `_profile_user` (embed + profile page) →
     `parse_media` over the latest posts (feed, at most 6) or `parse_profile`
     (details). A name that is not a handle (1-30 letters, digits, `.`, `_`)
     is skipped unfetched; a profile already read during discovery is reused.
   - A **post/reel** target → `fetch_html("p/<code>/")` → `parse_post`, which
     reads the embedded mobile-v1 `PolarisMedia` JSON (full fidelity) and falls
     back to Open Graph meta only if that blob is absent. Numeric-ID post URLs are
     skipped (the page keys on the shortCode).
3. `fetch_html` warms the session on first use, rotates the IP + re-warms on
   401/403 (at most 3 rotations), backs off on 429, returns `None` on 404, and
   raises `InstagramAccessBlockedError` on a `/accounts/login/` redirect.
4. Parsers map raw web JSON/HTML to flat dicts; the orchestrator stamps
   `scrapedAt` and applies `resultsLimit` / `onlyPostsNewerThan` as request-time
   policy.

## Keyword discovery (Google-backed)

Instagram's native keyword search is login-walled. `_discover` first reads a
query shaped like a handle as that account (`"messi"` → `instagram.com/messi/`;
the profile read is kept so the flow does not read it again). A one-word query
with no such account, and any other query (e.g. `"national geographic"`), goes
through `_discover_via_google`, which calls the `google_search` platform with
`site:instagram.com`, classifies each organic URL with `resolve_url`, keeps the
**profile** hits plus, for `posts`/`reels` runs, the **post and reel** hits
(Google mostly returns those for a topic), de-dupes, and caps at `searchLimit`
(default 10). A walled account lookup in one comma-separated query does not
sink the others; a run where every lookup was walled raises
`InstagramAccessBlockedError`.

Caveats:

- **Coupling**: Instagram depends on the `google_search` platform. The dependency
  is one-directional and lives behind `_discover_via_google` so it stays testable.
- **Quality**: results reflect Google's index/ranking of `instagram.com`, not
  IG's own relevance. This is discovery, not search parity.

## Observed limits & calibration caveats

- Anonymous web JSON/HTML is rate-limited per IP; the sticky-session pool keeps
  each IP's request rate modest but a hot pool will still hit login walls — that's
  the `InstagramAccessBlockedError` path, not a bug.
- `likesCount` is frequently withheld on anonymous responses (surfaces as `-1` or
  absent upstream); treat it as best-effort.
- **Single-post extraction** reads the mobile-v1 `PolarisMedia` object embedded in
  the public `/p/` document (og-meta is a lossy fallback). If Instagram strips both
  for a given post (private, taken down, or a login interstitial), `parse_post`
  returns `None` — an honest empty, never a fabricated item. ponytail: the
  embedded-blob shape can drift; a live probe that dumps the raw HTML pins it (see
  Testing) and any change is contained to `_find_media` / `parse_post`.
- The `$3.50 / 1k items` default meter assumes the proxy-bytes-per-item measured
  on the reference targets; re-measure with the scale harness before high-volume use.

## Testing

- Offline unit tests: `tests/unit/platforms/instagram/` — `test_skeleton.py`
  (schema + URL resolver), `test_parsers.py` (mapping incl. `parse_post`
  relay-JSON/og shapes; fixture-pinned tests skip when the fixture is absent),
  `test_discovery.py` (account-or-Google discovery with a fake `scrape_serps`),
  `test_profile_pages.py` (embed + profile page parsing from trimmed natgeo
  pages, not-found vs walled), `test_url_building.py` (URLs stay on
  instagram.com), `test_fetch_resilience.py` (warm / rotate / backoff loop +
  fan-out with fake sessions, no network), `test_budget.py` (fair-share caps +
  de-dup).
- Stress / accuracy harness (live, needs network + residential proxy):
  `scripts/stress/stress_instagram_scraper.py` — `--mode live-discovery` (profile
  discovery accuracy), `--mode probe-post` (dumps a real anonymous `/p/` payload
  to `fixtures/post.json` and shows what `parse_post` extracted), `--mode
  probe-mentions` (settles that the tagged/`mentions` feed is login-walled), and
  `--mode accuracy` (field coverage across the profile + single-post flows).

```bash
cd surfsense_backend
uv run pytest tests/unit/platforms/instagram/
# Live single-post probe: confirms /p/ is anonymously extractable + pins the shape
uv run python scripts/stress/stress_instagram_scraper.py --mode probe-post \
  --post https://www.instagram.com/p/<shortcode>/
```

## TODO / out of scope (v1)

- Deep feed pagination past the first web page of profile media (GraphQL cursor
  doc-id).
- Sticky-IP provider parity (same `__sid` caveat as the Reddit sibling).
