// A list inside a quote ("> - a point") is a list in Markdown, but Plate's
// quote holds lines of text only: on reading, its list items were dropped
// and a quoted answer's points showed as nothing. Here such a list becomes
// lines of the quote, each with its bullet or number ("• a point", "2. the next"),
// nested items indented, so every word shows. An edited note writes them back
// as those lines.

type MdNode = {
	type: string;
	children?: MdNode[];
	value?: string;
	ordered?: boolean | null;
	start?: number | null;
	checked?: boolean | null;
};

const INDENT = "\u00a0\u00a0\u00a0";

/** The list's items as lines of words, nested items indented. */
function itemsAsLines(list: MdNode, depth: number): MdNode[][] {
	const lines: MdNode[][] = [];
	(list.children ?? []).forEach((item, index) => {
		const mark = list.ordered
			? `${(list.start ?? 1) + index}. `
			: item.checked === true
				? "\u2611 "
				: item.checked === false
					? "\u2610 "
					: "\u2022 ";
		let first = true;
		for (const child of item.children ?? []) {
			if (child.type === "list") {
				lines.push(...itemsAsLines(child, depth + 1));
				continue;
			}
			const lead = INDENT.repeat(depth) + (first ? mark : INDENT);
			first = false;
			const words =
				child.type === "paragraph" || child.type === "heading"
					? (child.children ?? [])
					: [{ type: "text", value: child.value ?? "" }];
			lines.push([{ type: "text", value: lead }, ...words]);
		}
	});
	return lines;
}

/** A list as one paragraph of the quote, a line per item. */
function listAsParagraph(list: MdNode): MdNode {
	const children = itemsAsLines(list, 0).flatMap((line, i) =>
		i === 0 ? line : [{ type: "break" }, ...line]
	);
	return { type: "paragraph", children };
}

function walk(node: MdNode): void {
	if (!node.children) return;
	if (node.type === "blockquote") {
		node.children = node.children.flatMap((child) =>
			child.type === "list" ? [listAsParagraph(child)] : [child]
		);
	}
	for (const child of node.children) walk(child);
}

/** remark plugin: lists in quotes as lines of the quote. */
export function remarkQuotedListsAsLines() {
	return (tree: MdNode) => walk(tree);
}
