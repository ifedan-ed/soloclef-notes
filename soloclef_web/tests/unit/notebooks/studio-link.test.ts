import assert from "node:assert/strict";
import { test } from "node:test";
import {
	readStudioLink,
	retryDecision,
	studioHref,
	withoutStudioLinkParams,
} from "@/features/notebooks/lib/studio-link";

/**
 * The inbox's links into a notebook's Studio: open the pane, and for a failed
 * output's "Try again", run it once, then drop the parameters so a reload
 * does not run it again.
 */

test("a Studio link opens the pane, and may name an output to run again", () => {
	assert.equal(studioHref(11), "/dashboard/notebooks/11?pane=studio");
	assert.equal(studioHref(11, 42), "/dashboard/notebooks/11?pane=studio&retry_output=42");
	assert.deepEqual(readStudioLink("?pane=studio"), { retryOutputId: null });
	assert.deepEqual(readStudioLink("?pane=studio&retry_output=42"), { retryOutputId: 42 });
	assert.deepEqual(readStudioLink("?retry_output=42"), { retryOutputId: 42 });
	assert.equal(readStudioLink(""), null);
	assert.equal(readStudioLink("?pane=chat"), null);
	assert.deepEqual(readStudioLink("?pane=studio&retry_output=-1"), { retryOutputId: null });
	assert.deepEqual(readStudioLink("?pane=studio&retry_output=x"), { retryOutputId: null });
});

test("once acted on, the link's parameters leave the address and every other one stays", () => {
	assert.equal(
		withoutStudioLinkParams(
			"https://notes.example/dashboard/notebooks/11?pane=studio&retry_output=42"
		),
		"https://notes.example/dashboard/notebooks/11"
	);
	assert.equal(
		withoutStudioLinkParams("https://notes.example/dashboard/notebooks/11?note=3&pane=studio"),
		"https://notes.example/dashboard/notebooks/11?note=3"
	);
	assert.equal(
		readStudioLink(
			new URL(
				withoutStudioLinkParams(
					"https://notes.example/dashboard/notebooks/11?pane=studio&retry_output=42"
				)
			).search
		),
		null
	);
});

test("the output is run again only once the outputs are in and only while it is failed or cancelled", () => {
	assert.equal(retryDecision(undefined, 42), "wait");
	assert.equal(retryDecision([{ id: 42, status: "failed" }], 42), "retry");
	assert.equal(retryDecision([{ id: 42, status: "cancelled" }], 42), "retry");
	for (const status of ["queued", "running", "ready"] as const) {
		assert.equal(retryDecision([{ id: 42, status }], 42), "gone", status);
	}
	assert.equal(retryDecision([{ id: 7, status: "failed" }], 42), "gone");
	assert.equal(retryDecision([], 42), "gone");
});
