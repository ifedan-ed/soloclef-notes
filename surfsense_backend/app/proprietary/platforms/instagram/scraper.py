"""Orchestrator for the Instagram scraper.

The core is the async generator :func:`iter_instagram` (unbounded);
:func:`scrape_instagram` is a thin collector with a caller-supplied ``limit``
guard. Any cap is caller policy, never baked into flow logic.

Independent targets (one per ``directUrl`` / discovered entity) fan out
concurrently on a pool of warm sessions (sticky IPs); each target's own paging
stays sequential. ``fan_out`` is ported from ``../reddit/scraper.py`` but bound
to *this* module's proxy holders so every worker warms its own session once and
reuses it.

Anonymous-only. Every surface here is reachable without a login: the public
profile embed and profile page, single-post/reel pages, and Google-backed
discovery. Login-walled surfaces (hashtag/place feeds, comment threads, IG's
native keyword search, and now ``web_profile_info``) are deliberately absent.

Flows are selected by ``resultsType``:
- ``posts`` / ``reels`` -> media items (profile feed, or a single
  ``/p/``/``/reel/`` page, or discovery search)
- ``details`` -> profile metadata (by URL or discovery search)

ponytail: deep feed pagination (past the first web page of media) needs the
GraphQL cursor endpoint whose doc-id drifts; v1 emits the first page and stops.
The upgrade path is a ``_paginate_feed`` helper in this file plus a doc-id in
``fetch.py`` — contained to these two files, per the acquisition-seam rule.
"""

from __future__ import annotations

import asyncio
import logging
import re
from collections.abc import AsyncIterator, Callable
from contextlib import aclosing
from datetime import UTC, datetime, timedelta
from typing import Any

from app.proprietary.platforms.google_search.schemas import GoogleSearchScrapeInput
from app.proprietary.platforms.google_search.scraper import scrape_serps

from .fetch import (
    InstagramAccessBlockedError,
    bind_proxy_holder,
    fetch_html,
    now_iso,
    open_proxy_holder,
    proxy_session,
)
from .parsers import (
    merge_profile_users,
    parse_media,
    parse_post,
    parse_profile,
    parse_profile_embed,
    parse_profile_page,
)
from .schemas import InstagramScrapeInput
from .url_resolver import ResolvedUrl, resolve_url

logger = logging.getLogger(__name__)

__all__ = [
    "InstagramAccessBlockedError",
    "iter_instagram",
    "scrape_instagram",
]

# Independent jobs run concurrently on a pool of warm proxy sessions. Anonymous
# Instagram is the most hostile platform, so this stays low to avoid burning the
# residential pool with parallel login walls.
_FANOUT_CONCURRENCY = 8

# Instagram usernames: 1-30 chars of letters/digits/period/underscore. Used to
# treat a profile/user discovery query as a direct (anonymous) handle lookup.
_HANDLE_RE = re.compile(r"[A-Za-z0-9._]{1,30}\Z")


def _parse_newer_than(value: str | None) -> datetime | None:
    """Parse ``onlyPostsNewerThan`` (ISO, YYYY-MM-DD, or relative) to UTC.

    Relative forms: ``"<n> <unit>"`` where unit is minute/hour/day/week/month/
    year (singular or plural). Anything unparseable returns ``None`` (no filter).
    """
    if not value:
        return None
    text = value.strip().lower()
    parts = text.split()
    if len(parts) == 2 and parts[0].isdigit():
        n = int(parts[0])
        unit = parts[1].rstrip("s")
        days = {
            "minute": n / 1440,
            "hour": n / 24,
            "day": n,
            "week": n * 7,
            "month": n * 30,
            "year": n * 365,
        }.get(unit)
        if days is None:
            return None
        return datetime.now(UTC) - timedelta(days=days)
    try:
        dt = datetime.fromisoformat(value.replace("Z", "+00:00"))
        if dt.tzinfo:
            return dt
        return dt.replace(tzinfo=UTC)
    except ValueError:
        return None


def _is_after(timestamp: str | None, cutoff: datetime | None) -> bool:
    """True if the item ``timestamp`` (ISO) is at/after the cutoff (or no cutoff)."""
    if cutoff is None:
        return True
    if not timestamp:
        return True
    try:
        dt = datetime.fromisoformat(timestamp.replace("Z", "+00:00"))
        return dt >= cutoff
    except ValueError:
        return True


