/**
 * The saved mind map, as a plain tree.
 *
 * A mind map is stored as Markdown (headings and nested lists), the same
 * source it has always had; markmap-lib parses it, so every mind map saved
 * before this viewer still opens. Labels are plain text: links and images
 * are dropped, HTML is escaped.
 */

export interface MindMapNode {
	id: string;
	label: string;
	depth: number;
	children: MindMapNode[];
}

interface ParsedNode {
	content?: unknown;
	children?: unknown;
}

export const MAX_SOURCE_LENGTH = 50_000;
const MAX_NODES = 1_000;
const MAX_DEPTH = 32;

/** Build the tree from a parsed Markdown root; ``textOf`` turns node HTML into text. */
export function toMindMapTree(root: unknown, textOf: (html: string) => string): MindMapNode {
	let count = 0;

	function visit(value: unknown, depth: number, id: string): MindMapNode {
		if (depth > MAX_DEPTH || ++count > MAX_NODES || typeof value !== "object" || value === null) {
			throw new Error("Invalid mind-map hierarchy");
		}
		const node = value as ParsedNode;
		if (typeof node.content !== "string") throw new Error("Invalid mind-map node");
		const label = textOf(node.content).replace(/\s+/g, " ").trim();
		if (!label) throw new Error("Mind-map nodes need labels");
		const children = node.children ?? [];
		if (!Array.isArray(children)) throw new Error("Invalid mind-map children");
		return {
			id,
			label,
			depth,
			children: children.map((child, index) => visit(child, depth + 1, `${id}.${index}`)),
		};
	}

	const tree = visit(root, 0, "root");
	if (tree.children.length === 0) throw new Error("Mind map needs at least one child");
	return tree;
}

export function walk(node: MindMapNode, visit: (node: MindMapNode) => void): void {
	visit(node);
	for (const child of node.children) walk(child, visit);
}

/** What opens first: the root and its first level, as NotebookLM does. */
export function initiallyExpanded(tree: MindMapNode): Set<string> {
	return new Set([tree.id]);
}

/** Every node that has children. */
export function allExpandable(tree: MindMapNode): Set<string> {
	const ids = new Set<string>();
	walk(tree, (node) => {
		if (node.children.length > 0) ids.add(node.id);
	});
	return ids;
}

/** Whether "Expand all" still has anything to open. */
export function hasCollapsed(tree: MindMapNode, expanded: ReadonlySet<string>): boolean {
	let collapsed = false;
	walk(tree, (node) => {
		if (node.children.length > 0 && !expanded.has(node.id)) collapsed = true;
	});
	return collapsed;
}

export function toggled(expanded: ReadonlySet<string>, id: string): Set<string> {
	const next = new Set(expanded);
	if (next.has(id)) next.delete(id);
	else next.add(id);
	return next;
}

export function findNode(tree: MindMapNode, id: string): MindMapNode | null {
	if (tree.id === id) return tree;
	for (const child of tree.children) {
		const found = findNode(child, id);
		if (found) return found;
	}
	return null;
}
