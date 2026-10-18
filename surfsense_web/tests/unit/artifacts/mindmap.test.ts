import assert from "node:assert/strict";
import test from "node:test";
import {
	downloadName,
	mindMapSvg,
} from "@/features/artifacts/rendering/formats/mindmap/mindmap-export";
import {
	CHEVRON_SIZE,
	connectorPath,
	fitAll,
	fitWidth,
	H_GAP,
	layoutTree,
	MAX_ZOOM,
	MIN_ZOOM,
	measureTree,
	revealBranch,
	shiftLayout,
	wrapLabel,
	zoomAt,
} from "@/features/artifacts/rendering/formats/mindmap/mindmap-layout";
import {
	paletteFor,
	pillColours,
} from "@/features/artifacts/rendering/formats/mindmap/mindmap-theme";
import {
	allExpandable,
	hasCollapsed,
	initiallyExpanded,
	toggled,
	toMindMapTree,
} from "@/features/artifacts/rendering/formats/mindmap/mindmap-tree";

// markmap-lib's parse result for the e2e seed map (content is node HTML).
const PARSED = {
	content: "E2E mind map",
	children: [
		{
			content: "Photosynthesis",
			children: [{ content: "Light reactions" }, { content: "Calvin <b>cycle</b>" }],
		},
		{ content: "Respiration", children: [{ content: "Glycolysis" }, { content: "Krebs cycle" }] },
	],
};
const stripTags = (html: string) => html.replace(/<[^>]+>/g, "");
const measure = (text: string, size: number) => text.length * size * 0.5;

function tree() {
	return toMindMapTree(PARSED, stripTags);
}

test("the saved Markdown's parse becomes a plain labelled tree", () => {
	const root = tree();
	assert.equal(root.label, "E2E mind map");
	assert.equal(root.children[0].children[1].label, "Calvin cycle");
	assert.equal(root.children[1].children[0].depth, 2);
	assert.throws(() => toMindMapTree({ content: "Only root", children: [] }, stripTags));
	assert.throws(() => toMindMapTree({ content: "", children: [{ content: "x" }] }, stripTags));
});

test("it opens on the root and its first level; expand all and collapse all", () => {
	const root = tree();
	const boxes = measureTree(root, measure);
	const first = layoutTree(root, boxes, initiallyExpanded(root));
	assert.deepEqual(
		first.nodes.map((node) => node.node.label),
		["E2E mind map", "Photosynthesis", "Respiration"]
	);
	assert.equal(hasCollapsed(root, initiallyExpanded(root)), true);

	const all = layoutTree(root, boxes, allExpandable(root));
	assert.equal(all.nodes.length, 7);
	assert.equal(hasCollapsed(root, allExpandable(root)), false);

	const onlyRoot = layoutTree(root, boxes, new Set());
	assert.equal(onlyRoot.nodes.length, 1);

	const photosynthesis = root.children[0].id;
	const opened = toggled(initiallyExpanded(root), photosynthesis);
	assert.ok(layoutTree(root, boxes, opened).byId.has(root.children[0].children[1].id));
	assert.equal(toggled(opened, photosynthesis).has(photosynthesis), false);
});

test("branches flow left to right and a parent is centred on its children", () => {
	const root = tree();
	const layout = layoutTree(root, measureTree(root, measure), allExpandable(root));
	const rootNode = layout.byId.get(root.id);
	const photosynthesis = layout.byId.get(root.children[0].id);
	const respiration = layout.byId.get(root.children[1].id);
	assert.ok(rootNode && photosynthesis && respiration);
	assert.equal(photosynthesis.x, rootNode.x + rootNode.width + H_GAP);
	assert.ok(respiration.y > photosynthesis.y + photosynthesis.height);
	const centre = (node: { y: number; height: number }) => node.y + node.height / 2;
	assert.equal(centre(rootNode), (centre(photosynthesis) + centre(respiration)) / 2);
	for (const node of layout.nodes) {
		assert.ok(node.x >= layout.bounds.x && node.y >= layout.bounds.y);
	}
	// The connector starts right of the parent's chevron and ends on the child's edge.
	const path = connectorPath(rootNode, photosynthesis);
	assert.match(path, /^M[\d.]+,[\d.-]+ C/);
	const startX = Number(path.slice(1).split(",")[0]);
	assert.ok(startX > rootNode.x + rootNode.width + CHEVRON_SIZE / 2);
	assert.ok(path.endsWith(`${photosynthesis.x},${centre(photosynthesis)}`));
});

test("long labels wrap within the pill width", () => {
	const lines = wrapLabel("one two three four five six seven", 60, (text) => text.length * 7);
	assert.ok(lines.length > 1);
	assert.equal(lines.join(" "), "one two three four five six seven");
	for (const line of lines) assert.ok(line.length * 7 <= 60 || !line.includes(" "));
});

test("the view fits the width on open, never above 100%, and zoom is clamped", () => {
	const bounds = { x: 0, y: -50, width: 1000, height: 300 };
	const view = fitWidth(bounds, { width: 400, height: 600 });
	assert.ok(Math.abs(view.k - (400 - 48) / 1000) < 1e-9);
	// Centred: equal space left and right of the tree.
	assert.ok(Math.abs(view.x - (400 - 1000 * view.k) / 2) < 1e-9);
	assert.equal(
		fitWidth({ x: 0, y: 0, width: 9000, height: 40 }, { width: 400, height: 600 }).k,
		MIN_ZOOM
	);
	assert.equal(fitWidth({ x: 0, y: 0, width: 100, height: 40 }, { width: 800, height: 600 }).k, 1);
	assert.ok(Math.abs(fitAll(bounds, { width: 400, height: 400 }).k - (400 - 48) / 1000) < 1e-9);
	assert.ok(Math.abs(fitAll(bounds, { width: 4000, height: 400 }).k - (400 - 48) / 300) < 1e-9);

	const zoomed = zoomAt({ x: 10, y: 20, k: 1 }, 2, { x: 100, y: 100 });
	assert.equal(zoomed.k, 2);
	// The point under the cursor stays put.
	assert.equal((100 - zoomed.x) / zoomed.k, (100 - 10) / 1);
	assert.equal(zoomAt({ x: 0, y: 0, k: 1 }, 100, { x: 0, y: 0 }).k, MAX_ZOOM);
	assert.equal(zoomAt({ x: 0, y: 0, k: 1 }, 0.001, { x: 0, y: 0 }).k, MIN_ZOOM);
});

