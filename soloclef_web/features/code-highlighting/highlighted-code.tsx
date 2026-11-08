"use client";

import { Fragment, type ReactNode, useMemo } from "react";

import { type HighlightedNode, highlightCode } from "@/features/code-highlighting/highlighter";
import { cn } from "@/lib/utils";

function render(nodes: HighlightedNode[], keyPrefix: string): ReactNode[] {
	return nodes.map((node, index) => {
		const key = `${keyPrefix}.${index}`;
		if (node.type === "text") {
			return <Fragment key={key}>{node.value}</Fragment>;
		}
		return (
			<span key={key} className={node.classes.join(" ")}>
				{render(node.children, key)}
			</span>
		);
	});
}

/**
 * Source code with highlight.js scope classes and no colour of its own: the
 * `--syntax-*` tokens in `globals.css` colour it, in whichever theme is active.
 */
export function HighlightedCode({
	language,
	source,
	className,
}: {
	language: string;
	source: string;
	className?: string;
}) {
	const nodes = useMemo(() => highlightCode(language, source), [language, source]);

	return <code className={cn("hljs", className)}>{render(nodes, "n")}</code>;
}
