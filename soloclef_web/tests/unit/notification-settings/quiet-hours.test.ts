import assert from "node:assert/strict";
import { test } from "node:test";
import {
	firstQuietHours,
	QUIET_HOURS_PROPOSAL,
	quietHoursProblem,
	runsOvernight,
	timeZoneChoices,
} from "@/features/notification-settings/lib/quiet-hours";

/** Quiet hours are refused here for the same reasons the server refuses them (422). */

const zone = "Africa/Lagos";

test("times are HH:MM on a 24 hour clock, and start and end differ", () => {
	assert.equal(quietHoursProblem({ start: "22:00", end: "07:00", time_zone: zone }), null);
	assert.equal(quietHoursProblem({ start: "00:00", end: "23:59", time_zone: zone }), null);
	for (const bad of ["", "7:00", "24:00", "12:60", "22:00:00", "noon"]) {
		assert.equal(
			quietHoursProblem({ start: bad, end: "07:00", time_zone: zone }),
			"invalid_time",
			bad
		);
		assert.equal(
			quietHoursProblem({ start: "07:00", end: bad, time_zone: zone }),
			"invalid_time",
			bad
		);
	}
	assert.equal(quietHoursProblem({ start: "07:00", end: "07:00", time_zone: zone }), "same_time");
});

test("hours that end before they start run overnight", () => {
	assert.equal(runsOvernight({ start: "22:00", end: "07:00", time_zone: zone }), true);
	assert.equal(runsOvernight({ start: "13:00", end: "14:30", time_zone: zone }), false);
	assert.equal(runsOvernight({ start: "09:59", end: "10:00", time_zone: zone }), false);
});

test("switching on proposes a night, in the browser's zone, and that is what is saved", () => {
	const first = firstQuietHours("America/New_York");
	assert.deepEqual(first, { ...QUIET_HOURS_PROPOSAL, time_zone: "America/New_York" });
	assert.equal(quietHoursProblem(first), null);
});

test("the zone picker offers the browser's list plus the stored and browser zones, once each, sorted", () => {
	assert.deepEqual(timeZoneChoices(["Europe/London", "Africa/Lagos"], "UTC", "Africa/Lagos"), [
		"Africa/Lagos",
		"Europe/London",
		"UTC",
	]);
	// A browser that cannot list zones still offers the one it is in.
	assert.deepEqual(timeZoneChoices([], null, "Asia/Seoul"), ["Asia/Seoul"]);
});
