/**
 * Where each visible node of a mind map sits: a horizontal tree, root on the
 * left, each child a fixed gap to the right of its parent and centred on its
 * own children, with curved connectors from the parent's chevron.
 */
import type { MindMapNode } from "./mindmap-tree";

export const FONT_SIZE = 14;
export const ROOT_FONT_SIZE = 15;
export const LINE_HEIGHT = 20;
export const PAD_X = 14;
export const PAD_Y = 8;
export const MAX_TEXT_WIDTH = 220;
export const ROOT_MAX_TEXT_WIDTH = 260;
/** The expand/collapse button, just right of a pill. */
export const CHEVRON_SIZE = 22;
export const CHEVRON_GAP = 4;
/** From a pill's right edge to its children's left edge. */
export const H_GAP = 76;
export const V_GAP = 12;

export interface NodeBox {
	lines: string[];
	width: number;
	height: number;
}

export interface PlacedNode {
	node: MindMapNode;
	parentId: string | null;
	x: number;
	y: number;
	width: number;
	height: number;
	lines: string[];
	expanded: boolean;
}

export interface Layout {
	nodes: PlacedNode[];
	byId: Map<string, PlacedNode>;
	/** Bounding box of every pill and chevron. */
	bounds: { x: number; y: number; width: number; height: number };
}

export type MeasureText = (text: string, fontSize: number, bold: boolean) => number;

/** Greedy word wrap to ``maxWidth``; a single overlong word gets its own line. */
export function wrapLabel(
	label: string,
	maxWidth: number,
	measure: (text: string) => number
): string[] {
	const lines: string[] = [];
	let current = "";
	for (const word of label.split(" ")) {
		const candidate = current ? `${current} ${word}` : word;
		if (current && measure(candidate) > maxWidth) {
			lines.push(current);
			current = word;
		} else {
			current = candidate;
		}
	}
	if (current) lines.push(current);
	return lines;
}

/** The pill size of every node, measured once per tree. */
export function measureTree(tree: MindMapNode, measure: MeasureText): Map<string, NodeBox> {
	const boxes = new Map<string, NodeBox>();
	const visit = (node: MindMapNode) => {
		const root = node.depth === 0;
		const size = root ? ROOT_FONT_SIZE : FONT_SIZE;
		const maxWidth = root ? ROOT_MAX_TEXT_WIDTH : MAX_TEXT_WIDTH;
		const lines = wrapLabel(node.label, maxWidth, (text) => measure(text, size, root));
		const textWidth = Math.max(...lines.map((line) => measure(line, size, root)));
		boxes.set(node.id, {
			lines,
			width: Math.ceil(Math.min(textWidth, maxWidth) + PAD_X * 2),
			height: lines.length * LINE_HEIGHT + PAD_Y * 2,
		});
		for (const child of node.children) visit(child);
	};
	visit(tree);
	return boxes;
}

/** Lay out the nodes whose ancestors are all expanded. */
export function layoutTree(
	tree: MindMapNode,
	boxes: ReadonlyMap<string, NodeBox>,
	expanded: ReadonlySet<string>
): Layout {
	const span = new Map<string, number>();
	const showsChildren = (node: MindMapNode) => node.children.length > 0 && expanded.has(node.id);

	const measureSpan = (node: MindMapNode): number => {
		const box = boxes.get(node.id);
		if (!box) throw new Error(`unmeasured node ${node.id}`);
		let total = box.height;
		if (showsChildren(node)) {
			const children = node.children.reduce((sum, child) => sum + measureSpan(child), 0);
			total = Math.max(box.height, children + V_GAP * (node.children.length - 1));
		}
		span.set(node.id, total);
		return total;
	};
	measureSpan(tree);

	const nodes: PlacedNode[] = [];
	const place = (node: MindMapNode, parentId: string | null, x: number, top: number): number => {
		const box = boxes.get(node.id) as NodeBox;
		const total = span.get(node.id) as number;
		let centre = top + total / 2;
		const open = showsChildren(node);
		const placed: PlacedNode = {
			node,
			parentId,
			x,
			y: 0,
			width: box.width,
			height: box.height,
			lines: box.lines,
			expanded: open,
		};
		nodes.push(placed);
		if (open) {
			const childrenSpan =
				node.children.reduce((sum, child) => sum + (span.get(child.id) as number), 0) +
				V_GAP * (node.children.length - 1);
			let childTop = top + (total - childrenSpan) / 2;
			const centres: number[] = [];
			for (const child of node.children) {
				centres.push(place(child, node.id, x + box.width + H_GAP, childTop));
				childTop += (span.get(child.id) as number) + V_GAP;
			}
			centre = (centres[0] + centres[centres.length - 1]) / 2;
		}
		placed.y = centre - box.height / 2;
		return centre;
	};
	place(tree, null, 0, 0);

	let minX = Number.POSITIVE_INFINITY;
	let minY = Number.POSITIVE_INFINITY;
	let maxX = Number.NEGATIVE_INFINITY;
	let maxY = Number.NEGATIVE_INFINITY;
	for (const placed of nodes) {
		const right =
			placed.x + placed.width + (placed.node.children.length > 0 ? CHEVRON_GAP + CHEVRON_SIZE : 0);
		minX = Math.min(minX, placed.x);
		minY = Math.min(minY, placed.y);
		maxX = Math.max(maxX, right);
		maxY = Math.max(maxY, placed.y + placed.height);
	}
	return {
		nodes,
		byId: new Map(nodes.map((placed) => [placed.node.id, placed])),
		bounds: { x: minX, y: minY, width: maxX - minX, height: maxY - minY },
	};
}

