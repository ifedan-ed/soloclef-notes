/**
 * Builds an assistant message from chat stream events, following the web's
 * ``processSharedStreamEvent`` + ``streaming-state`` rules
 * (surfsense_web/lib/chat/stream-pipeline.ts, streaming-state.ts):
 *
 * - text deltas accumulate into the current text part; a reasoning block or a
 *   tool call closes it, so the next delta opens a new one;
 * - reasoning deltas accumulate into a reasoning part that ``reasoning-end``
 *   marks completed;
 * - tool calls are cards keyed by ``toolCallId``: input deltas, final input
 *   and output update the same card;
 * - ``error`` ends the turn with the server's message.
 *
 * Side-channel events (message ids, thread title, turn status, token usage)
 * are reported through {@link StreamSideEffects}; everything else is ignored,
 * as the web ignores events it has no UI for.
 */

export type ContentPart =
	| { type: "text"; text: string }
	| { type: "reasoning"; text: string; status: "running" | "completed" }
	| {
			type: "tool-call";
			toolCallId: string;
			toolName: string;
			args: Record<string, unknown>;
			argsText?: string;
			result?: unknown;
	  };

export interface StreamSideEffects {
	onUserMessageId?: (id: number) => void;
	onAssistantMessageId?: (id: number) => void;
	onThreadTitle?: (threadId: number, title: string) => void;
	onTurnStatus?: (status: string) => void;
	onTruncated?: () => void;
}

export class StreamError extends Error {
	constructor(
		message: string,
		readonly errorCode?: string
	) {
		super(message);
		this.name = "StreamError";
	}
}

type Event = { type?: unknown; [key: string]: unknown };

function asRecord(value: unknown): Record<string, unknown> {
	return typeof value === "object" && value !== null ? (value as Record<string, unknown>) : {};
}

export class AssistantMessageBuilder {
	private parts: ContentPart[] = [];
	private currentText = -1;
	private currentReasoning = -1;
	private toolIndex = new Map<string, number>();
	finished = false;

	constructor(private readonly effects: StreamSideEffects = {}) {}

	/** Apply one event. Returns true when the visible content changed. Throws on ``error``. */
	apply(raw: unknown): boolean {
		const event = asRecord(raw) as Event;
		switch (event.type) {
			case "text-delta":
				return this.appendText(String(event.delta ?? ""));
			case "reasoning-start":
				this.currentText = -1;
				this.currentReasoning = -1;
				return false;
			case "reasoning-delta":
				return this.appendReasoning(String(event.delta ?? ""));
			case "reasoning-end": {
				const part = this.parts[this.currentReasoning];
				if (part?.type === "reasoning") part.status = "completed";
				this.currentReasoning = -1;
				return true;
			}
			case "tool-input-start":
				this.addTool(String(event.toolCallId), String(event.toolName ?? "tool"), {});
				return true;
			case "tool-input-delta": {
				const part = this.tool(String(event.toolCallId));
				if (part) part.argsText = (part.argsText ?? "") + String(event.inputTextDelta ?? "");
				return false;
			}
			case "tool-input-available": {
				const id = String(event.toolCallId);
				const input = asRecord(event.input);
				const existing = this.tool(id);
				if (existing) existing.args = input;
				else this.addTool(id, String(event.toolName ?? "tool"), input);
				return true;
			}
			case "tool-output-available": {
				const part = this.tool(String(event.toolCallId));
				if (part) part.result = event.output;
				return true;
			}
			case "data-user-message-id": {
				const id = Number(asRecord(event.data).message_id);
				if (Number.isFinite(id)) this.effects.onUserMessageId?.(id);
				return false;
			}
			case "data-assistant-message-id": {
				const id = Number(asRecord(event.data).message_id);
				if (Number.isFinite(id)) this.effects.onAssistantMessageId?.(id);
				return false;
			}
			case "data-thread-title-update": {
				const data = asRecord(event.data);
				if (typeof data.title === "string" && typeof data.threadId === "number") {
					this.effects.onThreadTitle?.(data.threadId, data.title);
				}
				return false;
			}
			case "data-turn-status": {
				const status = asRecord(event.data).status;
				if (typeof status === "string") this.effects.onTurnStatus?.(status);
				return false;
			}
			case "data-token-usage":
				if (asRecord(event.data).truncated) this.effects.onTruncated?.();
				return false;
			case "finish":
				this.finished = true;
				return false;
			case "error":
				throw new StreamError(
					typeof event.message === "string" && event.message ? event.message : "The answer could not be completed.",
					typeof event.errorCode === "string" ? event.errorCode : undefined
				);
			default:
				return false;
		}
	}

