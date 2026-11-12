/**
 * Thread persistence utilities for the new chat feature.
 * Provides API functions and thread list management.
 */

import { baseApiService } from "@/lib/apis/base-api.service";
import { buildBackendUrl } from "@/lib/env-config";
// =============================================================================
// Types matching backend schemas
// =============================================================================

/**
 * A thread as the server answers it on create, update and full read.
 *
 * A thread belongs to the account that made it and to nobody else, so the
 * record names no owner and no sharing level: the server takes
 * the account from the session and answers 404 for anyone else's thread.
 */
export interface ThreadRecord {
	id: number;
	title: string;
	archived: boolean;
	created_at: string;
	updated_at: string;
}

export interface TokenUsageSummary {
	prompt_tokens: number;
	completion_tokens: number;
	total_tokens: number;
	/**
	 * Total provider USD cost for this assistant turn, in micro-USD
	 * (1_000_000 = $1.00). Optional because rows persisted before the
	 * cost-credits migration won't have it.
	 */
	cost_micros?: number;
	model_breakdown?: Record<
		string,
		{
			prompt_tokens: number;
			completion_tokens: number;
			total_tokens: number;
			cost_micros?: number;
		}
	> | null;
}

export interface MessageRecord {
	id: number;
	thread_id: number;
	role: "user" | "assistant" | "system";
	content: unknown;
	created_at: string;
	author_id?: string | null;
	author_display_name?: string | null;
	author_avatar_url?: string | null;
	token_usage?: TokenUsageSummary | null;
	// Per-turn correlation id from ``configurable.turn_id`` at streaming
	// time (added in migration 136). Used by the per-turn revert
	// endpoint and edit-from-arbitrary-position. Nullable on legacy
	// rows that predate the column.
	turn_id?: string | null;
}

export interface ThreadHistoryLoadResponse {
	messages: MessageRecord[];
}

// =============================================================================
// API Service Functions
// =============================================================================

/**
 * Create a new thread for the signed-in account.
 *
 * The body carries the title and nothing about ownership: the server keys
 * the thread by the session's account, so there is no id a caller could
 * send to place a thread anywhere else.
 */
export async function createThread(title = "New Chat"): Promise<ThreadRecord> {
	return baseApiService.post<ThreadRecord>("/api/v1/threads", undefined, {
		body: {
			title,
			archived: false,
		},
	});
}

/**
 * Get thread messages
 */
export async function getThreadMessages(threadId: number): Promise<ThreadHistoryLoadResponse> {
	return baseApiService.get<ThreadHistoryLoadResponse>(`/api/v1/threads/${threadId}`);
}

/**
 * Append a message to a thread.
 *
 * ``turn_id`` is the per-turn correlation id streamed by the backend
 * via ``data-turn-info``. Persisting it lets later edits locate the
 * matching LangGraph checkpoint without HumanMessage scanning. Older
 * callers can still omit it for back-compat.
 *
 * @deprecated Replaced by the SSE-based message ID handshake. The
 * streaming generator (`stream_new_chat` / `stream_resume_chat`) now
 * persists both the user and assistant rows server-side via
 * `persist_user_turn` / `persist_assistant_shell` and emits
 * `data-user-message-id` / `data-assistant-message-id` SSE events so
 * the UI renames its optimistic IDs in real time. The only remaining
 * caller is `persistAssistantErrorMessage` (pre-stream error fallback
 * for requests the server never accepted — the server has nothing to
 * persist in that case). After the legacy route is removed in a
 * follow-up PR this function will be deleted entirely.
 */
export async function appendMessage(
	threadId: number,
	message: {
		role: "user" | "assistant" | "system";
		content: unknown;
		token_usage?: unknown;
		turn_id?: string | null;
	}
): Promise<MessageRecord> {
	return baseApiService.post<MessageRecord>(`/api/v1/threads/${threadId}/messages`, undefined, {
		body: message,
	});
}

/**
 * Get the thread's own record (title, archived, dates) without its messages.
 */
export async function getThreadFull(threadId: number): Promise<ThreadRecord> {
	return baseApiService.get<ThreadRecord>(`/api/v1/threads/${threadId}/full`);
}

export interface PendingInterruptsResponse {
	/** The paused turn's assistant row; the reconstructed card reattaches here. */
	assistant_message_id: number | null;
	/** Each entry mirrors the ``data-interrupt-request`` SSE payload. */
	pending_interrupts: Array<Record<string, unknown>>;
}

/**
 * Fetch the thread's paused HITL interrupts from the LangGraph checkpoint.
 *
 * The live approval card lives only in ``chatStreamStore`` (module RAM), so a
 * page refresh loses it while the backend stays paused. Called on thread load
 * to rebuild the card.
 */
export async function getPendingInterrupts(threadId: number): Promise<PendingInterruptsResponse> {
	return baseApiService.get<PendingInterruptsResponse>(
		`/api/v1/threads/${threadId}/pending-interrupts`
	);
}

/**
 * Get the URL for the regenerate endpoint (for streaming fetch)
 */
export function getRegenerateUrl(threadId: number): string {
	return buildBackendUrl(`/api/v1/threads/${threadId}/regenerate`);
}