async def fan_out(
    jobs: list[AsyncIterator[dict[str, Any]]], *, concurrency: int = _FANOUT_CONCURRENCY
) -> AsyncIterator[dict[str, Any]]:
    """Stream items from independent async-iterator jobs via a warm worker pool.

    Each worker opens ONE proxy session and reuses it across the sequential jobs
    it pulls, so only the first job per worker pays the proxy handshake + the
    cookie warm-up. Partial results (matches the reddit sibling): one blocked or
    failed target yields nothing rather than aborting the batch — Instagram is
    an aggregation, not an atomic transaction, so 4/5 good targets beat 0/5. But
    if EVERY target was refused (zero items AND a hard block seen), the whole run
    couldn't reach anonymous data, so we surface ``InstagramAccessBlockedError``
    (-> 403) instead of a misleading empty success. Workers are cancelled and
    their sessions closed if the consumer stops early.
    """
    if not jobs:
        return
    job_queue: asyncio.Queue[AsyncIterator[dict[str, Any]]] = asyncio.Queue()
    for job in jobs:
        job_queue.put_nowait(job)
    results: asyncio.Queue[list[dict[str, Any]]] = asyncio.Queue()
    blocked = False  # set if any target hit a hard login/auth wall

    async def worker() -> None:
        nonlocal blocked
        holder = None
        try:
            holder = await open_proxy_holder()
        except Exception as e:  # no session: jobs still run via one-shot fetches
            logger.warning("[instagram] proxy session open failed: %s", e)
        try:
            while True:
                try:
                    job = job_queue.get_nowait()
                except asyncio.QueueEmpty:
                    return
                items: list[dict[str, Any]] = []
                try:
                    if holder is not None:
                        async with bind_proxy_holder(holder):
                            items = [item async for item in job]
                    else:
                        items = [item async for item in job]
                except InstagramAccessBlockedError as e:
                    # Partial results: a blocked target must not kill the batch.
                    # Record it so a fully-blocked run can still surface the 403.
                    blocked = True
                    logger.warning("[instagram] target blocked: %s", e)
                except Exception as e:  # one bad target must not kill the run
                    logger.warning("[instagram] fan-out job failed: %s", e)
                await results.put(items)
        finally:
            if holder is not None:
                await holder.close()

    tasks = [asyncio.create_task(worker()) for _ in range(min(concurrency, len(jobs)))]
    emitted = 0
    try:
        for _ in range(len(jobs)):
            for item in await results.get():
                emitted += 1
                yield item
    finally:
        for task in tasks:
            if not task.done():
                task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
    # Reached only on natural exhaustion (an early-stop close raises GeneratorExit
    # inside the loop and skips this). Nothing came back AND a wall was hit ->
    # the run was fully refused, so fail loud rather than return empty.
    if emitted == 0 and blocked:
        raise InstagramAccessBlockedError(
            "Instagram refused anonymous access to every target"
        )


def _emit(partial: dict[str, Any], *, input_url: str | None) -> dict[str, Any]:
    """Stamp provenance and serialize (parsers return plain dicts)."""
    out = {**partial, "scrapedAt": now_iso()}
    if input_url is not None:
        out.setdefault("inputUrl", input_url)
    return out


async def _profile_user(username: str) -> dict[str, Any] | None:
    """A profile's ``data.user`` node, or ``None`` when there is no such account.

    Read from the public pages (see ``parsers`` "Anonymous profile pages"):
    ``/<username>/embed/`` gives the counts and the latest posts, and
    ``/<username>/`` adds biography and following. Pages that answered without
    a profile mean no such account: ``None``, at once. Only when Instagram
    walled both pages does this raise :class:`InstagramAccessBlockedError`.

    The ``web_profile_info`` API is not called: it answers 401
    ``require_login`` to anonymous callers (checked 2026-10-09 from the home
    proxy and the server), so as a fallback it only cost 4 requests and 3
    rotations and then reported a missing account as "blocked".

    A name that is not an Instagram handle (``_HANDLE_RE``) is never fetched:
    the handle becomes part of the URL path.
    """
    if not _HANDLE_RE.match(username):
        logger.info("[instagram] %r is not an Instagram handle; skipped", username)
        return None
    embed, embed_walled = await _page_user(f"{username}/embed/", parse_profile_embed)
    page, page_walled = await _page_user(
        f"{username}/", lambda html: parse_profile_page(html, username)
    )
    if embed is not None or page is not None:
        return merge_profile_users(embed, page)
    if embed_walled and page_walled:
        raise InstagramAccessBlockedError(
            f"Instagram walled both public pages of {username}"
        )
    return None


async def _page_user(
    path: str, parse: Callable[[str | None], dict[str, Any] | None]
) -> tuple[dict[str, Any] | None, bool]:
    """Parse one public profile document: ``(user or None, walled)``."""
    try:
        return parse(await fetch_html(path)), False
    except InstagramAccessBlockedError as e:
        logger.info("[instagram] %s walled: %s", path, e)
        return None, True


