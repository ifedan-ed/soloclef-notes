import assert from "node:assert/strict";
import { test } from "node:test";
import {
	draftProblems,
	draftToBody,
	emptyDraft,
} from "../../../features/site-announcements/lib/announcement-draft";

const NOW = new Date("2026-09-26T12:00:00Z");
const good = { ...emptyDraft, title: " Podcasts ", body: " Up to **30** minutes. " };

test("a title and a message are enough to send to everyone", () => {
	assert.deepEqual(draftProblems(good, NOW), []);
	assert.deepEqual(draftToBody(good), {
		title: "Podcasts",
		body: "Up to **30** minutes.",
		link: null,
		expires_at: null,
	});
});

test("the empty form names what is missing", () => {
	assert.deepEqual(draftProblems(emptyDraft, NOW), [
		"Give the announcement a title.",
		"Write the announcement.",
	]);
});

test("a link must be a page or a web address, and the expiry must be ahead", () => {
	assert.equal(draftProblems({ ...good, link: "javascript:x" }, NOW).length, 1);
	assert.equal(draftProblems({ ...good, link: "/docs" }, NOW).length, 0);
	assert.equal(draftProblems({ ...good, expiresAt: "2026-09-25T09:00" }, NOW).length, 1);
	assert.equal(draftProblems({ ...good, expiresAt: "2026-10-25T09:00" }, NOW).length, 0);
});

test("the expiry is sent as an instant", () => {
	const body = draftToBody({ ...good, expiresAt: "2026-10-01T09:00" });
	assert.ok(body.expires_at?.endsWith("Z"));
});
