"use client";

import { createPlatePlugin, type PlateElementProps, useEditorSelector } from "platejs/react";
import type { FC } from "react";
import { InlineCitation, UrlCitation } from "@/components/assistant-ui/inline-citation";
import { SourceCitation } from "@/components/assistant-ui/source-citation";
import { CitationNumberingProvider } from "@/components/citations/citation-numbers-context";
import {
	CITATION_NODE,
	CITATION_TYPE,
	type CitationElementNode,
	citationOfNode,
	numberCitationNodes,
} from "@/components/editor/utils/citation-nodes";
import { citationKey } from "@/lib/citations/citation-numbering";

export {
	type CitationElementNode,
	citationMarkdownRules,
	injectCitationNodes,
} from "@/components/editor/utils/citation-nodes";

const CitationElement: FC<PlateElementProps<CitationElementNode>> = ({
	attributes,
	children,
	element,
}) => {
	const isUrl = element.kind === "url";
	// The note's chips are numbered 1, 2, 3 in reading order, as an answer's
	// are. The numbering is of the tree as it is now (cached per tree), so
	// deleting a chip renumbers the ones after it.
	// Speed: every keystroke makes a new tree, but a chip re-renders only
	// when its own number changes.
	const citation = citationOfNode(element);
	const key = citation ? citationKey(citation) : null;
	const numbering = useEditorSelector((editor) => numberCitationNodes(editor.children), [], {
		equalityFn: (a, b) => key === null || a.numbers.get(key) === b.numbers.get(key),
	});
	return (
		<span {...attributes} data-citation="" className="inline-flex align-baseline">
			<span contentEditable={false}>
				<CitationNumberingProvider numbering={numbering}>
					{isUrl && element.url ? (
						<UrlCitation url={element.url} />
					) : citation?.kind === "source" ? (
						<SourceCitation citation={citation} />
					) : element.chunkId !== undefined ? (
						<InlineCitation chunkId={element.chunkId} isDocsChunk={element.kind === "doc"} />
					) : null}
				</CitationNumberingProvider>
			</span>
			{children}
		</span>
	);
};

const CitationPlugin = createPlatePlugin({
	key: CITATION_TYPE,
	node: { ...CITATION_NODE, component: CitationElement },
});

export const CitationKit = [CitationPlugin];
