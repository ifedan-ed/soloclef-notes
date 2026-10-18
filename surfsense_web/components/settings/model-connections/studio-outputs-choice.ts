import { z } from "zod";

/**
 * The Studio outputs model setting as the backend stores it, and the pure
 * edits the settings card makes to it. The card sends the whole setting on
 * every change, so each edit returns a new setting rather than a patch.
 */
export const studioChoiceSchema = z.object({
	mode: z.enum(["recommended", "auto", "model"]),
	primary: z.number().nullish(),
	fallback: z.number().nullish(),
});
export type StudioChoice = z.infer<typeof studioChoiceSchema>;

const recommendedModelSchema = z.object({ name: z.string(), id: z.number().nullable() });

export const studioModelSchema = z.object({
	default: studioChoiceSchema,
	overrides: z.record(z.string(), studioChoiceSchema),
	customized: z.boolean(),
	recommended_primary: recommendedModelSchema,
	recommended_fallback: recommendedModelSchema.nullable(),
	unavailable: z.array(z.string()),
	slots: z.array(z.string()),
});
export type StudioModelRead = z.infer<typeof studioModelSchema>;

export type StudioModelSettings = {
	default: StudioChoice;
	overrides: Record<string, StudioChoice>;
};

export const RECOMMENDED = "recommended";
export const AUTO = "auto";
export const USE_DEFAULT = "default";
export const NO_FALLBACK = "none";

/** One click back to today's behaviour: every output type on the chat model. */
export const ALL_AUTO: StudioModelSettings = { default: { mode: "auto" }, overrides: {} };

/** The primary select's value; ``slot`` null is the workspace default. */
export function primaryValue(
	settings: StudioModelSettings,
	slot: string | null,
	modelIds: number[]
): string {
	const choice = slot === null ? settings.default : settings.overrides[slot];
	if (!choice) return USE_DEFAULT;
	if (choice.mode === "recommended") return RECOMMENDED;
	if (choice.mode === "auto") return AUTO;
	if (choice.primary && modelIds.includes(choice.primary)) return String(choice.primary);
	// A chosen model that is gone runs on the next level down; show that level.
	return slot === null ? AUTO : USE_DEFAULT;
}

export function fallbackValue(
	settings: StudioModelSettings,
	slot: string | null,
	modelIds: number[]
): string {
	const choice = slot === null ? settings.default : settings.overrides[slot];
	const fallback = choice?.mode === "model" ? choice.fallback : null;
	return fallback && modelIds.includes(fallback) ? String(fallback) : NO_FALLBACK;
}

function withChoice(
	settings: StudioModelSettings,
	slot: string | null,
	choice: StudioChoice | null
): StudioModelSettings {
	if (slot === null) return { ...settings, default: choice ?? { mode: "recommended" } };
	const overrides = { ...settings.overrides };
	if (choice) overrides[slot] = choice;
	else delete overrides[slot];
	return { ...settings, overrides };
}

export function choosePrimary(
	settings: StudioModelSettings,
	slot: string | null,
	value: string
): StudioModelSettings {
	if (value === USE_DEFAULT) return withChoice(settings, slot, null);
	if (value === RECOMMENDED || value === AUTO) {
		return withChoice(settings, slot, { mode: value });
	}
	const primary = Number(value);
	const current = slot === null ? settings.default : settings.overrides[slot];
	const kept = current?.mode === "model" ? current.fallback : null;
	return withChoice(settings, slot, {
		mode: "model",
		primary,
		fallback: kept && kept !== primary ? kept : null,
	});
}

export function chooseFallback(
	settings: StudioModelSettings,
	slot: string | null,
	value: string
): StudioModelSettings {
	const current = slot === null ? settings.default : settings.overrides[slot];
	if (current?.mode !== "model") return settings;
	const fallback = value === NO_FALLBACK ? null : Number(value);
	return withChoice(settings, slot, {
		...current,
		fallback: fallback === current.primary ? null : fallback,
	});
}

export function settingsOf(read: StudioModelRead): StudioModelSettings {
	return { default: read.default, overrides: read.overrides };
}
