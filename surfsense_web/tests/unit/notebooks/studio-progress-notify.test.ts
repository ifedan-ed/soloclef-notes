import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { answerTextOf } from "@/features/notebooks/lib/answer-text";
import { GUIDED_STARTS } from "@/features/notebooks/lib/guided-starts";
import { KNOWN_PHASES, knownPhase } from "@/features/notebooks/lib/output-phase";
import {
	justFinished,
	parseNotifyPreference,
	parseStarted,
	rememberStarted,
	shouldOfferNotifications,
} from "@/features/notebooks/lib/studio-notify";
import type { StudioOutput } from "@/features/notebooks/model/notebook";

const LOCALES = ["en", "es", "pt", "hi", "ko", "zh"];

function output(id: number, status: StudioOutput["status"]): StudioOutput {
	return {
		id,
		kind: "quiz",
		title: `Quiz ${id}`,
		status,
		source_count: 1,
		options: {},
		created_at: "2026-09-23T10:00:00Z",
	};
}

test("only outputs this browser started, going from in flight to done, notify", () => {
	const before = new Map([
		[1, "running"],
		[2, "running"],
		[3, "ready"],
		[4, "queued"],
	]);
	const now = [output(1, "ready"), output(2, "ready"), output(3, "ready"), output(4, "failed")];
	assert.deepEqual(
		justFinished(before, now, [1, 3, 4]).map((o) => o.id),
		[1, 4]
	);
	assert.deepEqual(justFinished(new Map(), now, [1]), []);
});

test("the started list is bounded, deduplicated and tolerant of junk", () => {
	let started: number[] = [];
	for (let id = 0; id < 60; id++) started = rememberStarted(started, id);
	started = rememberStarted(started, 30);
	assert.equal(started.length, 50);
	assert.equal(started.at(-1), 30);
	assert.equal(started.filter((id) => id === 30).length, 1);
	assert.deepEqual(parseStarted("not json"), []);
	assert.deepEqual(parseStarted('[1,"x",2.5,3]'), [1, 3]);
});

test("the banner shows only until the user decides, and never when blocked", () => {
	assert.equal(shouldOfferNotifications(true, "default", null), true);
	assert.equal(shouldOfferNotifications(true, "default", "off"), false);
	assert.equal(shouldOfferNotifications(true, "denied", null), false);
	assert.equal(shouldOfferNotifications(false, null, null), false);
	assert.equal(parseNotifyPreference("on"), "on");
	assert.equal(parseNotifyPreference("maybe"), null);
});

test("backend phases map to labels, unknown ones fall back", () => {
	assert.equal(knownPhase("drafting"), "drafting");
	assert.equal(knownPhase("generating"), null);
	assert.equal(knownPhase(null), null);
});

test("every phase, guided start and new notebook string exists in all six locales", () => {
	for (const locale of LOCALES) {
		const messages = JSON.parse(
			readFileSync(join(process.cwd(), "messages", `${locale}.json`), "utf8")
		).notebooks;
		for (const phase of KNOWN_PHASES) assert.ok(messages.phases[phase], `${locale} ${phase}`);
		for (const start of GUIDED_STARTS) {
			assert.ok(messages.welcome.starts[start].label, `${locale} ${start}`);
			assert.ok(messages.welcome.starts[start].prompt, `${locale} ${start}`);
		}
		assert.ok(messages.notify.turn_on && messages.delete_history.body && messages.studio_empty);
	}
});

test("answer text joins only the text parts", () => {
	assert.equal(
		answerTextOf([
			{ type: "text", text: "One" },
			{ type: "tool-call", toolName: "x" },
			{ type: "text", text: "Two" },
		]),
		"One\n\nTwo"
	);
	assert.equal(answerTextOf(undefined), "");
});