test("a toggle can keep the clicked node where it was", () => {
	const root = tree();
	const boxes = measureTree(root, measure);
	const layout = layoutTree(root, boxes, initiallyExpanded(root));
	const shifted = shiftLayout(layout, 5, -7);
	const before = layout.byId.get(root.id);
	const after = shifted.byId.get(root.id);
	assert.ok(before && after);
	assert.deepEqual([after.x - before.x, after.y - before.y], [5, -7]);
	assert.equal(shifted.bounds.y, layout.bounds.y - 7);
});

test("colours follow depth in both themes", () => {
	const light = paletteFor(false);
	const dark = paletteFor(true);
	assert.notEqual(light.background, dark.background);
	assert.notEqual(pillColours(light, 0).fill, pillColours(light, 1).fill);
	assert.equal(pillColours(light, 9), pillColours(light, 3));
});

test("the download is the expanded tree as a standalone SVG", () => {
	const root = tree();
	const layout = layoutTree(root, measureTree(root, measure), allExpandable(root));
	const svg = mindMapSvg(layout, paletteFor(false), 'Inter, "Segoe UI"');
	assert.ok(svg.startsWith('<svg xmlns="http://www.w3.org/2000/svg"'));
	assert.ok(svg.includes("Calvin cycle"));
	assert.equal((svg.match(/<path /g) ?? []).length, 6);
	// Fully expanded: no chevron buttons in the picture.
	assert.equal((svg.match(/<circle /g) ?? []).length, 0);
	// ...and a stub joins each expanded pill to its connectors.
	assert.equal((svg.match(/<line /g) ?? []).length, 3);
	// The page font first, then sans-serif fallbacks (never Times).
	assert.ok(
		svg.includes(
			'font-family="Inter, &quot;Segoe UI&quot;, Roboto, &quot;Helvetica Neue&quot;, Arial, sans-serif"'
		)
	);
	assert.ok(!svg.includes("<style>"));
	const withFont = mindMapSvg(layout, paletteFor(false), "Inter", "@font-face{font-family:Inter}");
	assert.ok(withFont.includes("<style>@font-face{font-family:Inter}</style>"));
	// A collapsed branch keeps a small chevron so the picture shows it continues.
	const collapsed = mindMapSvg(
		layoutTree(root, measureTree(root, measure), new Set(["root"])),
		paletteFor(false),
		"Inter"
	);
	assert.ok((collapsed.match(/<circle /g) ?? []).length > 0);
	const escaped = mindMapSvg(
		layoutTree(
			toMindMapTree({ content: "A & <B>", children: [{ content: "c" }] }, (text) => text),
			measureTree(
				toMindMapTree({ content: "A & <B>", children: [{ content: "c" }] }, (text) => text),
				measure
			),
			new Set(["root"])
		),
		paletteFor(true),
		"sans-serif"
	);
	assert.ok(escaped.includes("A &amp; &lt;B&gt;"));
	assert.equal(downloadName('Cells: a/b "map"', "png"), "Cells a b map.png");
	assert.equal(downloadName("", "svg"), "mind-map.svg");
});

test("opening a branch near the right edge brings its children into view", () => {
	const root = tree();
	const boxes = measureTree(root, measure);
	const first = root.children[0];
	const open = new Set([root.id, first.id]);
	const layout = layoutTree(root, boxes, open);
	const viewport = { width: 400, height: 300 };
	const placed = layout.byId.get(first.id);
	assert.ok(placed);
	// A view where the clicked node sits near the right edge.
	const view = { x: viewport.width - (placed.x + placed.width) - 30, y: 150 - placed.y, k: 1 };
	const next = revealBranch(view, layout, first.id, true, viewport);
	assert.ok(next.x < view.x, "moves left");
	for (const child of first.children) {
		const c = layout.byId.get(child.id);
		assert.ok(c);
		assert.ok(c.x + c.width + next.x <= viewport.width - 24 + CHEVRON_SIZE + 5, "child in view");
	}
	assert.ok(placed.x + next.x >= 24, "the clicked node stays on screen");
});

test("a branch already in view doesn't move the map", () => {
	const root = tree();
	const layout = layoutTree(root, measureTree(root, measure), new Set([root.id]));
	const view = fitAll(layout.bounds, { width: 2000, height: 1500 });
	assert.equal(revealBranch(view, layout, root.id, true, { width: 2000, height: 1500 }), view);
});

test("closing a branch keeps its chevron on screen", () => {
	const root = tree();
	const first = root.children[0];
	const layout = layoutTree(root, measureTree(root, measure), new Set([root.id]));
	const placed = layout.byId.get(first.id);
	assert.ok(placed);
	const viewport = { width: 300, height: 300 };
	const view = { x: viewport.width - placed.x - placed.width, y: 100 - placed.y, k: 1 };
	const next = revealBranch(view, layout, first.id, false, viewport);
	const chevronRight = placed.x + placed.width + 4 + CHEVRON_SIZE;
	assert.ok(chevronRight + next.x <= viewport.width - 24 + 0.001);
});
