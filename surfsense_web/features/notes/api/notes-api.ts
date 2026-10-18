import { z } from "zod";
import { notebookNoteSchema } from "@/features/notebooks/model/notebook";
import { baseApiService } from "@/lib/apis/base-api.service";
import { noteDocumentContentSchema, workspaceNoteSchema } from "../model/workspace-note";

const workspace = (workspaceId: number) => `/api/v1/workspaces/${workspaceId}`;

/**
 * The Notes page's own routes. A notebook note is otherwise read and changed
 * through its notebook's routes (``notebooksApi``), and a note document
 * through the document editor routes, as everywhere else.
 */
export const notesApi = {
	list: (workspaceId: number, query: string) => {
		const params = new URLSearchParams();
		if (query) params.set("q", query);
		const suffix = params.size > 0 ? `?${params.toString()}` : "";
		return baseApiService.get(
			`${workspace(workspaceId)}/all-notes${suffix}`,
			z.array(workspaceNoteSchema)
		);
	},
	/** The note document becomes a note of ``notebookId`` (see the backend's moving.py). */
	moveIntoNotebook: (workspaceId: number, documentId: number, notebookId: number) =>
		baseApiService.post(
			`${workspace(workspaceId)}/all-notes/documents/${documentId}/move`,
			notebookNoteSchema,
			{ body: { notebook_id: notebookId } }
		),
	documentContent: (workspaceId: number, documentId: number) =>
		baseApiService.get(
			`${workspace(workspaceId)}/documents/${documentId}/editor-content`,
			noteDocumentContentSchema
		),
	/** Saving a note document reindexes it, so this is a Save button, not autosave. */
	saveDocument: (workspaceId: number, documentId: number, title: string, markdown: string) =>
		baseApiService.post(`${workspace(workspaceId)}/documents/${documentId}/save`, undefined, {
			// The title is sent so the server keeps it instead of reading one
			// off the first line.
			body: { title, source_markdown: markdown },
		}),
	deleteDocument: (workspaceId: number, documentId: number) =>
		baseApiService.delete(`${workspace(workspaceId)}/notes/${documentId}`),
};
