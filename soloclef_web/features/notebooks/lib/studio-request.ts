import type { StudioKind } from "../model/notebook";
import { CUSTOM_STYLE_MAX_CHARS, defaultOptions, studioTile } from "./studio-catalog";

/** What a studio dialog holds while the user is choosing. */
export interface StudioDialogState {
	kind: StudioKind;
	options: Record<string, string>;
	language: string;
	focus: string;
	/** Document ids ticked in the dialog's source picker. */
	sourceIds: number[];
	/** Video only: the user's own look, used when the style is "custom". */
	stylePrompt?: string;
	/** Report only: the template chosen and its (possibly edited) prompt. */
	templateKey?: string | null;
	templatePrompt?: string | null;
}

export type StudioRequestBody = Record<string, unknown> & { kind: StudioKind };

export function initialDialogState(kind: StudioKind, sourceIds: number[]): StudioDialogState {
	return {
		kind,
		options: defaultOptions(kind),
		language: "",
		focus: "",
		sourceIds: [...sourceIds],
		templateKey: null,
		templatePrompt: null,
		stylePrompt: "",
	};
}

/** A custom video style with no description cannot be generated. */
export function needsStyleDescription(state: StudioDialogState): boolean {
	return (
		state.kind === "video" &&
		state.options.visual_style === "custom" &&
		!(state.stylePrompt ?? "").trim()
	);
}

/** Append a suggestion to the focus text without doubling it up. */
export function appendSuggestion(focus: string, suggestion: string): string {
	const current = focus.trim();
	if (!current) return suggestion;
	if (current.includes(suggestion)) return current;
	return `${current}${/[.!?]$/.test(current) ? "" : "."} ${suggestion}`;
}

/**
 * The generate request for a dialog. Only presets the backend knows for this
 * kind are sent; the file type only matters for document reports; the
 * source list is sent only when it differs from "every enabled source", so
 * the default stays "whatever the notebook has enabled".
 */
export function buildStudioRequest(
	state: StudioDialogState,
	{
		enabledSourceIds,
		title,
		requestId,
	}: { enabledSourceIds: number[]; title: string; requestId: string }
): StudioRequestBody {
	const body: StudioRequestBody = { kind: state.kind, title, request_id: requestId };
	for (const group of studioTile(state.kind).groups) {
		const value = state.options[group.field];
		body[group.field] = group.values.includes(value) ? value : group.defaultValue;
	}
	if (state.kind === "report") {
		if (body.format !== "document") delete body.file_type;
		if (state.templateKey) body.template_key = state.templateKey;
		const prompt = state.templatePrompt?.trim();
		if (prompt) body.template_prompt = prompt;
	}
	if (state.kind === "video" && body.visual_style === "custom") {
		body.style_prompt = (state.stylePrompt ?? "")
			.trim()
			.replace(/\s+/g, " ")
			.slice(0, CUSTOM_STYLE_MAX_CHARS);
	}
	const focus = state.focus.trim();
	if (focus) body.focus = focus;
	const language = languageForRequest(state.kind, state.language);
	if (language) body.language = language;

	const chosen = [...new Set(state.sourceIds)].sort((a, b) => a - b);
	const enabled = [...new Set(enabledSourceIds)].sort((a, b) => a - b);
	const sameAsEnabled =
		chosen.length === enabled.length && chosen.every((id, index) => id === enabled[index]);
	if (!sameAsEnabled) body.source_ids = chosen;
	return body;
}

export function newRequestId(): string {
	if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
		return crypto.randomUUID().replaceAll("-", "");
	}
	return `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 10)}`;
}

/** Languages offered for written and narrated outputs (audio uses its provider's list). */
export const STUDIO_LANGUAGES = [
	"en",
	"es",
	"pt",
	"fr",
	"de",
	"it",
	"hi",
	"ko",
	"zh",
	"ja",
	"ar",
	"ru",
];

/**
 * Audio takes the BCP-47 tag its voice catalogue is keyed by; every other
 * generator reads the language inside a prompt, where "Spanish" is clearer
 * than "es". An empty choice means "the generator's default".
 */
export function languageForRequest(kind: StudioKind, tag: string): string | null {
	const trimmed = tag.trim();
	if (!trimmed) return null;
	if (kind === "audio") return trimmed;
	try {
		return new Intl.DisplayNames(["en"], { type: "language" }).of(trimmed) ?? trimmed;
	} catch {
		return trimmed;
	}
}
