/**
 * Incremental Server-Sent Events reader for the chat stream.
 *
 * Same rules as the web's ``readSSEStream`` (surfsense_web/lib/chat/
 * streaming-state.ts): events are separated by a blank line (LF or CRLF), only
 * ``data: `` lines carry payloads, ``[DONE]`` and empty payloads are skipped,
 * and a payload that is not valid JSON is dropped rather than failing the turn.
 * Chunks may split anywhere, including inside a multi-byte character; the
 * decoder handles bytes and this parser handles text.
 */
export class SseParser {
	private buffer = "";

	/** Feed decoded text; returns every complete event's parsed payloads. */
	push(text: string): unknown[] {
		this.buffer += text;
		const blocks = this.buffer.split(/\r?\n\r?\n/);
		this.buffer = blocks.pop() ?? "";
		return blocks.flatMap(parseBlock);
	}

	/** The stream closed: a final event without its trailing blank line still counts. */
	end(): unknown[] {
		const rest = this.buffer;
		this.buffer = "";
		return rest.trim() ? parseBlock(rest) : [];
	}
}

function parseBlock(block: string): unknown[] {
	const out: unknown[] = [];
	for (const line of block.split(/\r?\n/)) {
		if (!line.startsWith("data: ")) continue;
		const data = line.slice(6).trim();
		if (!data || data === "[DONE]") continue;
		try {
			out.push(JSON.parse(data));
		} catch {
			// Malformed payload: skip it, as the web client does.
		}
	}
	return out;
}

/** Read a byte stream to the end, handing each event to ``onEvent`` as it arrives. */
export async function readSseStream(
	body: ReadableStream<Uint8Array>,
	onEvent: (event: unknown) => void
): Promise<void> {
	const reader = body.getReader();
	const decoder = new TextDecoder();
	const parser = new SseParser();
	try {
		while (true) {
			const { done, value } = await reader.read();
			if (done) break;
			for (const event of parser.push(decoder.decode(value, { stream: true }))) onEvent(event);
		}
		const tail = decoder.decode();
		for (const event of parser.push(tail)) onEvent(event);
		for (const event of parser.end()) onEvent(event);
	} finally {
		reader.releaseLock();
	}
}
