import { z } from "zod";

/**
 * The account's push choices (GET/PUT /api/v1/push/preferences). The server
 * owns the list of kinds and their words: the page draws one switch per entry
 * in `kinds`, in the order given, and carries no list of its own, so a kind
 * the server adds or retires needs no web change.
 */
export const pushKindInfo = z.object({
	kind: z.string(),
	label: z.string(),
	description: z.string(),
});

/** No push between `start` and `end` ("HH:MM", 24 h) in `time_zone`; wraps midnight when end < start. */
export const quietHours = z.object({
	start: z.string(),
	end: z.string(),
	time_zone: z.string(),
});

export const pushPreferences = z.object({
	choices: z.record(z.string(), z.boolean()),
	kinds: z.array(pushKindInfo),
	quiet_hours: quietHours.nullable(),
});

/**
 * A change: at least one key. A kind left out of `choices` keeps its value;
 * `quiet_hours: null` switches quiet hours off.
 */
export const pushPreferencesChange = z
	.object({
		choices: z.record(z.string(), z.boolean()).optional(),
		quiet_hours: quietHours.nullable().optional(),
	})
	.refine((change) => change.choices !== undefined || change.quiet_hours !== undefined, {
		message: "A change names choices, quiet hours or both",
	});

export type PushKindInfo = z.infer<typeof pushKindInfo>;
export type QuietHours = z.infer<typeof quietHours>;
export type PushPreferences = z.infer<typeof pushPreferences>;
export type PushPreferencesChange = z.infer<typeof pushPreferencesChange>;
