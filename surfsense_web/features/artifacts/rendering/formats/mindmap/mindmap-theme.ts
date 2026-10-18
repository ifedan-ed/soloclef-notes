/**
 * Mind-map colours by depth, NotebookLM-like: a blue root, green first-level
 * branches, lighter greens further out. The canvas and the exported image
 * use the same values, so a download looks like the screen.
 */

export interface PillColours {
	fill: string;
	stroke: string;
	text: string;
}

export interface MindMapPalette {
	background: string;
	connector: string;
	chevronFill: string;
	chevronStroke: string;
	chevronIcon: string;
	depths: readonly PillColours[];
}

const LIGHT: MindMapPalette = {
	background: "#ffffff",
	connector: "#a3aab2",
	chevronFill: "#ffffff",
	chevronStroke: "#c4c9cf",
	chevronIcon: "#444746",
	depths: [
		{ fill: "#d3e3fd", stroke: "#a8c7fa", text: "#041e49" },
		{ fill: "#c4eed0", stroke: "#94d3a6", text: "#072711" },
		{ fill: "#e3f4e8", stroke: "#b5dec1", text: "#0d3a1c" },
		{ fill: "#f0f8f2", stroke: "#cfe8d6", text: "#1e3a28" },
	],
};

const DARK: MindMapPalette = {
	background: "#1b1c1d",
	connector: "#5f6368",
	chevronFill: "#2a2b2d",
	chevronStroke: "#4a4d51",
	chevronIcon: "#e3e3e3",
	depths: [
		{ fill: "#0b3a82", stroke: "#2356a8", text: "#d3e3fd" },
		{ fill: "#0f5223", stroke: "#23703a", text: "#c4eed0" },
		{ fill: "#173d24", stroke: "#2a5a3b", text: "#c9ebd3" },
		{ fill: "#1d3326", stroke: "#31503d", text: "#cfe3d6" },
	],
};

export function paletteFor(dark: boolean): MindMapPalette {
	return dark ? DARK : LIGHT;
}

export function pillColours(palette: MindMapPalette, depth: number): PillColours {
	return palette.depths[Math.min(depth, palette.depths.length - 1)];
}
