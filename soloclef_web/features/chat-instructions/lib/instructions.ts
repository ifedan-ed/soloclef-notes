/**
 * The account's chat instructions on the web side: the length cap the
 * backend enforces, what each save-time warning means, and whether a chat
 * should say its answers are shaped by them.
 */

/** The API's limit (accountprofile.InstructionsMaxLength in soloclef_api). */
export const MAX_INSTRUCTIONS_CHARS = 4000;

export type LintCode = "citations" | "outside_knowledge" | "safety" | "prompt_injection";

export interface LintWarning {
	code: LintCode;
	quote: string;
	source: "rule" | "model";
}

const LINT_MESSAGES: Record<LintCode, string> = {
	citations:
		"This looks like it turns citations off. Answers will still cite their sources; this part will be ignored.",
	outside_knowledge:
		"This looks like it asks for answers beyond the sources. Answers stay grounded in the sources; this part will be ignored.",
	safety:
		"This looks like it changes safety behaviour. Safety rules always apply; this part will be ignored.",
	prompt_injection:
		"This looks like an attempt to override or reveal the assistant's rules. It will be treated as plain text and ignored.",
};

export function lintMessage(code: LintCode): string {
	return LINT_MESSAGES[code];
}

/** What is saved: trimmed, empty when blank. */
export function savedInstructions(text: string): string {
	return text.trim();
}

export function isOverLimit(text: string): boolean {
	return savedInstructions(text).length > MAX_INSTRUCTIONS_CHARS;
}

/** Whether the account's answers are shaped by its instructions. */
export function instructionsApplied(instructions: string | null | undefined): boolean {
	return !!instructions && instructions.trim().length > 0;
}
