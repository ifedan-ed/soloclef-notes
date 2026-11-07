import { all, createLowlight } from "lowlight";

/**
 * One highlighter for the whole app, so the chat and the note editor never
 * disagree about which languages they know or which classes they emit.
 */
export const lowlight = createLowlight(all);

export type HighlightedNode =
	| { type: "text"; value: string }
	| { type: "element"; classes: string[]; children: HighlightedNode[] };

type HastNode = {
	type: string;
	value?: string;
	properties?: { className?: unknown };
	children?: HastNode[];
};

function classesOf(node: HastNode): string[] {
	const className = node.properties?.className;
	if (Array.isArray(className)) return className.map(String);
	if (typeof className === "string") return className.split(/\s+/).filter(Boolean);
	return [];
}

function convert(nodes: HastNode[]): HighlightedNode[] {
	const converted: HighlightedNode[] = [];
	for (const node of nodes) {
		if (node.type === "text") {
			converted.push({ type: "text", value: node.value ?? "" });
			continue;
		}
		if (node.type !== "element") continue;
		converted.push({
			type: "element",
			classes: classesOf(node),
			children: convert(node.children ?? []),
		});
	}
	return converted;
}

/**
 * Highlight source for display. A language highlight.js does not know, or
 * source it cannot parse, comes back as one unstyled run instead of throwing,
 * so an unrecognised fence still renders as code.
 */
export function highlightCode(language: string, source: string): HighlightedNode[] {
	const name = language.trim().toLowerCase();
	if (!name || !lowlight.registered(name)) {
		return source ? [{ type: "text", value: source }] : [];
	}
	try {
		return convert(lowlight.highlight(name, source).children as HastNode[]);
	} catch {
		return source ? [{ type: "text", value: source }] : [];
	}
}
