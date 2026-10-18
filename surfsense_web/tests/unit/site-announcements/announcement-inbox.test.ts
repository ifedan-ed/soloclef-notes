import assert from "node:assert/strict";
import { test } from "node:test";
import {
	announcementTarget,
	isExpiredAnnouncement,
} from "../../../features/site-announcements/lib/announcement-inbox";

const NOW = new Date("2026-09-26T12:00:00Z");
const announcement = (metadata: Record<string, unknown>) => ({
	type: "site_announcement",
	metadata: { announcement_id: 1, ...metadata },
});

test("an announcement hides once its expiry passes; other notifications never expire", () => {
	assert.equal(isExpiredAnnouncement(announcement({ expires_at: null }), NOW), false);
	assert.equal(
		isExpiredAnnouncement(announcement({ expires_at: "2026-09-27T00:00:00+00:00" }), NOW),
		false
	);
	assert.equal(
		isExpiredAnnouncement(announcement({ expires_at: "2026-09-26T11:59:00Z" }), NOW),
		true
	);
	assert.equal(
		isExpiredAnnouncement(
			{ type: "document_processing", metadata: { expires_at: "2000-01-01T00:00:00Z" } },
			NOW
		),
		false
	);
});

test("opening an announcement goes to its page in the app or its web address", () => {
	assert.deepEqual(announcementTarget(announcement({ link: "/docs/notifications" })), {
		kind: "app",
		href: "/docs/notifications",
	});
	assert.deepEqual(announcementTarget(announcement({ link: "https://example.org" })), {
		kind: "web",
		href: "https://example.org",
	});
	assert.equal(announcementTarget(announcement({ link: "javascript:alert(1)" })), null);
	assert.equal(announcementTarget(announcement({})), null);
	assert.equal(announcementTarget({ type: "new_mention", metadata: { link: "/x" } }), null);
});
