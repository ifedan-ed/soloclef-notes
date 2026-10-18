/**
 * The Studio tiles and their option groups, identical to the web's
 * surfsense_web/features/notebooks/lib/studio-catalog.ts (the backend
 * validates these values). Presentation hints: "cards" are described choices,
 * "segments" are one-line pills (the default is named beside the label), and
 * "gallery" is the picture-card row, drawn as cards on the phone.
 */
import type { StudioKind } from "../api/schemas";

export interface StudioOptionGroup {
	field: string;
	values: readonly string[];
	defaultValue: string;
	presentation: "cards" | "segments" | "gallery";
}

export interface StudioTile {
	kind: StudioKind;
	icon: string;
	groups: readonly StudioOptionGroup[];
	language: boolean;
}

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
export const INFOGRAPHIC_ORIENTATIONS = ["landscape", "portrait", "square"] as const;
export const INFOGRAPHIC_DETAILS = ["concise", "standard", "detailed"] as const;
export const VIDEO_STYLES = ["auto", "custom", "classic", "whiteboard", "playful", "dark", "minimal"] as const;
export const CUSTOM_STYLE_MAX_CHARS = 300;

export const STUDIO_TILES: readonly StudioTile[] = [
	{
		kind: "audio",
		icon: "headset",
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
		icon: "videocam",
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
			},
		],
	},
	{
		kind: "mindmap",
		icon: "git-network",
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
		icon: "document-text",
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
		icon: "albums",
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
		icon: "list",
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
		icon: "image",
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
		icon: "easel",
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
		icon: "grid",
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
	return Object.fromEntries(studioTile(kind).groups.map((group) => [group.field, group.defaultValue]));
}

/** "8:07" for 487 seconds; hours when needed. */
export function formatDuration(totalSeconds: number): string {
	const seconds = Math.max(0, Math.round(totalSeconds));
	const hours = Math.floor(seconds / 3600);
	const minutes = Math.floor((seconds % 3600) / 60);
	const rest = String(seconds % 60).padStart(2, "0");
	return hours > 0 ? `${hours}:${String(minutes).padStart(2, "0")}:${rest}` : `${minutes}:${rest}`;
}

export function isInFlight(status: string): boolean {
	return status === "queued" || status === "running";
}

/** Poll every 4 s while anything generates, not at all otherwise (as the web does). */
export function outputsRefetchInterval(statuses: readonly string[]): number | false {
	return statuses.some(isInFlight) ? 4000 : false;
}

/** Phases the web has words for; anything else shows the plain status. */
export const KNOWN_PHASES = [
	"preparing",
	"drafting",
	"authoring",
	"narrating",
	"reviewing",
	"repairing",
	"rendering",
	"verifying",
	"saving",
] as const;

export function knownPhase(phase: string | null | undefined): string | null {
	return phase && (KNOWN_PHASES as readonly string[]).includes(phase) ? phase : null;
}
