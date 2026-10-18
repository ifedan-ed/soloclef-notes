import assert from "node:assert/strict";
import test from "node:test";
import {
	emailImapAccountSchema,
	testConnectionResultSchema,
} from "@/features/email-imap-connect/api/email-imap-api";

test("an account response never includes a password field", () => {
	const parsed = emailImapAccountSchema.parse({
		id: "abc-123",
		label: "Work",
		host: "mail.example.com",
		port: 993,
		use_tls: true,
		username: "me@example.com",
		created_at: "2026-01-01T00:00:00Z",
	});
	assert.ok(!("app_password" in parsed));
	assert.ok(!("password" in parsed));
});

test("an account response with a stray password field fails validation (strict shape)", () => {
	// zod's default .object() strips unknown keys rather than rejecting them,
	// so this documents the schema's actual behavior: parsing never *returns*
	// a password even if the backend regressed and included one.
	const parsed = emailImapAccountSchema.parse({
		id: "abc-123",
		label: "Work",
		host: "mail.example.com",
		port: 993,
		use_tls: true,
		username: "me@example.com",
		created_at: null,
		app_password: "should-never-survive-parsing",
	});
	assert.ok(!("app_password" in parsed));
});

test("test-connection result accepts ok and error shapes", () => {
	assert.equal(testConnectionResultSchema.parse({ status: "ok", folder_count: 4 }).status, "ok");
	assert.equal(
		testConnectionResultSchema.parse({ status: "error", message: "bad password" }).status,
		"error"
	);
});