	private appendText(delta: string): boolean {
		if (!delta) return false;
		this.currentReasoning = -1;
		const part = this.parts[this.currentText];
		if (part?.type === "text") {
			part.text += delta;
		} else {
			this.parts.push({ type: "text", text: delta });
			this.currentText = this.parts.length - 1;
		}
		return true;
	}

	private appendReasoning(delta: string): boolean {
		if (!delta) return false;
		this.currentText = -1;
		const part = this.parts[this.currentReasoning];
		if (part?.type === "reasoning") {
			part.text += delta;
		} else {
			this.parts.push({ type: "reasoning", text: delta, status: "running" });
			this.currentReasoning = this.parts.length - 1;
		}
		return true;
	}

	private addTool(toolCallId: string, toolName: string, args: Record<string, unknown>) {
		if (this.toolIndex.has(toolCallId)) return;
		this.parts.push({ type: "tool-call", toolCallId, toolName, args });
		this.toolIndex.set(toolCallId, this.parts.length - 1);
		this.currentText = -1;
		this.currentReasoning = -1;
	}

	private tool(toolCallId: string) {
		const index = this.toolIndex.get(toolCallId);
		const part = index === undefined ? undefined : this.parts[index];
		return part?.type === "tool-call" ? part : undefined;
	}

	/** A copy safe to hand to React state. Empty text/reasoning parts are dropped. */
	snapshot(): ContentPart[] {
		return this.parts
			.filter((part) => (part.type === "tool-call" ? true : part.text.length > 0))
			.map((part) => ({ ...part }));
	}
}

/** The answer's text, joined as it reads (for follow-ups and "copy"). */
export function answerText(parts: readonly ContentPart[]): string {
	return parts
		.filter((part): part is Extract<ContentPart, { type: "text" }> => part.type === "text")
		.map((part) => part.text)
		.join("\n\n");
}

/**
 * Persisted message content (from ``GET /threads/{id}``) back into parts.
 * The backend stores the same part list the stream builds, plus parts the app
 * does not draw (mentioned-documents chips, activity journals); a plain string
 * is legacy content.
 */
export function partsFromStored(content: unknown): ContentPart[] {
	if (typeof content === "string") return content ? [{ type: "text", text: content }] : [];
	if (!Array.isArray(content)) return [];
	const parts: ContentPart[] = [];
	for (const item of content) {
		const part = asRecord(item);
		if (part.type === "text" && typeof part.text === "string" && part.text) {
			parts.push({ type: "text", text: part.text });
		} else if (part.type === "reasoning" && typeof part.text === "string" && part.text) {
			parts.push({ type: "reasoning", text: part.text, status: "completed" });
		} else if (part.type === "tool-call" && typeof part.toolName === "string") {
			parts.push({
				type: "tool-call",
				toolCallId: String(part.toolCallId ?? parts.length),
				toolName: part.toolName,
				args: asRecord(part.args),
				result: part.result,
			});
		}
	}
	return parts;
}

/** Titles of the sources a stored user message @-mentioned, for its chips. */
export function mentionsFromStored(content: unknown): string[] {
	if (!Array.isArray(content)) return [];
	for (const item of content) {
		const part = asRecord(item);
		if (part.type === "mentioned-documents" && Array.isArray(part.documents)) {
			return part.documents
				.map((doc) => asRecord(doc).title)
				.filter((title): title is string => typeof title === "string");
		}
	}
	return [];
}
