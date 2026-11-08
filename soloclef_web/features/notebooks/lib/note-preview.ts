import { plainText } from "@/lib/markdown/plain-text";

/** A note row's opening words: the note as plain text on one line (lib/markdown/plain-text.ts). */
export function notePreview(content: string): string {
	return plainText(content).replace(/\s+/g, " ").trim();
}
