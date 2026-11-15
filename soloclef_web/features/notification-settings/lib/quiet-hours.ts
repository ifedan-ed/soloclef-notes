/**
 * Quiet hours as the page edits them: what it proposes when the person first
 * switches them on, what it refuses before asking the server (the server
 * refuses the same with a 422), and the time zones it offers.
 */

import type { QuietHours } from "@/contracts/types/push-preferences.types";

/**
 * The times shown when quiet hours are first switched on: a night. They are
 * on screen, in the fields, from that moment and saved as shown, so the
 * person sees exactly what was set and changes it there.
 */
export const QUIET_HOURS_PROPOSAL = { start: "22:00", end: "07:00" } as const;

/** "HH:MM", 24 h, as the server takes it (and as `<input type="time">` gives it). */
const CLOCK_TIME = /^([01]\d|2[0-3]):[0-5]\d$/;

export type QuietHoursProblem = "invalid_time" | "same_time";

/** Why the server would refuse these quiet hours, or null when it would take them. */
export function quietHoursProblem(hours: QuietHours): QuietHoursProblem | null {
	if (!CLOCK_TIME.test(hours.start) || !CLOCK_TIME.test(hours.end)) return "invalid_time";
	if (hours.start === hours.end) return "same_time";
	return null;
}

/** Quiet hours that end the next morning (22:00 to 07:00): the server wraps them past midnight. */
export function runsOvernight(hours: QuietHours): boolean {
	return hours.end < hours.start;
}

/**
 * What switching quiet hours on saves: the proposed night, in the zone the
 * person's browser is set to. The zone is then stored with them and shown,
 * so the choice is the person's, made explicit, not a default applied later.
 */
export function firstQuietHours(browserTimeZone: string): QuietHours {
	return { ...QUIET_HOURS_PROPOSAL, time_zone: browserTimeZone };
}

/**
 * The zones the picker offers: the browser's list of IANA names, plus the
 * stored zone and the browser's own (either can be an alias, such as "UTC",
 * that the browser's list leaves out), each once, sorted.
 */
export function timeZoneChoices(
	supported: readonly string[],
	stored: string | null,
	browser: string
): string[] {
	const zones = new Set(supported);
	zones.add(browser);
	if (stored) zones.add(stored);
	return [...zones].sort((a, b) => a.localeCompare(b));
}
