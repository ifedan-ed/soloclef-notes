/** The text of an assistant message's parts, joined as the answer reads. */
export function answerTextOf(content: unknown): string {
	if (!Array.isArray(content)) return "";
	return content
		.filter(
			(part): part is { type: "text"; text: string } =>
				typeof part === "object" &&
				part !== null &&
				(part as { type?: unknown }).type === "text" &&
				typeof (part as { text?: unknown }).text === "string"
		)
		.map((part) => part.text)
		.join("\n\n");
}