async def _known_profile(
    username: str, profiles: dict[str, dict[str, Any]] | None
) -> dict[str, Any] | None:
    """The profile discovery already read for this run, else a fresh read."""
    user = (profiles or {}).get(username.lower())
    return user if user is not None else await _profile_user(username)


def _media_matches(item: dict[str, Any], result_type: str) -> bool:
    """Filter a media item by feed type. ``reels`` keeps clips/videos only."""
    if result_type == "reels":
        return item.get("type") == "Video" or item.get("productType") == "clips"
    return True


async def _media_flow(
    resolved: ResolvedUrl,
    *,
    input_model: InstagramScrapeInput,
    cutoff: datetime | None,
    per_target: int,
    profiles: dict[str, dict[str, Any]] | None = None,
) -> AsyncIterator[dict[str, Any]]:
    """Emit media items for a profile feed, or a single ``/p/``/``/reel/`` page."""
    from .parsers import _edges

    result_type = input_model.resultsType
    if resolved.kind == "profile":
        user = await _known_profile(resolved.value, profiles)
        if user is None:
            return
        nodes = _edges(user.get("edge_owner_to_timeline_media"))
        emitted = 0
        for node in nodes:
            item = parse_media(node)
            if input_model.skipPinnedPosts and item.get("isPinned"):
                continue
            if not _media_matches(item, result_type):
                continue
            if not _is_after(item.get("timestamp"), cutoff):
                continue
            yield _emit(item, input_url=resolved.url)
            emitted += 1
            if emitted >= per_target:
                return
        return
    if resolved.kind in ("post", "reel"):
        # Single-post extraction: the anonymous ``?__a=1`` JSON API 404s/login-
        # walls, but the public /p/<code>/ document embeds the mobile-v1
        # PolarisMedia JSON (og-meta fallback), which parse_post reads. Numeric-ID
        # URLs can't be keyed this way (the page needs the shortCode), so they're
        # skipped upstream.
        if resolved.numeric_post_id:
            return
        html = await fetch_html(f"p/{resolved.value}/")
        item = parse_post(html, url=resolved.url, shortcode=resolved.value)
        if item is None:
            return
        if not _media_matches(item, result_type):
            return
        if not _is_after(item.get("timestamp"), cutoff):
            return
        yield _emit(item, input_url=resolved.url)
        return


async def _details_flow(
    resolved: ResolvedUrl,
    *,
    input_model: InstagramScrapeInput,
    profiles: dict[str, dict[str, Any]] | None = None,
) -> AsyncIterator[dict[str, Any]]:
    """Emit one profile detail item for a URL (see :func:`_profile_user`)."""
    if resolved.kind == "profile":
        user = await _known_profile(resolved.value, profiles)
        if user is not None:
            yield _emit(parse_profile(user), input_url=resolved.url)


def _kind_matches(
    resolved: ResolvedUrl, search_type: str, *, with_media: bool = False
) -> bool:
    """True if a resolved IG URL is the kind the discovery query asked for.

    Profiles always (hashtag/place feeds are login-walled, so every supported
    ``search_type`` maps to a profile). A run that wants posts or reels also
    keeps single post/reel pages: Google's ``site:instagram.com`` results for a
    topic are mostly posts and reels, and those pages read anonymously.
    """
    if resolved.kind == "profile":
        return True
    return with_media and resolved.kind in ("post", "reel")


async def _discover_via_google(
    query: str, *, search_type: str, limit: int, with_media: bool = False
) -> list[ResolvedUrl]:
    """Discover IG targets via Google ``site:instagram.com`` (anonymous).

    Instagram's own keyword search is login-walled, so we reuse the existing
    ``google_search`` platform, classify each organic URL with ``resolve_url``,
    keep the profile hits (plus post/reel hits when ``with_media``), de-dup,
    and cap at ``limit``.

    Quality caveat: results reflect Google's index/ranking of instagram.com, not
    IG's own relevance. This is discovery, not search parity (see README).
    """
    serps = await scrape_serps(
        GoogleSearchScrapeInput(
            queries=query, site="instagram.com", maxPagesPerQuery=1
        ),
        limit=1,
    )
    resolved: list[ResolvedUrl] = []
    seen: set[tuple[str, str]] = set()
    for serp in serps:
        for org in serp.get("organicResults") or []:
            url = org.get("url", "") if isinstance(org, dict) else ""
            r = resolve_url(url)
            if r is None or not _kind_matches(r, search_type, with_media=with_media):
                continue
            key = (r.kind, r.value)
            if key in seen:
                continue
            seen.add(key)
            resolved.append(r)
            if len(resolved) >= limit:
                return resolved
    return resolved


