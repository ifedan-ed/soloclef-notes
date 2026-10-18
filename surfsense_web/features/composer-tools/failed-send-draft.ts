import { atom } from "jotai";
import type { ComposerToolId } from "./composer-tools";

/** A message that never reached the server: its text and the tool picked for it. */
export interface FailedSendDraft {
	text: string;
	tool: ComposerToolId | null;
}

/** Set by the chat engine when a send fails before the server accepts it. */
export const failedSendDraftAtom = atom<FailedSendDraft | null>(null);

/**
 * What to put back in the composer after a failed send. The text returns only
 * to an empty composer, so nothing typed since is overwritten; the tool chip
 * returns unless another tool has been picked meanwhile.
 */
export function draftToRestore(
	composer: { isEmpty: boolean; tool: ComposerToolId | null },
	failed: FailedSendDraft
): { text: string | null; tool: ComposerToolId | null } {
	return {
		text: composer.isEmpty && failed.text.trim() ? failed.text : null,
		tool: composer.tool ?? failed.tool,
	};
}
