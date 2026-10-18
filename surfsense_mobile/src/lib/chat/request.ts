import type { NotebookSource } from "../api/schemas";
import { answerText, type ContentPart } from "./stream-state";

export interface ChatTurn {
	role: "user" | "assistant";
	parts: ContentPart[];
}

/** A source picked with @, in the shape the web's mention chips carry. */
export interface MentionedSource {
	id: number;
	title: string;
	kind: "doc" | "folder";
	document_type?: string;
}

/** A notebook source as the @ picker offers it (surfsense_web/.../notebook-source-picker.tsx). */
export function mentionFromSource(source: NotebookSource): MentionedSource {
	return source.kind === "folder"
		? { id: source.target_id, title: source.title, kind: "folder" }
		: {
				id: source.target_id,
				title: source.title,
				kind: "doc",
				document_type: source.document_type ?? "FILE",
			};
}

/**
 * The body of ``POST /api/v1/new_chat`` for one notebook turn, built the way
 * the web's ``startNewChat`` builds it (surfsense_web/lib/chat/stream-engine/
 * engine.ts). The notebook's own sources are pinned server-side from the
 * thread, so nothing notebook-specific is sent here; mentions narrow the turn
 * to the picked sources.
 */
export function buildChatRequest({
	threadId,
	workspaceId,
	query,
	history,
	mentions,
}: {
	threadId: number;
	workspaceId: number;
	query: string;
	history: readonly ChatTurn[];
	mentions: readonly MentionedSource[];
}): Record<string, unknown> {
	const documentIds = mentions.filter((m) => m.kind === "doc").map((m) => m.id);
	const folderIds = mentions.filter((m) => m.kind === "folder").map((m) => m.id);
	const messages = history
		.map((turn) => ({ role: turn.role, content: answerText(turn.parts) }))
		.filter((turn) => turn.content.length > 0);
	return {
		chat_id: threadId,
		user_query: query.trim(),
		workspace_id: workspaceId,
		retrieval_scope: "documents",
		filesystem_mode: "cloud",
		client_platform: "web",
		messages,
		mentioned_document_ids: documentIds.length > 0 ? documentIds : undefined,
		mentioned_folder_ids: folderIds.length > 0 ? folderIds : undefined,
		mentioned_documents: mentions.length > 0 ? mentions : undefined,
	};
}

/** The text after the last "@" being typed, or null when the cursor is not in a mention. */
export function activeMentionQuery(text: string): string | null {
	const at = text.lastIndexOf("@");
	if (at < 0) return null;
	if (at > 0 && !/\s/.test(text[at - 1])) return null;
	const query = text.slice(at + 1);
	if (/\n/.test(query) || query.length > 60) return null;
	return query;
}

/** Remove the "@query" being typed once a source was picked from it. */
export function stripActiveMention(text: string): string {
	const at = text.lastIndexOf("@");
	return at < 0 ? text : text.slice(0, at).replace(/\s+$/, at > 0 ? " " : "");
}
