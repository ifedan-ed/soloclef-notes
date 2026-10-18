/**
 * A mind map's Markdown (the artifact's ``markdown_representation``, which the
 * web draws with markmap) as a tree: headings nest by level, and list items
 * nest under the heading above them by indentation — the same hierarchy
 * markmap builds. Labels are plain text (links and emphasis are dropped, as
 * the web viewer strips links too).
 */
import { inlineText, parseInline } from "./parse";

export interface MindNode {
	id: string;
	label: string;
	children: MindNode[];
}

const HEADING = /^(#{1,6})\s+(.*?)\s*#*\s*$/;
const LIST_ITEM = /^(\s*)(?:[-*+]|\d{1,9}[.)])\s+(.*)$/;

function label(text: string): string {
	return inlineText(parseInline(text.trim())).trim();
}

export function parseMindMap(markdown: string, fallbackTitle = ""): MindNode | null {
	// Strip YAML front matter (markmap options) if present.
	const body = markdown.replace(/^---\n[\s\S]*?\n---\n/, "");
	const root: MindNode = { id: "root", label: fallbackTitle, children: [] };
	// Stack of [depth, node]; headings use depth = level * 100, lists add indent.
	const stack: { depth: number; node: MindNode }[] = [{ depth: -1, node: root }];
	let headingDepth = 0;
	let count = 0;

	const attach = (depth: number, text: string, isHeading: boolean) => {
		const name = label(text);
		if (!name) return;
		// A heading closes every list above it; a list item closes deeper items.
		while (
			stack.length > 1 &&
			(stack[stack.length - 1].depth >= depth || (isHeading && stack[stack.length - 1].depth % 100 !== 0))
		) {
			stack.pop();
		}
		const parent = stack[stack.length - 1].node;
		const node: MindNode = { id: `${parent.id}.${parent.children.length}`, label: name, children: [] };
		parent.children.push(node);
		stack.push({ depth, node });
		count += 1;
	};

	for (const line of body.replace(/\r\n?/g, "\n").split("\n")) {
		const heading = HEADING.exec(line);
		if (heading) {
			headingDepth = heading[1].length * 100;
			attach(headingDepth, heading[2], true);
			continue;
		}
		const item = LIST_ITEM.exec(line);
		if (item) {
			const indent = item[1].replace(/\t/g, "    ").length;
			attach(headingDepth + 1 + indent, item[2], false);
		}
	}

	if (count === 0) return null;
	// A single top heading is the map's centre, as in markmap.
	if (root.children.length === 1 && (!fallbackTitle || root.children[0].children.length > 0)) {
		return reId(root.children[0], "root");
	}
	if (!root.label) root.label = fallbackTitle || "Mind map";
	return root;
}

function reId(node: MindNode, id: string): MindNode {
	return {
		id,
		label: node.label,
		children: node.children.map((child, index) => reId(child, `${id}.${index}`)),
	};
}

export function countNodes(node: MindNode): number {
	return 1 + node.children.reduce((sum, child) => sum + countNodes(child), 0);
}
