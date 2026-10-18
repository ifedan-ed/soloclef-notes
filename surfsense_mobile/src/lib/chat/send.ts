import type { ApiClient, FetchLike } from "../api/client";
import { ApiError, parseErrorBody } from "../api/errors";
import { readSseStream } from "./sse";
import { AssistantMessageBuilder, type ContentPart, type StreamSideEffects } from "./stream-state";

export interface StreamTurnOptions {
	api: ApiClient;
	body: Record<string, unknown>;
	/** A fetch whose Response exposes a readable ``body`` stream (``expo/fetch`` on device). */
	fetchImpl: FetchLike;
	signal?: AbortSignal;
	effects?: StreamSideEffects;
	/** Called with the answer so far, at most every ``throttleMs``. */
	onUpdate: (parts: ContentPart[]) => void;
	/** Called once the server accepted the turn (the stream started). */
	onAccepted?: () => void;
	throttleMs?: number;
	sleep?: (ms: number) => Promise<void>;
}

const MAX_ATTEMPTS = 4;

function defaultSleep(ms: number): Promise<void> {
	return new Promise((resolve) => setTimeout(resolve, ms));
}

type ChatHttpError = ApiError & { retryAfterMs?: number };

function str(value: unknown): string | undefined {
	return typeof value === "string" && value ? value : undefined;
}
function num(value: unknown): number | undefined {
	return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

/**
 * A refused chat request, read like the web's ``toHttpResponseError``
 * (surfsense_web/lib/chat/chat-request-errors.ts): the code and retry hint may
 * sit in ``detail`` (an object), at the top level, in the error envelope, or in
 * Retry-After headers.
 */
export async function chatHttpError(response: Response): Promise<ChatHttpError> {
	let body: Record<string, unknown> = {};
	try {
		const parsed = await response.json();
		if (parsed && typeof parsed === "object") body = parsed as Record<string, unknown>;
	} catch {
		// not JSON
	}
	const detail = body.detail && typeof body.detail === "object" ? (body.detail as Record<string, unknown>) : {};
	const envelope = parseErrorBody(body);
	const code =
		str(detail.errorCode) ?? str(detail.error_code) ?? str(body.errorCode) ?? str(body.error_code) ?? envelope.code;
	const headerMs = Number.parseFloat(response.headers.get("retry-after-ms") ?? "");
	const headerSeconds = Number.parseFloat(response.headers.get("retry-after") ?? "");
	const retryAfterMs =
		num(detail.retry_after_ms) ??
		num(detail.retryAfterMs) ??
		num(body.retry_after_ms) ??
		num(body.retryAfterMs) ??
		(Number.isFinite(headerMs)
			? headerMs
			: Number.isFinite(headerSeconds)
				? Math.round(headerSeconds * 1000)
				: undefined);
	const message = str(detail.message) ?? envelope.message ?? "";
	const error = new ApiError(
		message || `Chat request failed (${response.status})`,
		response.status,
		code
	) as ChatHttpError;
	error.retryAfterMs = retryAfterMs;
	return error;
}

/**
 * Send one chat turn to ``POST /api/v1/new_chat`` and stream the answer.
 * A previous turn still being cancelled answers TURN_CANCELLING; like the web
 * (``fetchWithTurnCancellingRetry``) the request is retried a few times.
 * Resolves with the final parts; rejects with ApiError before the stream
 * starts, or StreamError when the server ends the turn with an error event.
 */
export async function streamChatTurn({
	api,
	body,
	fetchImpl,
	signal,
	effects,
	onUpdate,
	onAccepted,
	throttleMs = 60,
	sleep = defaultSleep,
}: StreamTurnOptions): Promise<ContentPart[]> {
	let response: Response | null = null;
	for (let attempt = 1; attempt <= MAX_ATTEMPTS; attempt += 1) {
		const candidate = await api.fetchAuthed(
			"/api/v1/new_chat",
			{
				method: "POST",
				headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
				body: JSON.stringify(body),
				signal,
			},
			fetchImpl
		);
		if (candidate.ok) {
			response = candidate;
			break;
		}
		const error = await chatHttpError(candidate);
		if (error.code === "TURN_CANCELLING" && attempt < MAX_ATTEMPTS) {
			await sleep(error.retryAfterMs ?? 500 * attempt);
			continue;
		}
		throw error;
	}
	if (!response?.body) throw new ApiError("The server did not stream an answer.", 502);
	onAccepted?.();

	const builder = new AssistantMessageBuilder(effects);
	let timer: ReturnType<typeof setTimeout> | null = null;
	let dirty = false;
	const flush = () => {
		timer = null;
		if (!dirty) return;
		dirty = false;
		onUpdate(builder.snapshot());
	};
	try {
		await readSseStream(response.body as ReadableStream<Uint8Array>, (event) => {
			if (builder.apply(event)) {
				dirty = true;
				if (!timer) timer = setTimeout(flush, throttleMs);
			}
		});
	} finally {
		if (timer) clearTimeout(timer);
		flush();
	}
	return builder.snapshot();
}
