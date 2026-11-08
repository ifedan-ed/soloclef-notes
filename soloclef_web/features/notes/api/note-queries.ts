import { type QueryClient, queryOptions } from "@tanstack/react-query";
import { notebookKeys } from "@/features/notebooks/api/notebook-query-keys";
import { notebooksApi } from "@/features/notebooks/api/notebooks-api";
import { noteSavesSettled } from "@/features/notebooks/lib/note-saves";
import type { WorkspaceNote } from "../model/workspace-note";
import { notesApi } from "./notes-api";
import { notesKeys } from "./notes-query-keys";

/** A notebook note's full text, as the Notes page opens it. */
export function notebookNoteQuery(workspaceId: number, notebookId: number, noteId: number) {
	return queryOptions({
		queryKey: notebookKeys.note(workspaceId, notebookId, noteId),
		// Asked while a save is on its way, a read would bring back the text from before it.
		queryFn: async () => {
			await noteSavesSettled(workspaceId, notebookId);
			return notebooksApi.note(workspaceId, notebookId, noteId);
		},
		enabled: notebookId > 0,
	});
}

/** A note document's Markdown, as its editor reads it. */
export function noteDocumentQuery(workspaceId: number, documentId: number) {
	return queryOptions({
		queryKey: notesKeys.document(workspaceId, documentId),
		queryFn: () => notesApi.documentContent(workspaceId, documentId),
	});
}

/** Fetches a Notes page row's text into the cache (a failure is left for its query to show). */
export function prefetchNoteText(
	queryClient: QueryClient,
	workspaceId: number,
	note: WorkspaceNote
): Promise<void> {
	return note.kind === "document"
		? queryClient.prefetchQuery(noteDocumentQuery(workspaceId, note.id))
		: queryClient.prefetchQuery(notebookNoteQuery(workspaceId, note.notebook?.id ?? 0, note.id));
}
