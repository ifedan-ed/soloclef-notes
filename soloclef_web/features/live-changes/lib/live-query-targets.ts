/**
 * The invalidation map: which of the app's cached REST reads a change makes
 * stale. This file is the whole of the app's knowledge of the stream's change
 * types; a type it does not know refreshes nothing (the contract's forward
 * compatibility), and a new screen that shows live data adds its key here.
 *
 * Targets are plain data, so the map can be read and tested without a query
 * client; apply-live-targets.ts turns them into TanStack filters.
 */
import { artifactListQueryKey } from "@/features/artifacts/api/artifact-queries";
import { documentViewQueryKey } from "@/features/documents/viewer/document-view-query";
import { notebookKeys } from "@/features/notebooks/api/notebook-query-keys";
import { notesKeys } from "@/features/notes/api/notes-query-keys";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import type { LiveChange } from "../model/live-change";

/** A notebook's own reads, all keyed ["notebooks", notebookId, ...] (notebook-query-keys.ts). */
export type NotebookPart =
	| "detail"
	| "sources"
	| "outputs"
	| "threads"
	| "output-view"
	| "report-view";

export type LiveTarget =
	/**
	 * Every cached read whose key starts with `queryKey` (only that key with
	 * `exact`). `refetch: "none"` marks them stale without reading them now.
	 */
	| { kind: "key"; queryKey: readonly unknown[]; exact?: true; refetch?: "none" }
	/** One part of one notebook, or of every notebook when the change names none. */
	| { kind: "notebook"; part: NotebookPart; notebookId?: number };

/** The change kinds the server sends (soloclef_notify/livechange, Kind). */
export const LIVE_CHANGE_TYPES = [
	"documents",
	"folders",
	"notebooks",
	"outputs",
	"notes",
	"inbox",
	"chats",
	"automations",
	"connectors",
] as const;

const key = (queryKey: readonly unknown[], exact?: true): LiveTarget =>
	exact ? { kind: "key", queryKey, exact } : { kind: "key", queryKey };
const notebook = (part: NotebookPart, notebookId?: number): LiveTarget =>
	notebookId === undefined ? { kind: "notebook", part } : { kind: "notebook", part, notebookId };
/** A key family's first element: every key of that family starts with it. */
const familyOf = (queryKey: readonly unknown[]): readonly unknown[] => [queryKey[0]];

function documentTargets(id: number | undefined): LiveTarget[] {
	return [
		// Every document list, the source-stage poll and the type counts.
		key(familyOf(cacheKeys.documents.globalQueryParams({}))),
		key(familyOf(cacheKeys.documents.withQueryParams({}))),
		// The @-mention picker's title search (document-mention-picker.tsx).
		key(["document-titles"]),
		...(id === undefined
			? [key(familyOf(cacheKeys.documents.document(""))), key(familyOf(documentViewQueryKey(0)))]
			: [key(cacheKeys.documents.document(String(id))), key(documentViewQueryKey(id))]),
		// Sources are documents and folders; a notebook card counts them.
		key(notebookKeys.list(), true),
		notebook("detail"),
		notebook("sources"),
		// Not the document's editor content (document-content-query.ts): it is
		// read once for the editor, and reading it again while someone types
		// would replace their words.
	];
}

function notebookTargets(id: number | undefined): LiveTarget[] {
	return [
		key(notebookKeys.list(), true),
		key(familyOf(notebookKeys.search(""))),
		notebook("detail", id),
		notebook("sources", id),
	];
}

function outputTargets(notebookId: number | undefined): LiveTarget[] {
	return [
		notebook("outputs", notebookId),
		notebook("output-view", notebookId),
		notebook("report-view", notebookId),
		// A chat's cards: podcast runs, video jobs and saved artifacts. Their
		// keys do not say which notebook they belong to, so every one is read
		// again; only the cards on screen are active and fetch.
		key(familyOf(cacheKeys.podcasts.detail(0))),
		key(familyOf(cacheKeys.deliverableJobs.detail(0))),
		key(artifactListQueryKey()),
		key(["artifact-manifest"]),
	];
}

function chatTargets(id: number | undefined, notebookId: number | undefined): LiveTarget[] {
	return [
		...(id === undefined
			? [key(["threads"])]
			: [
					key(cacheKeys.threads.messages(id)),
					key(cacheKeys.threads.detail(id)),
					key(cacheKeys.threads.turnStatus(id)),
				]),
		notebook("threads", notebookId),
		// Not the thread's agent actions (use-agent-actions-query.ts): a running
		// turn writes them into the cache as it streams, and a refetch then
		// would drop what the stream added.
	];
}

function automationTargets(id: number | undefined): LiveTarget[] {
	if (id === undefined) return [key(["automations"])];
	return [
		// The runs list (every page) and each run's detail share this prefix.
		key(["automations", "runs", id]),
		key(cacheKeys.automations.detail(id)),
		key(["automations", "list"]),
	];
}

/** What one change makes stale; nothing for a type this app does not know. */
export function targetsForChange(change: LiveChange): LiveTarget[] {
	switch (change.type) {
		case "documents":
			return documentTargets(change.id);
		case "folders":
			return [key(cacheKeys.folders.all())];
		case "notebooks":
			return notebookTargets(change.id);
		case "outputs":
			return outputTargets(change.notebook_id);
		case "notes":
			// Stale, not read now: an autosave sends this about once a second
			// while someone types, and the open note is their own copy
			// (features/notes/lib/note-cache.ts). A list is read again the next
			// time it is shown. The note's document change comes as `documents`.
			return [{ kind: "key", queryKey: notesKeys.lists(), refetch: "none" }];
		case "inbox":
			return [key(["notifications"])];
		case "chats":
			return chatTargets(change.id, change.notebook_id);
		case "automations":
			return automationTargets(change.id);
		case "connectors":
			// The list only: one connection's own read is the edit form's,
			// asked for when the form opens (editable-connector.ts).
			return [key(cacheKeys.connectors.all())];
		default:
			return [];
	}
}

/** A target's identity, so the same refetch is asked for once per batch. */
function signatureOf(target: LiveTarget): string {
	return JSON.stringify(target);
}

export function uniqueTargets(targets: readonly LiveTarget[]): LiveTarget[] {
	const seen = new Set<string>();
	return targets.filter((target) => {
		const signature = signatureOf(target);
		if (seen.has(signature)) return false;
		seen.add(signature);
		return true;
	});
}

/** Everything the stream keeps fresh: what a client that may have missed changes reads again. */
export function everyLiveTarget(): LiveTarget[] {
	return uniqueTargets(LIVE_CHANGE_TYPES.flatMap((type) => targetsForChange({ type })));
}
