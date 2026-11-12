import type {
	deriveMentionedPayload,
	MentionedDocumentInfo,
} from "@/atoms/chat/mentioned-documents.atom";
import type { RetrievalScope } from "@/contracts/types/retrieval-scope.types";
import type { HitlResponse } from "@/features/chat-messages/hitl";
import type { PreferredToolRequestFields } from "@/features/composer-tools/preferred-tool-request";
import type { AgentFilesystemSelection } from "@/lib/agent-filesystem";
import type { NewChatUserImagePayload } from "@/lib/chat/user-turn-api-parts";

/**
 * The JSON bodies of the three requests that start a chat turn: a new
 * message, a resume after an approval, and a regenerate or edit.
 *
 * They are built here, apart from the engine that sends them, so that what
 * goes on the wire can be read and tested without a stream, a store or a
 * network. None of them says whose chat it is: the server takes the account
 * from the session and finds the thread under it, so an id naming an owner
 * would be at best ignored and at worst believed.
 *
 * An absent list is left out (undefined is dropped by JSON.stringify) and
 * never sent as an empty one, so the server can tell "nothing mentioned"
 * from a list that was cleared.
 */

type MentionedPayload = ReturnType<typeof deriveMentionedPayload>;

/** A list for the wire, or undefined when it has nothing in it. */
function orAbsent<T>(items: readonly T[]): readonly T[] | undefined {
	return items.length > 0 ? items : undefined;
}

/** The fields every turn carries about where the agent's files live. */
function filesystemFields(selection: AgentFilesystemSelection) {
	return {
		filesystem_mode: selection.filesystem_mode,
		client_platform: selection.client_platform,
		local_filesystem_mounts: selection.local_filesystem_mounts,
	};
}

export interface NewChatBodyInput {
	threadId: number;
	userQuery: string;
	turnToolFields: PreferredToolRequestFields;
	selection: AgentFilesystemSelection;
	messageHistory: ReadonlyArray<{ role: string; content: string }>;
	mentionPayload: MentionedPayload;
	/** Every mention of the turn, de-duplicated, as the chips show them. */
	mentionedDocuments: readonly MentionedDocumentInfo[];
	disabledTools: readonly string[];
	userImages: readonly NewChatUserImagePayload[];
}

/** The body of POST /api/v1/new_chat. */
export function newChatRequestBody(input: NewChatBodyInput): Record<string, unknown> {
	const { mentionPayload } = input;
	return {
		chat_id: input.threadId,
		user_query: input.userQuery.trim(),
		retrieval_scope: input.turnToolFields.retrieval_scope,
		preferred_tool: input.turnToolFields.preferred_tool,
		...filesystemFields(input.selection),
		messages: input.messageHistory,
		mentioned_document_ids: orAbsent(mentionPayload.document_ids),
		mentioned_folder_ids: orAbsent(mentionPayload.folder_ids),
		mentioned_connector_ids: orAbsent(mentionPayload.connector_ids),
		// The connector rows travel only with their ids, never on their own.
		mentioned_connectors:
			mentionPayload.connector_ids.length > 0 ? mentionPayload.connectors : undefined,
		mentioned_thread_ids: orAbsent(mentionPayload.thread_ids),
		mentioned_documents: orAbsent(input.mentionedDocuments),
		disabled_tools: orAbsent(input.disabledTools),
		// Unlike the lists above the key itself is left out, as it always was:
		// a turn without pictures has no user_images field at all.
		...(input.userImages.length > 0 ? { user_images: input.userImages } : {}),
	};
}

export interface ResumeBodyInput {
	retrievalScope: RetrievalScope;
	decisions: readonly HitlResponse[];
	disabledTools: readonly string[];
	selection: AgentFilesystemSelection;
}

/** The body of POST /api/v1/threads/{id}/resume; the thread is named by the path. */
export function resumeRequestBody(input: ResumeBodyInput): Record<string, unknown> {
	return {
		retrieval_scope: input.retrievalScope,
		decisions: input.decisions,
		disabled_tools: orAbsent(input.disabledTools),
		...filesystemFields(input.selection),
	};
}

export interface RegenerateBodyInput {
	retrievalScope: RetrievalScope;
	/** The edited question; null regenerates the answer to the same one. */
	userQuery: string | null;
	disabledTools: readonly string[];
	selection: AgentFilesystemSelection;
	/** The mentions of the user message being answered again. */
	mentions: readonly MentionedDocumentInfo[];
	/**
	 * Set only for an edit: the pictures of the edited message, where an
	 * empty list means "the edit removed them". A plain regenerate leaves
	 * the field out so the server keeps the pictures the message had.
	 */
	editedUserImages?: readonly NewChatUserImagePayload[];
	/** Set when the edit rewinds the thread to an earlier message. */
	fromMessageId?: number;
	/** With fromMessageId: also undo the agent's reversible actions after it. */
	revertActions?: boolean;
}

/** The body of POST /api/v1/threads/{id}/regenerate; the thread is named by the path. */
export function regenerateRequestBody(input: RegenerateBodyInput): Record<string, unknown> {
	const ofKind = (kind: MentionedDocumentInfo["kind"]) =>
		input.mentions.filter((mention) => mention.kind === kind);
	const connectors = ofKind("connector");
	const body: Record<string, unknown> = {
		retrieval_scope: input.retrievalScope,
		user_query: input.userQuery,
		disabled_tools: orAbsent(input.disabledTools),
		...filesystemFields(input.selection),
		mentioned_document_ids: orAbsent(ofKind("doc").map((mention) => mention.id)),
		mentioned_folder_ids: orAbsent(ofKind("folder").map((mention) => mention.id)),
		mentioned_connector_ids: orAbsent(connectors.map((mention) => mention.id)),
		mentioned_connectors: orAbsent(connectors),
		mentioned_thread_ids: orAbsent(ofKind("thread").map((mention) => mention.id)),
		mentioned_documents: orAbsent(input.mentions),
	};
	if (input.editedUserImages !== undefined) body.user_images = input.editedUserImages;
	if (input.fromMessageId !== undefined) {
		body.from_message_id = input.fromMessageId;
		if (input.revertActions) body.revert_actions = true;
	}
	return body;
}