/** Centre of a node's chevron, where its connectors start. */
export function chevronCentre(placed: { x: number; y: number; width: number; height: number }) {
	return {
		x: placed.x + placed.width + CHEVRON_GAP + CHEVRON_SIZE / 2,
		y: placed.y + placed.height / 2,
	};
}

/** A smooth S-curve from the parent's chevron to the child's left edge. */
export function connectorPath(
	parent: { x: number; y: number; width: number; height: number },
	child: { x: number; y: number; height: number }
): string {
	const start = chevronCentre(parent);
	const x1 = start.x + CHEVRON_SIZE / 2;
	const y1 = start.y;
	const x2 = child.x;
	const y2 = child.y + child.height / 2;
	const mid = x1 + (x2 - x1) / 2;
	return `M${r(x1)},${r(y1)} C${r(mid)},${r(y1)} ${r(mid)},${r(y2)} ${r(x2)},${r(y2)}`;
}

function r(value: number): number {
	return Math.round(value * 10) / 10;
}

export interface View {
	x: number;
	y: number;
	k: number;
}

export const MIN_ZOOM = 0.2;
export const MAX_ZOOM = 3;

export function clampZoom(k: number): number {
	return Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, k));
}

/**
 * The opening view: as large as fits the width (never above 100%), centred
 * when it fits, else with the root's row in the middle.
 */
export function fitWidth(
	bounds: Layout["bounds"],
	viewport: { width: number; height: number },
	padding = 24
): View {
	const k = clampZoom(Math.min(1, (viewport.width - padding * 2) / Math.max(bounds.width, 1)));
	const x = (viewport.width - bounds.width * k) / 2 - bounds.x * k;
	const y = (viewport.height - bounds.height * k) / 2 - bounds.y * k;
	return { x, y, k };
}

/**
 * The branch the reader just opened or closed, brought into view: the node,
 * its chevron and (when open) its children with their chevrons. The view
 * moves only as far as needed, and never so far that the clicked node's left
 * edge or top leaves the screen — so on a very wide or tall branch the node
 * itself stays visible and the reader pans for the rest.
 */
export function revealBranch(
	view: View,
	layout: Layout,
	nodeId: string,
	open: boolean,
	viewport: { width: number; height: number },
	padding = 24
): View {
	const placed = layout.byId.get(nodeId);
	if (!placed || viewport.width === 0) return view;
	const right = (p: typeof placed) =>
		p.x + p.width + (p.node.children.length > 0 ? CHEVRON_GAP + CHEVRON_SIZE : 0);
	let minY = placed.y;
	let maxY = placed.y + placed.height;
	let maxX = right(placed);
	if (open) {
		for (const child of placed.node.children) {
			const c = layout.byId.get(child.id);
			if (!c) continue;
			minY = Math.min(minY, c.y);
			maxY = Math.max(maxY, c.y + c.height);
			maxX = Math.max(maxX, right(c));
		}
	}
	const { k } = view;
	let dx = 0;
	const screenRight = maxX * k + view.x;
	if (screenRight > viewport.width - padding) dx = viewport.width - padding - screenRight;
	const nodeLeft = placed.x * k + view.x + dx;
	if (nodeLeft < padding) dx += padding - nodeLeft;
	let dy = 0;
	const screenBottom = maxY * k + view.y;
	const screenTop = minY * k + view.y;
	if (screenBottom > viewport.height - padding) dy = viewport.height - padding - screenBottom;
	if (screenTop + dy < padding) dy = padding - screenTop;
	const nodeTop = placed.y * k + view.y + dy;
	const nodeBottom = (placed.y + placed.height) * k + view.y + dy;
	if (nodeTop < padding) dy += padding - nodeTop;
	else if (nodeBottom > viewport.height - padding) dy -= nodeBottom - (viewport.height - padding);
	return dx === 0 && dy === 0 ? view : { ...view, x: view.x + dx, y: view.y + dy };
}

/** The whole tree inside the viewport. */
export function fitAll(
	bounds: Layout["bounds"],
	viewport: { width: number; height: number },
	padding = 24
): View {
	const k = clampZoom(
		Math.min(
			1.25,
			(viewport.width - padding * 2) / Math.max(bounds.width, 1),
			(viewport.height - padding * 2) / Math.max(bounds.height, 1)
		)
	);
	return {
		x: (viewport.width - bounds.width * k) / 2 - bounds.x * k,
		y: (viewport.height - bounds.height * k) / 2 - bounds.y * k,
		k,
	};
}

/** Zoom by ``factor`` keeping the screen point ``at`` still. */
export function zoomAt(view: View, factor: number, at: { x: number; y: number }): View {
	const k = clampZoom(view.k * factor);
	const ratio = k / view.k;
	return { k, x: at.x - (at.x - view.x) * ratio, y: at.y - (at.y - view.y) * ratio };
}

/** The same layout moved by (dx, dy), so a toggled node stays where it was. */
export function shiftLayout(layout: Layout, dx: number, dy: number): Layout {
	if (dx === 0 && dy === 0) return layout;
	const nodes = layout.nodes.map((placed) => ({ ...placed, x: placed.x + dx, y: placed.y + dy }));
	return {
		nodes,
		byId: new Map(nodes.map((placed) => [placed.node.id, placed])),
		bounds: { ...layout.bounds, x: layout.bounds.x + dx, y: layout.bounds.y + dy },
	};
}
