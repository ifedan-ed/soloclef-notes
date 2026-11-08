import assert from "node:assert/strict";
import test from "node:test";
import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { notebookKeys } from "@/features/notebooks/api/notebook-query-keys";
import { noteSavesSettled, saveNotebookNote } from "@/features/notebooks/lib/note-saves";
import type { NotebookNote } from "@/features/notebooks/model/notebook";

const NB = 2;

function note(content: string, title = "Reopen check"): NotebookNote {
	return {
		id: 7,
		notebook_id: NB,
		title,
		content,
		converted_document_id: null,
		origin: "hand",
		automation_id: null,
		created_at: "2026-10-10T10:00:00Z",
		updated_at: "2026-10-10T10:00:00Z",
	};
}

function deferred<T>() {
	let resolve!: (value: T) => void;
	let reject!: (error: unknown) => void;
	const promise = new Promise<T>((res, rej) => {
		resolve = res;
		reject = rej;
	});
	return { promise, resolve, reject };
}

const tick = () => new Promise((resolve) => setTimeout(resolve, 0));

// Each test has a workspace of its own: saves on their way are kept per workspace and note.
let workspaces = 0;

function setup() {
	workspaces += 1;
	const WS = workspaces;
	const listKey = notebookKeys.notes(WS, NB);
	const noteKey = notebookKeys.note(WS, NB, 7);
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	queryClient.setQueryData(listKey, [note("First line.")]);
	queryClient.setQueryData(noteKey, note("First line."));
	return { WS, ref: { workspaceId: WS, notebookId: NB, noteId: 7 }, listKey, noteKey, queryClient };
}

test("the text being saved is in both of the note's caches as the save starts", async () => {
	const { ref, listKey, noteKey, queryClient } = setup();
	const answer = deferred<NotebookNote>();
	const saving = saveNotebookNote(
		queryClient,
		ref,
		{ content: "First line. alpha" },
		() => answer.promise
	);
	// Studio's list (Back, then the note opened again) and the Notes page's note.
	assert.equal(queryClient.getQueryData<NotebookNote[]>(listKey)?.[0].content, "First line. alpha");
	assert.equal(queryClient.getQueryData<NotebookNote>(noteKey)?.content, "First line. alpha");
	answer.resolve(note("First line. alpha"));
	await saving;
	assert.equal(queryClient.getQueryData<NotebookNote>(noteKey)?.content, "First line. alpha");
});

test("a read already on its way when a save starts does not bring the old text back", async () => {
	const { ref, listKey, queryClient } = setup();
	const read = deferred<NotebookNote[]>();
	const observer = new QueryObserver(queryClient, {
		queryKey: listKey,
		queryFn: () => read.promise,
		staleTime: 0,
	});
	const unsubscribe = observer.subscribe(() => undefined);
	void observer.refetch();
	await tick();
	const answer = deferred<NotebookNote>();
	const saving = saveNotebookNote(
		queryClient,
		ref,
		{ content: "First line. alpha" },
		() => answer.promise
	);
	// The read was asked before the save: its answer has the text from before.
	read.resolve([note("First line.")]);
	await tick();
	assert.equal(queryClient.getQueryData<NotebookNote[]>(listKey)?.[0].content, "First line. alpha");
	answer.resolve(note("First line. alpha"));
	await saving;
	unsubscribe();
});

test("a read asked while a save is on its way waits for it", async () => {
	const { WS, ref, queryClient } = setup();
	const answer = deferred<NotebookNote>();
	const saving = saveNotebookNote(
		queryClient,
		ref,
		{ content: "First line. alpha" },
		() => answer.promise
	);
	let waited = false;
	const reading = noteSavesSettled(WS, NB).then(() => {
		waited = true;
	});
	await tick();
	assert.equal(waited, false);
	// Another notebook's reads do not wait for it.
	await noteSavesSettled(WS, NB + 1);
	answer.resolve(note("First line. alpha"));
	await saving;
	await reading;
	assert.equal(waited, true);
	// The Notes page's list waits for any note of the workspace.
	const second = deferred<NotebookNote>();
	const again = saveNotebookNote(queryClient, ref, { title: "Renamed" }, () => second.promise);
	let listWaited = false;
	const list = noteSavesSettled(WS).then(() => {
		listWaited = true;
	});
	await tick();
	assert.equal(listWaited, false);
	second.resolve(note("First line. alpha", "Renamed"));
	await again;
	await list;
	assert.equal(listWaited, true);
});

test("a note's saves reach the server in the order made; only the newest answer is kept", async () => {
	const { ref, listKey, noteKey, queryClient } = setup();
	const sent: string[] = [];
	const answers = [deferred<NotebookNote>(), deferred<NotebookNote>()];
	const send = (changes: { content?: string }) => {
		sent.push(changes.content ?? "");
		return answers[sent.length - 1].promise;
	};
	const first = saveNotebookNote(queryClient, ref, { content: "First line. alpha" }, send);
	const second = saveNotebookNote(queryClient, ref, { content: "First line. alpha beta" }, send);
	await tick();
	// The second waits for the first to be answered.
	assert.deepEqual(sent, ["First line. alpha"]);
	answers[0].resolve(note("First line. alpha"));
	await first;
	await tick();
	assert.deepEqual(sent, ["First line. alpha", "First line. alpha beta"]);
	// The first answer is older than what the caches hold: they keep the newer text.
	assert.equal(queryClient.getQueryData<NotebookNote>(noteKey)?.content, "First line. alpha beta");
	assert.equal(
		queryClient.getQueryData<NotebookNote[]>(listKey)?.[0].content,
		"First line. alpha beta"
	);
	answers[1].resolve(note("First line. alpha beta"));
	await second;
	assert.equal(queryClient.getQueryData<NotebookNote>(noteKey)?.content, "First line. alpha beta");
});

test("a failed save has both caches read again", async () => {
	const { ref, listKey, noteKey, queryClient } = setup();
	await assert.rejects(
		saveNotebookNote(queryClient, ref, { content: "Lost" }, () => Promise.reject(new Error("503")))
	);
	assert.equal(queryClient.getQueryState(listKey)?.isInvalidated, true);
	assert.equal(queryClient.getQueryState(noteKey)?.isInvalidated, true);
});
