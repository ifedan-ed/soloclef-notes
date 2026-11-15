"use client";

import { useAuiState } from "@assistant-ui/react";
import { useMemo } from "react";

const NO_TEXTS: readonly string[] = [];

/**
 * The current message's text parts, in order: what its chips are numbered
 * over and what its Copy and Download carry. Reasoning and tool cards are
 * not part of the answer's words.
 */
export function useAnswerTexts(): readonly string[] {
	// The content array keeps its identity until the message changes, so the
	// list is rebuilt only then (on each token while streaming).
	const content = useAuiState(({ message }) => message.content);
	return useMemo(() => {
		if (!Array.isArray(content)) return NO_TEXTS;
		const texts: string[] = [];
		for (const part of content) {
			if (part.type === "text" && typeof part.text === "string") texts.push(part.text);
		}
		return texts;
	}, [content]);
}
