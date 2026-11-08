import type { LucideIcon } from "lucide-react";
import {
	AudioLines,
	FileText,
	ImageIcon,
	ListChecks,
	Network,
	PlayingCardsFan,
	Presentation,
	Sheet,
	Video,
} from "lucide-react";
import type { StudioKind } from "../model/notebook";

/** One preset group in a studio dialog, e.g. "Length: Short / Default / Long". */
export interface StudioOptionGroup {
	/** Request field the choice is sent as. */
	field: string;
	values: readonly string[];
	defaultValue: string;
	/**
	 * Large described cards, a segmented control, or a horizontally scrolling
	 * row of picture cards (for choices that are easier seen than read).
	 */
	presentation: "cards" | "segments" | "gallery";
	/** Card preview images by value, for choices that are easier seen than read. */
	previews?: Readonly<Record<string, string>>;
	/** The previews' shape in a gallery (16:9 unless said otherwise). */
	previewShape?: "wide" | "square";
}

/**
 * The backend's infographic presets (app/artifacts/infographic/presets.py)
 * plus Auto, in NotebookLM's order. Each preview is the same rocket drawn in
 * that style (generated once, public/infographic-styles/*.webp).
 */
export const INFOGRAPHIC_STYLES = [
	"auto",
	"kawaii",
	"clay",
	"sketch-note",
	"anime",
	"editorial",
	"instructional",
	"bento-grid",
	"bricks",
	"scientific",
	"professional",
] as const;

/** Canvas shapes the image model draws at (app/artifacts/infographic/layout.py). */
export const INFOGRAPHIC_ORIENTATIONS = ["landscape", "portrait", "square"] as const;
export const INFOGRAPHIC_DETAILS = ["concise", "standard", "detailed"] as const;

/**
 * Video looks the renderer can really draw (React/SVG, Inter/Lora/JetBrains
 * Mono only): see VIDEO_STYLES in app/notebooks/studio/briefs.py. "auto"
 * leaves the look to the author; "custom" asks for a short description.
 */
export const VIDEO_STYLES = [
	"auto",
	"custom",
	"classic",
	"whiteboard",
	"playful",
	"dark",
	"minimal",
] as const;
export const CUSTOM_STYLE_MAX_CHARS = 300;

export interface StudioTile {
	kind: StudioKind;
	icon: LucideIcon;
	groups: readonly StudioOptionGroup[];
	/** Offers a language choice (the generator can narrate or write in it). */
	language: boolean;
}

/**
 * Every output the backend can generate without a chat turn. The order is the
 * tile order in the Studio grid. The infographic style, which chat asks inside
 * the tool, is chosen here instead so the background run never has to ask.
 */
export const STUDIO_TILES: readonly StudioTile[] = [
	{
		kind: "audio",
		icon: AudioLines,
		language: true,
		groups: [
			{
				field: "format",
				values: ["deep_dive", "brief", "critique", "debate"],
				defaultValue: "deep_dive",
				presentation: "cards",
			},
			{
				field: "length",
				values: ["short", "default", "long"],
				defaultValue: "default",
				presentation: "segments",
			},
		],
	},
	{
		kind: "video",
		icon: Video,
		language: true,
		groups: [
			{
				field: "format",
				values: ["explainer", "short"],
				defaultValue: "explainer",
				presentation: "cards",
			},
			{
				field: "visual_style",
				values: VIDEO_STYLES,
				defaultValue: "auto",
				presentation: "gallery",
				previews: Object.fromEntries(
					VIDEO_STYLES.map((style) => [style, `/video-styles/${style}.svg`])
				),
			},
		],
	},
	{
		kind: "mindmap",
		icon: Network,
		language: true,
		groups: [
			{
				field: "detail",
				values: ["overview", "standard", "detailed"],
				defaultValue: "standard",
				presentation: "segments",
			},
		],
	},
	{
		kind: "report",
		icon: FileText,
		language: true,
		groups: [
			{
				field: "format",
				values: ["document", "interactive"],
				defaultValue: "document",
				presentation: "cards",
			},
			{
				field: "file_type",
				values: ["pdf", "docx"],
				defaultValue: "pdf",
				presentation: "segments",
			},
		],
	},
	{
		kind: "flashcards",
		icon: PlayingCardsFan,
		language: true,
		groups: [
			{
				field: "count",
				values: ["fewer", "standard", "more"],
				defaultValue: "standard",
				presentation: "segments",
			},
			{
				field: "difficulty",
				values: ["easy", "medium", "hard"],
				defaultValue: "medium",
				presentation: "segments",
			},
		],
	},
	{
		kind: "quiz",
		icon: ListChecks,
		language: true,
		groups: [
			{
				field: "count",
				values: ["fewer", "standard", "more"],
				defaultValue: "standard",
				presentation: "segments",
			},
			{
				field: "difficulty",
				values: ["easy", "medium", "hard"],
				defaultValue: "medium",
				presentation: "segments",
			},
		],
	},
	{
		kind: "infographic",
		icon: ImageIcon,
		language: true,
		groups: [
			{
				field: "orientation",
				values: INFOGRAPHIC_ORIENTATIONS,
				defaultValue: "landscape",
				presentation: "segments",
			},
			{
				field: "style",
				values: INFOGRAPHIC_STYLES,
				defaultValue: "auto",
				presentation: "gallery",
				previewShape: "square",
				previews: Object.fromEntries(
					INFOGRAPHIC_STYLES.map((style) => [style, `/infographic-styles/${style}.webp`])
				),
			},
			{
				field: "detail",
				values: INFOGRAPHIC_DETAILS,
				defaultValue: "standard",
				presentation: "segments",
			},
		],
	},
	{
		kind: "slides",
		icon: Presentation,
		language: true,
		groups: [
			{
				field: "format",
				values: ["detailed", "presenter"],
				defaultValue: "detailed",
				presentation: "cards",
			},
			{
				field: "length",
				values: ["short", "default", "long"],
				defaultValue: "default",
				presentation: "segments",
			},
		],
	},
	{
		kind: "data_table",
		icon: Sheet,
		language: true,
		groups: [],
	},
];

export function studioTile(kind: StudioKind): StudioTile {
	const tile = STUDIO_TILES.find((candidate) => candidate.kind === kind);
	if (!tile) throw new Error(`unknown studio kind: ${kind}`);
	return tile;
}

export function defaultOptions(kind: StudioKind): Record<string, string> {
	return Object.fromEntries(
		studioTile(kind).groups.map((group) => [group.field, group.defaultValue])
	);
}
