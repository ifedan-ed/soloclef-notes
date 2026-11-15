/**
 * A change to the push choices, shown before the server answers and taken
 * back if it refuses. Taking back puts back only what that change touched:
 * two switches flipped quickly are two saves in flight, and the first one
 * failing must not undo the second.
 */

import type {
	PushPreferences,
	PushPreferencesChange,
} from "@/contracts/types/push-preferences.types";

/** The preferences as they will be once the server takes `change`. */
export function withChange(
	preferences: PushPreferences,
	change: PushPreferencesChange
): PushPreferences {
	return {
		...preferences,
		choices: change.choices ? { ...preferences.choices, ...change.choices } : preferences.choices,
		quiet_hours: change.quiet_hours !== undefined ? change.quiet_hours : preferences.quiet_hours,
	};
}

/**
 * `current` with the keys `change` named set back to their values in
 * `before` (the preferences when the change was made); every other key keeps
 * its current value, whatever else changed meanwhile.
 */
export function withoutChange(
	current: PushPreferences,
	change: PushPreferencesChange,
	before: PushPreferences
): PushPreferences {
	const choices = { ...current.choices };
	for (const kind of Object.keys(change.choices ?? {})) {
		if (kind in before.choices) choices[kind] = before.choices[kind];
		else delete choices[kind];
	}
	return {
		...current,
		choices,
		quiet_hours: change.quiet_hours !== undefined ? before.quiet_hours : current.quiet_hours,
	};
}

/**
 * Whether a kind is on. The server answers every kind it lists in `choices`;
 * one missing there is on, as the server sends every kind until it is
 * turned off (the phone app reads it the same way).
 */
export function isKindOn(preferences: PushPreferences, kind: string): boolean {
	return preferences.choices[kind] !== false;
}
