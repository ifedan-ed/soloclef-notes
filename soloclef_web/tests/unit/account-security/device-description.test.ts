import assert from "node:assert/strict";
import test from "node:test";
import {
	deviceName,
	orderDevices,
	showsEnd,
} from "@/features/account-security/lib/device-description";
import type { Device } from "@/features/account-security/model/device";

// Run with: pnpm exec tsx --test tests/unit/account-security/device-description.test.ts

const device = (over: Partial<Device>): Device => ({
	session_id: "00000000-0000-4000-8000-000000000000",
	kind: "web",
	device_label: null,
	signed_in_at: "2026-11-01T10:00:00Z",
	last_used_at: "2026-11-10T10:00:00Z",
	expires_at: "2026-11-24T10:00:00Z",
	current: false,
	...over,
});

test("a phone is called by its own name; anything unnamed by its kind", () => {
	assert.deepEqual(deviceName(device({ kind: "app", device_label: "Ada's iPhone" })), {
		label: "Ada's iPhone",
		unnamed: "unnamed_phone",
	});
	assert.equal(deviceName(device({ kind: "app", device_label: "  " })).label, null);
	assert.deepEqual(deviceName(device({})), { label: null, unnamed: "unnamed_browser" });
});

test("only a phone's row says when it ends", () => {
	assert.equal(showsEnd(device({ kind: "app" })), true);
	assert.equal(showsEnd(device({ kind: "web" })), false);
});

test("this browser comes first, then the rest by latest use", () => {
	const older = device({ session_id: "a", last_used_at: "2026-11-01T00:00:00Z" });
	const newer = device({ session_id: "b", last_used_at: "2026-11-12T00:00:00Z" });
	const mine = device({ session_id: "c", last_used_at: "2026-10-01T00:00:00Z", current: true });
	const list = [older, mine, newer];
	assert.deepEqual(
		orderDevices(list).map((row) => row.session_id),
		["c", "b", "a"]
	);
	// The list given is not reordered in place: it is the query's cached data.
	assert.deepEqual(
		list.map((row) => row.session_id),
		["a", "c", "b"]
	);
});
