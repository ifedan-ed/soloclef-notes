import assert from "node:assert/strict";
import test from "node:test";
import { emailImapAccountSchema } from "@/features/email-imap-connect/api/email-imap-api";
import {
	MICROSOFT_MAIL_PARAM,
	microsoftMailOutcome,
	outlookHintKey,
	returnPathWithoutOutcome,
} from "@/features/email-imap-connect/lib/microsoft-mail-outcome";

const base = {
	id: "abc-123",
	label: "me@contoso.example",
	host: "outlook.office365.com",
	port: 993,
	use_tls: true,
	username: "me@contoso.example",
	created_at: null,
};

test("an account from an older server reads as a password account that is fine", () => {
	const parsed = emailImapAccountSchema.parse(base);
	assert.equal(parsed.auth, "password");
	assert.equal(parsed.needs_reconnect, false);
});

test("a Microsoft account that needs reconnecting says so, and never carries a token", () => {
	const parsed = emailImapAccountSchema.parse({
		...base,
		auth: "microsoft",
		needs_reconnect: true,
		refresh_token: "should-never-survive-parsing",
	});
	assert.equal(parsed.auth, "microsoft");
	assert.equal(parsed.needs_reconnect, true);
	assert.ok(!("refresh_token" in parsed));
});

test("each way back from Microsoft has one short message, and cancelling has none", () => {
	assert.deepEqual(microsoftMailOutcome("connected"), {
		tone: "success",
		messageKey: "microsoft_connected",
	});
	assert.equal(microsoftMailOutcome("denied"), null);
	assert.deepEqual(microsoftMailOutcome("admin_approval"), {
		tone: "error",
		messageKey: "microsoft_admin_approval",
	});
	assert.deepEqual(microsoftMailOutcome("scope_missing"), {
		tone: "error",
		messageKey: "microsoft_scope_missing",
	});
	assert.deepEqual(microsoftMailOutcome("expired"), {
		tone: "error",
		messageKey: "microsoft_expired",
	});
	assert.deepEqual(microsoftMailOutcome("something-new"), {
		tone: "error",
		messageKey: "microsoft_connect_failed",
	});
	assert.equal(microsoftMailOutcome(null), null);
});

test("the page to come back to keeps its own query but not a previous outcome", () => {
	assert.equal(MICROSOFT_MAIL_PARAM, "microsoft_mail");
	assert.equal(
		returnPathWithoutOutcome({
			pathname: "/dashboard/3/workspace-settings/connectors",
			search: "?microsoft_mail=connected&q=mail",
		}),
		"/dashboard/3/workspace-settings/connectors?q=mail"
	);
	assert.equal(returnPathWithoutOutcome({ pathname: "/dashboard/3", search: "" }), "/dashboard/3");
});

test("the Outlook hint points at the button only when the server can connect Microsoft", () => {
	assert.equal(outlookHintKey(true), "outlook_hint_microsoft");
	assert.equal(outlookHintKey(false), "outlook_hint");
	assert.equal(outlookHintKey(undefined), null);
});
