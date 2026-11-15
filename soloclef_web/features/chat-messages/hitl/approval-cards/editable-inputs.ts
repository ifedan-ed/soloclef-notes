/**
 * Which of a call's inputs the approval card's Edit can change.
 *
 * The card edits an input as one line of text and sends it back as a string.
 * That is right for a string, a number or a truth value, and wrong for a
 * list or an object: a list of sources edited this way came back as the text
 * "a,b", which the tool refuses, so approving an edit broke the call. Those
 * inputs are shown as they are and sent back unchanged; to change them, the
 * person rejects the call and asks again.
 */
export function isEditableInput(value: unknown): boolean {
	return (
		value === null ||
		value === undefined ||
		typeof value === "string" ||
		typeof value === "number" ||
		typeof value === "boolean"
	);
}

/** Whether the card should offer Edit at all: some input can be changed. */
export function hasEditableInput(args: Record<string, unknown>): boolean {
	return Object.values(args).some(isEditableInput);
}
