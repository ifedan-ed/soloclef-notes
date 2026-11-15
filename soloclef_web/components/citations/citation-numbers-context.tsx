"use client";

import { createContext, type FC, type ReactNode, useContext, useMemo, useRef } from "react";
import {
	type CitationNumbering,
	citationKey,
	EMPTY_NUMBERING,
	type NumberedCitation,
	numberCitations,
} from "@/lib/citations/citation-numbering";

interface CitationNumbersValue {
	numbering: CitationNumbering;
	/** True while the answer streams: bare "[3]" labels are chips too. */
	labels: boolean;
}

const CitationNumbersContext = createContext<CitationNumbersValue | null>(null);

/**
 * Numbers the citations of one answer, note or document for every chip
 * inside it (lib/citations/citation-numbering.ts has the rule).
 *
 * The numbering is worked out from the whole text up front, not counted as
 * chips render: a chip in a later text part, or one Markdown renders out of
 * order (a table cell, a footnote), must get the number a reader expects.
 *
 * Speed: while an answer streams, `texts` changes on every token. The value
 * handed down keeps its identity until a new source appears, so the chips
 * already on screen do not re-render on each token.
 */
export const CitationNumbersProvider: FC<{
	texts: readonly string[];
	labels?: boolean;
	children: ReactNode;
}> = ({ texts, labels = false, children }) => {
	const previous = useRef<CitationNumbersValue | null>(null);
	const value = useMemo(() => {
		const numbering = numberCitations(texts, { labels });
		const last = previous.current;
		if (
			last &&
			last.labels === labels &&
			last.numbering.order.length === numbering.order.length &&
			last.numbering.order.every((key, i) => key === numbering.order[i])
		) {
			return last;
		}
		const next = { numbering, labels };
		previous.current = next;
		return next;
	}, [texts, labels]);
	return (
		<CitationNumbersContext.Provider value={value}>{children}</CitationNumbersContext.Provider>
	);
};

/**
 * Hands down a numbering worked out elsewhere: the note editor numbers its
 * chips from its own tree (editor/utils/citation-nodes.ts), not from text.
 */
export const CitationNumberingProvider: FC<{
	numbering: CitationNumbering;
	children: ReactNode;
}> = ({ numbering, children }) => {
	const value = useMemo(() => ({ numbering, labels: false }), [numbering]);
	return (
		<CitationNumbersContext.Provider value={value}>{children}</CitationNumbersContext.Provider>
	);
};

/**
 * The number a citation shows, or null where it has none: a legacy marker,
 * or a chip drawn outside any provider (which would be a surface this change
 * missed; it then shows a source icon, never a passage id).
 */
export function useCitationNumber(citation: NumberedCitation): number | null {
	const value = useContext(CitationNumbersContext);
	const key = citationKey(citation);
	if (!value || key === null) return null;
	return value.numbering.numbers.get(key) ?? null;
}

/** Whether streamed "[3]" labels are drawn as chips here. */
export function useCitationLabels(): boolean {
	return useContext(CitationNumbersContext)?.labels ?? false;
}

/** The current numbering, for a copy or download made from the same text. */
export function useCitationNumbering(): CitationNumbering {
	return useContext(CitationNumbersContext)?.numbering ?? EMPTY_NUMBERING;
}
