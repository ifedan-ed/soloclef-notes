import assert from "node:assert/strict";
import { mock, test } from "node:test";
import { emailImapApi } from "@/features/email-imap-connect/api/email-imap-api";
import { microsoftMailApi } from "@/features/email-imap-connect/api/microsoft-mail-api";
import { googleDriveApi } from "@/features/google-drive-connect/api/google-drive-api";
import { baseApiService } from "@/lib/apis/base-api.service";

// Run with: pnpm exec tsx --test tests/unit/connectors/account-connector-paths.test.ts
//
// Mailboxes and the Drive connection are the account's own:
// the server takes the account from the session. These pin the paths and
// bodies, so no workspace id (in a path, a query string or a body) comes back.

type Call = { arguments: unknown[] };
const urls = (calls: Call[]) => calls.map((call) => String(call.arguments[0]));
const bodies = (calls: Call[]) =>
	calls.map((call) => (call.arguments[2] as { body?: unknown } | undefined)?.body);

const ACCOUNT = {
	label: "me",
	host: "imap.example.com",
	port: 993,
	use_tls: true,
	username: "me@example.com",
	app_password: "x",
};

test("mailboxes are listed, added, tested and removed under the account", async (t) => {
	const get = mock.method(baseApiService, "get", async () => []);
	const post = mock.method(baseApiService, "post", async () => ({}));
	const del = mock.method(baseApiService, "delete", async () => ({}));
	t.after(() => mock.restoreAll());

	await emailImapApi.list();
	await emailImapApi.add(ACCOUNT);
	await emailImapApi.test("a/b");
	await emailImapApi.testUnsaved(ACCOUNT);
	await emailImapApi.remove("a/b");
	await microsoftMailApi.availability();
	await microsoftMailApi.connect("/dashboard/settings/connectors", "acc");

	assert.deepEqual(urls(get.mock.calls), [
		"/api/v1/connectors/email-imap/accounts",
		"/api/v1/connectors/email-imap/microsoft",
	]);
	assert.deepEqual(urls(post.mock.calls), [
		"/api/v1/connectors/email-imap/accounts",
		"/api/v1/connectors/email-imap/accounts/a%2Fb/test",
		"/api/v1/connectors/email-imap/test",
		"/api/v1/connectors/email-imap/microsoft/connect",
	]);
	assert.deepEqual(urls(del.mock.calls), ["/api/v1/connectors/email-imap/accounts/a%2Fb"]);
	assert.deepEqual(bodies(post.mock.calls)[3], {
		return_path: "/dashboard/settings/connectors",
		account_id: "acc",
	});
});

test("the Drive connection is the account's one, and an import names only its notebook", async (t) => {
	const get = mock.method(baseApiService, "get", async () => ({}));
	const post = mock.method(baseApiService, "post", async () => ({}));
	t.after(() => mock.restoreAll());

	await googleDriveApi.connection();
	await googleDriveApi.connect("/dashboard/notebooks/4");
	await googleDriveApi.importPicked(9, { files: [], notebook_id: 4 });

	assert.deepEqual(urls(get.mock.calls), ["/api/v1/google-drive/connection"]);
	assert.deepEqual(urls(post.mock.calls), [
		"/api/v1/google-drive/connect",
		"/api/v1/connectors/9/google-drive/import",
	]);
	assert.deepEqual(bodies(post.mock.calls)[1], { files: [], notebook_id: 4 });
});
