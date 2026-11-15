import assert from "node:assert/strict";
import { test } from "node:test";
import { QueryClient } from "@tanstack/react-query";
import { artifactListQueryKey } from "@/features/artifacts/api/artifact-queries";
import { documentViewQueryKey } from "@/features/documents/viewer/document-view-query";
import { applyLiveTargets, toQueryFilters } from "@/features/live-changes/lib/apply-live-targets";
import {
	everyLiveTarget,
	targetsForChange,
	uniqueTargets,
} from "@/features/live-changes/lib/live-query-targets";
import type { LiveChange } from "@/features/live-changes/model/live-change";
import { notebookKeys } from "@/features/notebooks/api/notebook-query-keys";
import { notesKeys } from "@/features/notes/api/notes-query-keys";
import { cacheKeys } from "@/lib/query-client/cache-keys";

/**
 * A cache holding one read of every kind the app keeps, named. A change is
 * applied to it through the real seam (applyLiveTargets on a QueryClient),
 * and the test reads back which reads were made stale.
 */
const CATALOGUE: Record<string, readonly unknown[]> = {
	documentsList: cacheKeys.documents.globalQueryParams({ page: 0, page_size: 20 }),
	documentsWithQueries: cacheKeys.documents.withQueryParams({ page: 0 }),
	sourceStages: ["documents", "stages", [12]],
	typeCounts: cacheKeys.documents.typeCounts(),
	documentTitles: ["document-titles", { page: 0, page_size: 20 }],
	document12: cacheKeys.documents.document("12"),
	document13: cacheKeys.documents.document("13"),
	view12: documentViewQueryKey(12),
	view13: documentViewQueryKey(13),
	content12: ["document-content", 12],
	notebookList: notebookKeys.list(),
	notebookSearch: notebookKeys.search("sepsis"),
	nb7: notebookKeys.detail(7),
	nb8: notebookKeys.detail(8),
	nb7Sources: notebookKeys.sources(7),
	nb8Sources: notebookKeys.sources(8),
	nb7Outputs: notebookKeys.outputs(7),
	nb8Outputs: notebookKeys.outputs(8),
	nb7OutputView: notebookKeys.outputView(7, 3),
	nb8OutputView: notebookKeys.outputView(8, 4),
	nb7ReportView: notebookKeys.reportView(7, 3),
	nb7Threads: notebookKeys.threads(7),
	nb8Threads: notebookKeys.threads(8),
	nb7Suggestions: notebookKeys.suggestions(7, "audio"),
	nb7Guide: notebookKeys.guide(7, 12),
	nb7Followups: notebookKeys.followups(7, "msg-1"),
	folders: cacheKeys.folders.all(),
	podcast5: cacheKeys.podcasts.detail(5),
	job6: cacheKeys.deliverableJobs.detail(6),
	artifactList55: artifactListQueryKey(55),
	artifactManifest9: ["artifact-manifest", 9],
	notesList: notesKeys.list({ query: "iron" }),
	note4: notesKeys.note(4),
	inboxHead: cacheKeys.notifications.head("status"),
	unreadCounts: cacheKeys.notifications.batchUnreadCounts(),
	messages55: cacheKeys.threads.messages(55),
	thread55: cacheKeys.threads.detail(55),
	turn55: cacheKeys.threads.turnStatus(55),
	messages56: cacheKeys.threads.messages(56),
	agentActions55: ["agent-actions", 55],
	runs3: cacheKeys.automations.runs(3, 20, 0),
	run3: cacheKeys.automations.run(3, 1),
	automation3: cacheKeys.automations.detail(3),
	runs4: cacheKeys.automations.runs(4, 20, 0),
	automationList: cacheKeys.automations.list(50, 0),
	connectorsList: cacheKeys.connectors.all(),
	connector9: cacheKeys.connectors.byId("9"),
	driveFolders: cacheKeys.connectors.googleDrive.folders(9),
	user: cacheKeys.user.current(),
};

function seededClient(): QueryClient {
	// No garbage-collection timers: they would keep the test process alive.
	const client = new QueryClient({
		defaultOptions: { queries: { gcTime: Number.POSITIVE_INFINITY } },
	});
	for (const key of Object.values(CATALOGUE)) client.setQueryData(key, { seeded: true });
	return client;
}

function staleAfter(...changes: LiveChange[]): string[] {
	const client = seededClient();
	applyLiveTargets(client, uniqueTargets(changes.flatMap(targetsForChange)));
	const stale = Object.entries(CATALOGUE)
		.filter(([, key]) => client.getQueryState(key)?.isInvalidated)
		.map(([name]) => name)
		.sort();
	client.clear();
	return stale;
}

const sorted = (names: string[]) => [...names].sort();

const EVERY_NOTEBOOK_SOURCES_AND_DETAIL = ["nb7", "nb8", "nb7Sources", "nb8Sources"];
const DOCUMENT_LISTS = [
	"documentsList",
	"documentsWithQueries",
	"sourceStages",
	"typeCounts",
	"documentTitles",
	"notebookList",
	...EVERY_NOTEBOOK_SOURCES_AND_DETAIL,
];

test("documents with an id: lists, stages, counts, that document's view, every notebook's sources", () => {
	assert.deepEqual(
		staleAfter({ type: "documents", id: 12 }),
		sorted([...DOCUMENT_LISTS, "document12", "view12"])
	);
});

test("documents without an id: every document's own reads too", () => {
	assert.deepEqual(
		staleAfter({ type: "documents" }),
		sorted([...DOCUMENT_LISTS, "document12", "document13", "view12", "view13"])
	);
});

