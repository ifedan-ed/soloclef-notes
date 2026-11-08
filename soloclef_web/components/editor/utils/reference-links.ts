// A reference link ("[the review][1]" with "[1]: https://…" further down)
// and a reference image are Markdown Plate does not read: their words were
// dropped. Here each becomes the link or image its definition gives (no
// definition: its words as written), so the words show and the link works.

type MdNode = {
	type: string;
	children?: MdNode[];
	identifier?: string;
	label?: string | null;
	url?: string;
	title?: string | null;
	alt?: string | null;
	value?: string;
};

function definitions(node: MdNode, found: Map<string, MdNode>): Map<string, MdNode> {
	if (node.type === "definition" && node.identifier && !found.has(node.identifier)) {
		found.set(node.identifier, node);
	}
	for (const child of node.children ?? []) definitions(child, found);
	return found;
}

function resolve(node: MdNode, found: Map<string, MdNode>): void {
	if (!node.children) return;
	node.children = node.children.map((child) => {
		const target = child.identifier ? found.get(child.identifier) : undefined;
		if (child.type === "linkReference") {
			return target
				? { type: "link", url: target.url, title: target.title, children: child.children }
				: { type: "text", value: `[${child.label ?? child.identifier ?? ""}]` };
		}
		if (child.type === "imageReference") {
			return target
				? { type: "image", url: target.url, title: target.title, alt: child.alt }
				: { type: "text", value: child.alt ?? "" };
		}
		return child;
	});
	for (const child of node.children) resolve(child, found);
}

/** remark plugin: reference links and images as the links and images they name. */
export function remarkReferenceLinks() {
	return (tree: MdNode) => resolve(tree, definitions(tree, new Map()));
}
