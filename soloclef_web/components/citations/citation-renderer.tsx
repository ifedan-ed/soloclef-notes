"use client";

import type { ReactNode } from "react";
import {
	InlineCitation,
	LabelCitation,
	UrlCitation,
} from "@/components/assistant-ui/inline-citation";
import { SourceCitation } from "@/components/assistant-ui/source-citation";
import { type CitationLabel, splitLabels } from "@/lib/citations/citation-numbering";
import {
	type CitationToken,
	type CitationUrlMap,
	type ParsedSegment,
	parseTextWithCitations,
} from "@/lib/citations/citation-parser";

/**
 * Render a single parsed citation token as JSX.
 *
 * `ordinalKey` should be a stable per-render counter so duplicate identical
 * citations within the same parent don't collide on `key`. The previous
 * implementation in `markdown-text.tsx` used the source string itself as
 * the key, which produced React warnings when two segments rendered the
 * same `[citation:N]` text.
 */
export function renderCitationToken(
	token: CitationToken | CitationLabel,
	ordinalKey: number
): ReactNode {
	if (token.kind === "label") {
		return <LabelCitation key={`citation-label-${ordinalKey}`} label={token.label} />;
	}
	if (token.kind === "url") {
		return <UrlCitation key={`citation-url-${ordinalKey}`} url={token.url} />;
	}
	if (token.kind === "source") {
		return <SourceCitation key={`citation-source-${ordinalKey}`} citation={token} />;
	}
	return (
		<InlineCitation
			key={`citation-${token.isDocsChunk ? "doc-" : ""}${token.chunkId}-${ordinalKey}`}
			chunkId={token.chunkId}
			isDocsChunk={token.isDocsChunk}
		/>
	);
}

/**
 * A text's markers, and while an answer streams its bare "[3]" labels too,
 * as citations between plain strings.
 */
function segmentsOf(
	text: string,
	urlMap: CitationUrlMap,
	labels: boolean
): (ParsedSegment | CitationLabel)[] {
	const segments = parseTextWithCitations(text, urlMap);
	if (!labels) return segments;
	return segments.flatMap((segment): (ParsedSegment | CitationLabel)[] =>
		typeof segment === "string" ? splitLabels(segment) : [segment]
	);
}

/**
 * Walk a `ReactNode` (string, array, or arbitrary node) and replace any
 * `[citation:...]` tokens inside string children with citation badges.
 *
 * Designed for use inside `Streamdown`/`react-markdown` `components`
 * overrides where the renderer hands you `children`. Non-string children
 * are returned untouched so block/phrasing structure is preserved.
 */
export function processChildrenWithCitations(
	children: ReactNode,
	urlMap: CitationUrlMap,
	labels = false
): ReactNode {
	if (typeof children === "string") {
		const segments = segmentsOf(children, urlMap, labels);
		if (segments.length === 1 && typeof segments[0] === "string") {
			return children;
		}
		let ordinal = 0;
		return segments.map((segment) =>
			typeof segment === "string" ? segment : renderCitationToken(segment, ordinal++)
		);
	}

	if (Array.isArray(children)) {
		let ordinal = 0;
		const occurrences = new Map<string, number>();
		return children.map((child) => {
			if (typeof child === "string") {
				const segments = segmentsOf(child, urlMap, labels);
				if (segments.length === 1 && typeof segments[0] === "string") {
					return child;
				}
				const occurrence = occurrences.get(child) ?? 0;
				occurrences.set(child, occurrence + 1);
				return (
					<span key={`citation-seg-${child}-${occurrence}`}>
						{segments.map((segment) =>
							typeof segment === "string" ? segment : renderCitationToken(segment, ordinal++)
						)}
					</span>
				);
			}
			return child;
		});
	}

	return children;
}
