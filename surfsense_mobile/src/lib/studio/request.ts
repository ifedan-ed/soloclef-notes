/**
 * Studio dialog state and the generate request it becomes: a port of
 * surfsense_web/features/notebooks/lib/studio-request.ts and
 * suggestion-rotation.ts. Keep the request body identical to the web's.
 */
import type { StudioKind } from "../api/schemas";
import { CUSTOM_STYLE_MAX_CHARS, defaultOptions, studioTile } from "./catalog";

export interface StudioDialogState {
	kind: StudioKind;
	options: Record<string, string>;
	language: string;
	focus: string;
	/** Notebook source ids ticked for this output. */
	sourceIds: number[];
	stylePrompt?: string;
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

export function needsStyleDescription(state: StudioDialogState): boolean {
	return state.kind === "video" && state.options.visual_style === "custom" && !(state.stylePrompt ?? "").trim();
}

export function appendSuggestion(focus: string, suggestion: string): string {
	const current = focus.trim();
	if (!current) return suggestion;
	if (current.includes(suggestion)) return current;
	return `${current}${/[.!?]$/.test(current) ? "" : "."} ${suggestion}`;
}

/** English names: generators read the language inside a prompt ("Spanish" beats "es"). */
const LANGUAGE_NAMES: Record<string, string> = {
	en: "English",
	es: "Spanish",
	pt: "Portuguese",
	fr: "French",
	de: "German",
	it: "Italian",
	hi: "Hindi",
	ko: "Korean",
	zh: "Chinese",
	ja: "Japanese",
	ar: "Arabic",
	ru: "Russian",
};

export const STUDIO_LANGUAGES = Object.keys(LANGUAGE_NAMES);

/**
 * Audio takes the BCP-47 tag its voice catalogue is keyed by; every other
 * generator gets the English language name, as the web sends via
 * ``Intl.DisplayNames(["en"])`` (which Hermes lacks, hence the table).
 */
export function languageForRequest(kind: StudioKind, tag: string): string | null {
	const trimmed = tag.trim();
	if (!trimmed) return null;
	if (kind === "audio") return trimmed;
	const base = trimmed.toLowerCase().split(/[-_]/)[0];
	return LANGUAGE_NAMES[trimmed.toLowerCase()] ?? LANGUAGE_NAMES[base] ?? trimmed;
}

export function buildStudioRequest(
	state: StudioDialogState,
	{ enabledSourceIds, title, requestId }: { enabledSourceIds: number[]; title: string; requestId: string }
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
		body.style_prompt = (state.stylePrompt ?? "").trim().replace(/\s+/g, " ").slice(0, CUSTOM_STYLE_MAX_CHARS);
	}
	const focus = state.focus.trim();
	if (focus) body.focus = focus;
	const language = languageForRequest(state.kind, state.language);
	if (language) body.language = language;

	const chosen = [...new Set(state.sourceIds)].sort((a, b) => a - b);
	const enabled = [...new Set(enabledSourceIds)].sort((a, b) => a - b);
	const sameAsEnabled = chosen.length === enabled.length && chosen.every((id, index) => id === enabled[index]);
	if (!sameAsEnabled) body.source_ids = chosen;
	return body;
}

export function newRequestId(random: () => number = Math.random): string {
	const cryptoObj = (globalThis as { crypto?: { randomUUID?: () => string } }).crypto;
	if (cryptoObj?.randomUUID) return cryptoObj.randomUUID().replaceAll("-", "");
	let out = Date.now().toString(36);
	while (out.length < 32) out += Math.floor(random() * 36).toString(36);
	return out.slice(0, 32);
}

// --- Suggestion rotation (three ideas at a time from the backend's pool) ---

export const SUGGESTION_WINDOW = 3;

const VARIANT_FIELD: Partial<Record<StudioKind, string>> = {
	audio: "format",
	video: "format",
	report: "format",
	slides: "format",
	flashcards: "difficulty",
	quiz: "difficulty",
};

export function suggestionVariant(kind: StudioKind, options: Record<string, string>): string {
	const field = VARIANT_FIELD[kind];
	return field ? (options[field] ?? "") : "";
}

function modulo(value: number, size: number): number {
	return ((value % size) + size) % size;
}

export function suggestionWindow(
	pool: readonly string[],
	examples: readonly string[],
	offset: number
): { suggestions: string[]; placeholder: string | null } {
	const size = pool.length;
	const suggestions =
		size >= SUGGESTION_WINDOW
			? Array.from({ length: SUGGESTION_WINDOW }, (_, i) => pool[modulo(offset * SUGGESTION_WINDOW + i, size)])
			: [...pool];
	const placeholder = examples.length ? examples[modulo(offset, examples.length)] : null;
	return { suggestions, placeholder };
}

export function windowCount(poolSize: number): number {
	return Math.max(1, Math.ceil(poolSize / SUGGESTION_WINDOW));
}
