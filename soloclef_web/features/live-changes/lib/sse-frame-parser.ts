/**
 * Server-Sent Events text framing, read incrementally from a fetch body.
 *
 * EventSource would parse this for us, but the stream is opened with fetch
 * (open-change-stream.ts says why), so it is framed here. Network
 * chunks split anywhere (inside a line, between "\r" and "\n"), so whatever
 * follows the last complete line waits for the next chunk.
 */

/** One dispatched frame: its `data:` lines joined with "\n", and its `id:` if it had one. */
export interface SseFrame {
	id?: string;
	data: string;
}

export class SseFrameParser {
	private pending = "";
	private dataLines: string[] = [];
	private frameId: string | undefined;

	/** Feed decoded text; returns every frame the text completed, in order. */
	push(chunk: string): SseFrame[] {
		this.pending += chunk;
		const frames: SseFrame[] = [];
		const text = this.pending;
		let lineStart = 0;
		for (let i = 0; i < text.length; i++) {
			const char = text[i];
			if (char !== "\n" && char !== "\r") continue;
			// A "\r" that ends the chunk may be the first half of "\r\n": wait.
			if (char === "\r" && i === text.length - 1) break;
			const frame = this.readLine(text.slice(lineStart, i));
			if (frame) frames.push(frame);
			if (char === "\r" && text[i + 1] === "\n") i++;
			lineStart = i + 1;
		}
		this.pending = text.slice(lineStart);
		return frames;
	}

	private readLine(line: string): SseFrame | null {
		if (line === "") return this.dispatch();
		// A comment: the server's keepalive. The reader counts the bytes as
		// life before they get here, so there is nothing more to do with it.
		if (line.startsWith(":")) return null;
		const colon = line.indexOf(":");
		const field = colon === -1 ? line : line.slice(0, colon);
		let value = colon === -1 ? "" : line.slice(colon + 1);
		if (value.startsWith(" ")) value = value.slice(1);
		if (field === "data") this.dataLines.push(value);
		// The SSE spec ignores an id holding NUL; so does this.
		else if (field === "id" && !value.includes("\0")) this.frameId = value;
		// `event:` and `retry:` are not part of the contract: the type is in
		// the data, and reconnect timing is the client's own (reconnect-backoff.ts).
		return null;
	}

	private dispatch(): SseFrame | null {
		const id = this.frameId;
		const data = this.dataLines;
		this.frameId = undefined;
		this.dataLines = [];
		if (data.length === 0) return null;
		return id === undefined ? { data: data.join("\n") } : { id, data: data.join("\n") };
	}
}
