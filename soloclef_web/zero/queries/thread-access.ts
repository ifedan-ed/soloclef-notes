import type { Context } from "@/types/zero";
import { constrainToAllowedSpaces, denySpace } from "./authz";

type ThreadQuery = {
	where: (...args: unknown[]) => ThreadQuery;
};

type Conditions = {
	cmp: (column: string, opOrValue: unknown, value?: unknown) => unknown;
	and: (...conditions: unknown[]) => unknown;
	or: (...conditions: unknown[]) => unknown;
	exists: (relationship: string, cb?: (query: never) => unknown) => unknown;
};

/**
 * The threads ctx's user may read through Zero: the REST thread routes' rule
 * (check_thread_access and list_threads in new_chat_routes.py), inside the
 * workspaces they belong to. Their own threads, any thread shared with the
 * workspace (visibility SEARCH_SPACE), and, in a workspace they own, a thread
 * whose creator is gone (created_by_id NULL). Another member's private thread
 * never: its messages, comments and session state stay with its creator.
 *
 * Runs on the server (the query route builds every client's query from these
 * definitions), so it holds for every client, the phone app's builds included.
 */
export function constrainToReadableThreads<T extends ThreadQuery>(query: T, ctx: Context): T {
	const inSpaces = constrainToAllowedSpaces(query, ctx);
	const userId = ctx?.userId;
	if (!userId) return denySpace(inSpaces);
	const allowed = new Set(ctx?.allowedSpaceIds ?? []);
	const owned = (ctx?.ownedSpaceIds ?? []).filter((id) => allowed.has(id));
	return inSpaces.where(({ cmp, and, or }: Conditions) =>
		or(
			cmp("createdById", userId),
			cmp("visibility", "SEARCH_SPACE"),
			...owned.map((id) => and(cmp("createdById", "IS", null), cmp("workspaceId", id)))
		)
	) as T;
}

/**
 * A Studio output's live row (podcast_runs, video_presentation_runs,
 * deliverable_jobs) for those who may read the thread that made it, inside
 * the workspaces ctx's user belongs to. Each row names its thread
 * (threadId) and carries what the thread's chat shows of it: the title, the
 * brief, progress, the error. Rows of another member's private chat (or of
 * their Studio request, which runs in a private thread of its own) reached
 * every member through bySpace before (custom/README.md, "Security fixes of
 * 2026-10-11", follow-up). A row with no thread (a notebook's audio
 * overview, or an output whose chat was deleted) stays the workspace's, as
 * the REST routes have it.
 *
 * Runs in the query route like constrainToReadableThreads, so it holds for
 * every client; the phone app does not ask for these rows.
 */
export function constrainToOutputsOfReadableThreads<T extends ThreadQuery>(
	query: T,
	ctx: Context
): T {
	const inSpaces = constrainToAllowedSpaces(query, ctx);
	if (!ctx?.userId) return denySpace(inSpaces);
	return inSpaces.where(({ cmp, or, exists }: Conditions) =>
		or(
			cmp("threadId", "IS", null),
			exists("thread", (thread: never) => constrainToReadableThreads(thread as ThreadQuery, ctx))
		)
	) as T;
}
