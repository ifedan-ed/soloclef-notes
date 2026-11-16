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
 * The looks the server's poster renderer draws an infographic in
 * (soloclef_studio/posterrender), plus Auto, which lets the server choose
 * from the material. Each preview is a real poster drawn in that look
 * (public/infographic-styles/*.webp). The server refuses any other name, so
 * there is no alias here for a look that has been removed.
 */
export const INFOGRAPHIC_STYLES = [
	"auto",
	"sketch-note",
	"editorial",
	"instructional",
	"bento-grid",
	"scientific",
	"professional",
] as const;

export type InfographicStyle = (typeof INFOGRAPHIC_STYLES)[number];

/**
 * Whether a saved output's style is one of today's. An infographic made in a
 * look that has since been removed keeps its picture; only its style name has
 * nothing left to be shown as.
 */
export function isInfographicStyle(value: unknown): value is InfographicStyle {
	return (INFOGRAPHIC_STYLES as readonly unknown[]).includes(value);
}

/** The poster's shapes: wide, tall, or square. */
export const INFOGRAPHIC_ORIENTATIONS = ["landscape", "portrait", "square"] as const;
export const INFOGRAPHIC_DETAILS = ["concise", "standard", "detailed"] as const;

/**
 * Video looks the renderer can really draw (React/SVG, Inter/Lora/JetBrains
 * Mono only). The server accepts the same list (visual_style in
 * soloclef_api/internal/studiogenerate/request.go) and describes each look to
 * the author in briefs.go (videoStyles). "auto" leaves the look to the
 * author; "custom" asks for a short description.
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
