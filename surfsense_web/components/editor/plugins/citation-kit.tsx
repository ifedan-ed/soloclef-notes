"use client";

import { createPlatePlugin, type PlateElementProps } from "platejs/react";
import type { FC } from "react";
import { InlineCitation, UrlCitation } from "@/components/assistant-ui/inline-citation";
import { RunCitation } from "@/components/citations/run-citation";
import {
	CITATION_NODE,
	CITATION_TYPE,
	type CitationElementNode,
} from "@/components/editor/utils/citation-nodes";

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
	return (
		<span {...attributes} data-citation="" className="inline-flex align-baseline">
			<span contentEditable={false}>
				{isUrl && element.url ? (
					<UrlCitation url={element.url} />
				) : element.kind === "run" && element.runId ? (
					<RunCitation runId={element.runId} />
				) : element.chunkId !== undefined ? (
					<InlineCitation chunkId={element.chunkId} isDocsChunk={element.kind === "doc"} />
				) : null}
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
