import { type ResearchEvent, researchEventSchema } from "../model/research";

export interface SseDataReader {
	/** Feed the next decoded chunk; returns the data payloads it completed. */
	push: (chunk: string) => string[];
	/** The stream closed: return a final payload left without its blank line. */
	end: () => string[];
}

/**
 * Reassembles Server-Sent Events from arbitrary network chunks. A chunk may
 * end mid-line, mid-"\r\n" or mid-event, so nothing is emitted until the
 * blank line that closes an event arrives.
 */
export function createSseDataReader(): SseDataReader {
	let buffer = "";
	let dataLines: string[] = [];

	const takeLine = (line: string, out: string[]) => {
		if (line === "") {
			if (dataLines.length > 0) out.push(dataLines.join("\n"));
			dataLines = [];
			return;
		}
		if (line.startsWith(":")) return;
		const colon = line.indexOf(":");
		const field = colon === -1 ? line : line.slice(0, colon);
		if (field !== "data") return;
		const value = colon === -1 ? "" : line.slice(colon + 1);
		dataLines.push(value.startsWith(" ") ? value.slice(1) : value);
	};

	return {
		push(chunk) {
			buffer += chunk;
			// A trailing "\r" may be the first half of "\r\n"; wait for the next chunk.
			const holdCarriageReturn = buffer.endsWith("\r");
			const ready = holdCarriageReturn ? buffer.slice(0, -1) : buffer;
			const lines = ready.replace(/\r\n?/g, "\n").split("\n");
			buffer = (lines.pop() ?? "") + (holdCarriageReturn ? "\r" : "");
			const out: string[] = [];
			for (const line of lines) takeLine(line, out);
			return out;
		},
		end() {
			const out: string[] = [];
			const rest = buffer.replace(/\r\n?/g, "\n").split("\n");
			buffer = "";
			for (const line of rest) takeLine(line, out);
			takeLine("", out);
			return out;
		},
	};
}

/** One research event from an SSE payload, or null when it is not one we know. */
export function parseResearchEvent(data: string): ResearchEvent | null {
	let json: unknown;
	try {
		json = JSON.parse(data);
	} catch {
		return null;
	}
	const parsed = researchEventSchema.safeParse(json);
	return parsed.success ? parsed.data : null;
}