async def _discover(
    query: str,
    *,
    search_type: str,
    limit: int,
    with_media: bool = False,
    profiles: dict[str, dict[str, Any]] | None = None,
) -> list[ResolvedUrl]:
    """Resolve a discovery query into targets - anonymously.

    A query shaped like a handle is first read as that account ("messi" ->
    instagram.com/messi/); the profile read here goes into ``profiles`` so the
    flow does not read it again. A one-word query with no such account, and
    any other query (e.g. "national geographic"), goes through Google
    ``site:instagram.com``, since IG's native keyword search is login-walled.
    Raises :class:`InstagramAccessBlockedError` when Instagram walled the
    account lookup.
    """
    handle = query.strip().lstrip("@")
    if _HANDLE_RE.match(handle):
        async with proxy_session():
            user = await _profile_user(handle)
        if user is not None:
            if profiles is not None:
                profiles[handle.lower()] = user
            url = f"https://www.instagram.com/{handle}/"
            return [ResolvedUrl("profile", handle, url)][:limit]
        logger.info("[instagram] no account named %r; searching Google", handle)
    return await _discover_via_google(
        query, search_type=search_type, limit=limit, with_media=with_media
    )


def _resolve_inputs(input_model: InstagramScrapeInput) -> list[ResolvedUrl]:
    """Resolve ``directUrls`` (URLs take priority over ``search``)."""
    resolved: list[ResolvedUrl] = []
    for url in input_model.directUrls:
        r = resolve_url(url)
        if r is None:
            logger.warning("[instagram] unrecognized URL: %s", url)
            continue
        resolved.append(r)
    return resolved


async def _targets(
    input_model: InstagramScrapeInput,
    profiles: dict[str, dict[str, Any]] | None = None,
) -> list[ResolvedUrl]:
    """The resolved targets for this run: direct URLs, else discovery search.

    Like :func:`fan_out`, a walled query does not sink the others; only a run
    where nothing was found and some lookup was walled raises.
    """
    if input_model.directUrls:
        return _resolve_inputs(input_model)
    if not input_model.search:
        return []
    limit = input_model.searchLimit or 10
    queries = [q.strip() for q in input_model.search.split(",") if q.strip()]
    targets: list[ResolvedUrl] = []
    with_media = input_model.resultsType != "details"
    walled: InstagramAccessBlockedError | None = None
    for query in queries:
        try:
            found = await _discover(
                query,
                search_type=input_model.searchType,
                limit=limit,
                with_media=with_media,
                profiles=profiles,
            )
        except InstagramAccessBlockedError as e:
            logger.warning("[instagram] lookup for %r blocked: %s", query, e)
            walled = e
            continue
        targets.extend(found)
    if not targets and walled is not None:
        raise walled
    return targets


async def iter_instagram(
    input_model: InstagramScrapeInput,
) -> AsyncIterator[dict[str, Any]]:
    """Yield flat Instagram items. ``directUrls`` override ``search``.

    Independent targets fan out concurrently; each target's paging stays
    sequential. De-dupes media by ``id`` across targets.
    """
    profiles: dict[str, dict[str, Any]] = {}  # read during discovery
    targets = await _targets(input_model, profiles)
    if not targets:
        return
    result_type = input_model.resultsType
    cutoff = _parse_newer_than(input_model.onlyPostsNewerThan)
    per_target = input_model.resultsLimit or 10

    if result_type == "details":
        jobs = [
            _details_flow(r, input_model=input_model, profiles=profiles)
            for r in targets
        ]
        async with aclosing(fan_out(jobs)) as stream:
            async for item in stream:
                yield item
        return

    # posts / reels -> media feeds, de-duped by id across targets.
    jobs = [
        _media_flow(
            r,
            input_model=input_model,
            cutoff=cutoff,
            per_target=per_target,
            profiles=profiles,
        )
        for r in targets
    ]
    seen: set[str] = set()
    async with aclosing(fan_out(jobs)) as stream:
        async for item in stream:
            item_id = item.get("id")
            if isinstance(item_id, str):
                if item_id in seen:
                    continue
                seen.add(item_id)
            yield item


async def scrape_instagram(
    input_model: InstagramScrapeInput, *, limit: int | None = None
) -> list[dict[str, Any]]:
    """Collect :func:`iter_instagram` into a list, honoring an optional ``limit``.

    ``limit`` is a request-time policy guard, NOT a ceiling in the streaming
    core.
    """
    from app.capabilities.core.progress import emit_progress

    results: list[dict[str, Any]] = []
    async with aclosing(iter_instagram(input_model)) as stream:
        async for item in stream:
            results.append(item)
            emit_progress("scraping", current=len(results), total=limit, unit="item")
            if limit is not None and len(results) >= limit:
                break
    return results
