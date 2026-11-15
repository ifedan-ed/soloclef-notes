import assert from "node:assert/strict";
import { test } from "node:test";
import type { PushPreferences } from "@/contracts/types/push-preferences.types";
import {
	isKindOn,
	withChange,
	withoutChange,
} from "@/features/notification-settings/lib/preference-change";

/**
 * Settings → Notifications saves each switch at once and shows it before the
 * server answers; a refusal puts back only what that save touched.
 */

const KINDS = [
	{ kind: "output", label: "Studio outputs", description: "" },
	{ kind: "source", label: "Sources", description: "" },
];

const stored: PushPreferences = {
	choices: { output: true, source: true },
	kinds: KINDS,
	quiet_hours: null,
};

test("a change shows at once: the named kinds and quiet hours, nothing else", () => {
	assert.deepEqual(withChange(stored, { choices: { output: false } }), {
		...stored,
		choices: { output: false, source: true },
	});
	const night = { start: "22:00", end: "07:00", time_zone: "Africa/Lagos" };
	assert.deepEqual(withChange(stored, { quiet_hours: night }).quiet_hours, night);
	assert.deepEqual(withChange(stored, { quiet_hours: night }).choices, stored.choices);
	const quiet = { ...stored, quiet_hours: night };
	assert.equal(withChange(quiet, { quiet_hours: null }).quiet_hours, null);
	assert.deepEqual(withChange(quiet, { choices: { source: false } }).quiet_hours, night);
});

test("a refused save puts back only its own switch, not one flipped since", () => {
	const first = { choices: { output: false } };
	const afterFirst = withChange(stored, first);
	const second = { choices: { source: false } };
	const afterBoth = withChange(afterFirst, second);

	// The first save fails while the second is still shown: output comes back on, source stays off.
	assert.deepEqual(withoutChange(afterBoth, first, stored).choices, {
		output: true,
		source: false,
	});
});

test("a refused quiet-hours save puts back the hours it replaced", () => {
	const night = { start: "22:00", end: "07:00", time_zone: "Africa/Lagos" };
	const change = { quiet_hours: { ...night, start: "23:30" } };
	const before = { ...stored, quiet_hours: night };
	const shown = withChange(before, change);
	assert.deepEqual(withoutChange(shown, change, before).quiet_hours, night);
	// Switching on, refused: off again.
	const on = { quiet_hours: night };
	assert.equal(withoutChange(withChange(stored, on), on, stored).quiet_hours, null);
});

test("a kind the stored choices never had leaves them as it found them", () => {
	const change = { choices: { mail: false } };
	const back = withoutChange(withChange(stored, change), change, stored);
	assert.deepEqual(back.choices, stored.choices);
});

test("a kind is on unless turned off, as the server sends every kind until then", () => {
	assert.equal(isKindOn(stored, "output"), true);
	assert.equal(isKindOn({ ...stored, choices: { output: false } }, "output"), false);
	assert.equal(isKindOn(stored, "automation"), true);
});
