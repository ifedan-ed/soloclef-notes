/**
 * Turns the invalidation map's targets (live-query-targets.ts) into TanStack
 * Query invalidations. Invalidating marks every matching read stale and reads
 * again only those a mounted component uses, so a change about a screen
 * nobody has open costs nothing until it is opened.
 */
import type { InvalidateQueryFilters, Query, QueryClient } from "@tanstack/react-query";
import type { LiveTarget, NotebookPart } from "./live-query-targets";

function isNotebookPart(queryKey: readonly unknown[], part: NotebookPart): boolean {
	if (queryKey[0] !== "notebooks" || typeof queryKey[1] !== "number") return false;
	return part === "detail" ? queryKey.length === 2 : queryKey[2] === part;
}

export function toQueryFilters(target: LiveTarget): InvalidateQueryFilters {
	if (target.kind === "key") {
		return {
			queryKey: target.queryKey,
			...(target.exact ? { exact: true } : {}),
			...(target.refetch ? { refetchType: target.refetch } : {}),
		};
	}
	const { part, notebookId } = target;
	if (notebookId !== undefined) {
		// ["notebooks", id] is the start of all of that notebook's keys, so the
		// detail alone must be matched exactly.
		return part === "detail"
			? { queryKey: ["notebooks", notebookId], exact: true }
			: { queryKey: ["notebooks", notebookId, part] };
	}
	return { predicate: (query: Query) => isNotebookPart(query.queryKey, part) };
}

export interface ApplyOptions {
	/**
	 * Only reads whose data is older than this time (ms since epoch). Used when
	 * the stream (re)connects without replay: anything read after it was ready
	 * is covered by the stream already, and reading it again would only repeat
	 * the request a page just made.
	 */
	readBefore?: number;
	/**
	 * Keep a read already in flight instead of restarting it. Right for a
	 * catch-up refresh; wrong for a named change, whose in-flight read may
	 * have started before the change and must be asked again.
	 */
	keepInFlight?: boolean;
}

export function applyLiveTargets(
	queryClient: QueryClient,
	targets: readonly LiveTarget[],
	{ readBefore, keepInFlight = false }: ApplyOptions = {}
): void {
	for (const target of targets) {
		const filters = toQueryFilters(target);
		const scoped: InvalidateQueryFilters =
			readBefore === undefined
				? filters
				: {
						...filters,
						predicate: (query: Query) =>
							query.state.dataUpdatedAt < readBefore && (filters.predicate?.(query) ?? true),
					};
		void queryClient.invalidateQueries(scoped, { cancelRefetch: !keepInFlight });
	}
}