test("documents never touch the open editor's content", () => {
	assert.equal(staleAfter({ type: "documents", id: 12 }).includes("content12"), false);
});

test("folders: the folder tree", () => {
	assert.deepEqual(staleAfter({ type: "folders", id: 2 }), ["folders"]);
});

test("notebooks with an id: the list, search and that notebook alone", () => {
	assert.deepEqual(
		staleAfter({ type: "notebooks", id: 7 }),
		sorted(["notebookList", "notebookSearch", "nb7", "nb7Sources"])
	);
});

test("notebooks without an id: every notebook's detail and sources, never their other reads", () => {
	assert.deepEqual(
		staleAfter({ type: "notebooks" }),
		sorted(["notebookList", "notebookSearch", ...EVERY_NOTEBOOK_SOURCES_AND_DETAIL])
	);
});

const CHAT_CARDS = ["podcast5", "job6", "artifactList55", "artifactManifest9"];

test("outputs with a notebook: that notebook's Studio reads and every chat card", () => {
	assert.deepEqual(
		staleAfter({ type: "outputs", notebook_id: 7 }),
		sorted(["nb7Outputs", "nb7OutputView", "nb7ReportView", ...CHAT_CARDS])
	);
});

test("outputs without a notebook: every notebook's Studio reads", () => {
	assert.deepEqual(
		staleAfter({ type: "outputs" }),
		sorted([
			"nb7Outputs",
			"nb8Outputs",
			"nb7OutputView",
			"nb8OutputView",
			"nb7ReportView",
			...CHAT_CARDS,
		])
	);
});

test("notes: the lists are marked stale but not read now, and the open note is left alone", () => {
	assert.deepEqual(staleAfter({ type: "notes", id: 4 }), ["notesList"]);
	const [target] = targetsForChange({ type: "notes", id: 4 });
	assert.equal(toQueryFilters(target).refetchType, "none");
});

test("inbox: the newest page and the unread counts", () => {
	assert.deepEqual(staleAfter({ type: "inbox" }), sorted(["inboxHead", "unreadCounts"]));
});

test("chats with a thread and notebook: that thread's messages, detail and turn status, that notebook's threads", () => {
	assert.deepEqual(
		staleAfter({ type: "chats", id: 55, notebook_id: 7 }),
		sorted(["messages55", "thread55", "turn55", "nb7Threads"])
	);
});

test("chats never refetch agent actions, which a running turn writes into the cache", () => {
	assert.equal(staleAfter({ type: "chats", id: 55 }).includes("agentActions55"), false);
	assert.equal(staleAfter({ type: "chats" }).includes("agentActions55"), false);
});

test("chats without ids: every thread read and every notebook's thread list", () => {
	assert.deepEqual(
		staleAfter({ type: "chats" }),
		sorted(["messages55", "thread55", "turn55", "messages56", "nb7Threads", "nb8Threads"])
	);
});

test("automations with an id: its runs (every page and each run), its detail, the list", () => {
	assert.deepEqual(
		staleAfter({ type: "automations", id: 3 }),
		sorted(["runs3", "run3", "automation3", "automationList"])
	);
});

test("automations without an id: everything automations", () => {
	assert.deepEqual(
		staleAfter({ type: "automations" }),
		sorted(["runs3", "run3", "automation3", "runs4", "automationList"])
	);
});

test("connectors: the list only, not an open edit form nor Drive folders", () => {
	assert.deepEqual(staleAfter({ type: "connectors", id: 9 }), ["connectorsList"]);
});

test("a type this app does not know refreshes nothing", () => {
	assert.deepEqual(targetsForChange({ type: "presence", id: 1 }), []);
	assert.deepEqual(staleAfter({ type: "presence" }), []);
});

test("a burst of changes asks for each refresh once", () => {
	const targets = uniqueTargets(
		[
			{ type: "documents", id: 12 },
			{ type: "documents", id: 13 },
		].flatMap(targetsForChange)
	);
	const signatures = targets.map((target) => JSON.stringify(target));
	assert.equal(new Set(signatures).size, signatures.length);
	assert.equal(signatures.filter((s) => s.includes('"documents"]')).length, 1);
});

test("a catch-up refresh covers every live read and nothing else", () => {
	const client = seededClient();
	applyLiveTargets(client, everyLiveTarget(), { keepInFlight: true });
	const stale = Object.entries(CATALOGUE)
		.filter(([, key]) => client.getQueryState(key)?.isInvalidated)
		.map(([name]) => name);
	client.clear();
	const untouched = Object.keys(CATALOGUE).filter((name) => !stale.includes(name));
	assert.deepEqual(
		sorted(untouched),
		sorted([
			"content12",
			"nb7Suggestions",
			"nb7Guide",
			"nb7Followups",
			"note4",
			"agentActions55",
			"connector9",
			"driveFolders",
			"user",
		])
	);
});

test("after a reconnect, only reads older than the stream's ready are refreshed", () => {
	const client = seededClient();
	const readyAt = Date.now();
	// Read after the stream was ready: the stream covers it already.
	client.setQueryData(cacheKeys.folders.all(), { seeded: true }, { updatedAt: readyAt + 5 });
	client.setQueryData(
		cacheKeys.notifications.head("status"),
		{ seeded: true },
		{
			updatedAt: readyAt - 5,
		}
	);
	applyLiveTargets(client, everyLiveTarget(), { readBefore: readyAt, keepInFlight: true });
	assert.equal(client.getQueryState(cacheKeys.folders.all())?.isInvalidated, false);
	assert.equal(client.getQueryState(cacheKeys.notifications.head("status"))?.isInvalidated, true);
	client.clear();
});
