import assert from "node:assert/strict";
import test from "node:test";
import {
	jmapAccountSchema,
	jmapRemovalSchema,
	jmapTestResultSchema,
} from "@/features/jmap-mail-connect/api/jmap-mail-api";
import {
	JMAP_MAIL_PARAM,
	jmapMailOutcome,
	normalizedServerUrl,
	returnPathWithoutJmapOutcome,
	serverHost,
} from "@/features/jmap-mail-connect/lib/jmap-mail-outcome";

// Run with: pnpm exec tsx --test tests/unit/jmap-mail-connect/jmap-mail.test.ts

const account = {
	id: "6f1c2a0e-3b7d-4c55-9a1e-2f8b9c0d1e2f",
	label: "Home mail",
	server_url: "https://mail.example.org",
	username: "ada@example.org",
	auth: "oauth",
	can_send: true,
	needs_reconnect: false,
	created_at: "2026-11-12T10:00:00Z",
};

test("an account never carries a secret into page state, whatever the server sent", () => {
	const parsed = jmapAccountSchema.parse({
		...account,
		app_password: "app_should-never-survive",
		refresh_token: "should-never-survive",
		credentials: "v1:sealed",
	});
	assert.deepEqual(Object.keys(parsed).sort(), Object.keys(account).sort());
});

test("an account of a kind this page does not know is refused, not guessed", () => {
	// No default: an unknown sign-in kind would otherwise be drawn as one of
	// the two, with the wrong advice on removal.
	assert.equal(jmapAccountSchema.safeParse({ ...account, auth: "kerberos" }).success, false);
	assert.equal(jmapAccountSchema.safeParse({ ...account, can_send: undefined }).success, false);
});

test("a test result and a removal read as the server words them", () => {
	assert.deepEqual(
		jmapTestResultSchema.parse({ status: "ok", mailbox_count: 6, can_send: true, message: null }),
		{ status: "ok", mailbox_count: 6, can_send: true, message: null }
	);
	assert.equal(
		jmapTestResultSchema.parse({
			status: "error",
			mailbox_count: null,
			can_send: null,
			message: "Stalwart refused the app password",
		}).status,
		"error"
	);
	assert.equal(jmapRemovalSchema.parse({ revoked: false }).revoked, false);
});

test("each way back from Stalwart has one message, cancelling none, and an unknown one is a failure", () => {
	assert.deepEqual(jmapMailOutcome("connected"), {
		tone: "success",
		messageKey: "oauth_connected",
	});
	assert.equal(jmapMailOutcome("denied"), null);
	assert.equal(jmapMailOutcome(null), null);
	assert.deepEqual(jmapMailOutcome("no_mail"), { tone: "error", messageKey: "oauth_no_mail" });
	assert.deepEqual(jmapMailOutcome("expired"), { tone: "error", messageKey: "oauth_expired" });
	assert.deepEqual(jmapMailOutcome("not_configured"), {
		tone: "error",
		messageKey: "oauth_not_configured",
	});
	assert.deepEqual(jmapMailOutcome("something-new"), { tone: "error", messageKey: "oauth_failed" });
});

test("the page to come back to keeps its own query and drops an earlier outcome", () => {
	assert.equal(
		returnPathWithoutJmapOutcome({
			pathname: "/dashboard/settings/connectors",
			search: `?tab=all&${JMAP_MAIL_PARAM}=failed`,
		}),
		"/dashboard/settings/connectors?tab=all"
	);
	assert.equal(returnPathWithoutJmapOutcome({ pathname: "/x", search: "" }), "/x");
});

test("a server address becomes an https origin, and anything else is refused at the field", () => {
	assert.equal(normalizedServerUrl("mail.example.org"), "https://mail.example.org");
	assert.equal(normalizedServerUrl("  https://Mail.Example.org/ "), "https://mail.example.org");
	assert.equal(normalizedServerUrl("http://mail.example.org"), null);
	assert.equal(normalizedServerUrl("https://mail.example.org/login"), null);
	assert.equal(normalizedServerUrl("https://user:pw@mail.example.org"), null);
	assert.equal(normalizedServerUrl("localhost"), null);
	assert.equal(normalizedServerUrl(""), null);
	assert.equal(serverHost("https://mail.example.org"), "mail.example.org");
});
