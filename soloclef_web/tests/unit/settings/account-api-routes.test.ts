import assert from "node:assert/strict";
import { mock, test } from "node:test";
import {
	clearNotificationsRequest,
	getNotificationsRequest,
	inboxItem,
} from "@/contracts/types/inbox.types";
import { createLogRequest, getLogsRequest, log } from "@/contracts/types/log.types";
import { promptCreateRequest, promptRead } from "@/contracts/types/prompts.types";
import { updateUserSettingsRequest, user, userSettings } from "@/contracts/types/user.types";
import { modelChoiceApi } from "@/features/model-choice/model-choice-api";
import { agentPermissionsApiService } from "@/lib/apis/agent-permissions-api.service";
import { baseApiService } from "@/lib/apis/base-api.service";
import { logsApiService } from "@/lib/apis/logs-api.service";
import { modelConnectionsApiService } from "@/lib/apis/model-connections-api.service";
import { notificationsApiService } from "@/lib/apis/notifications-api.service";
import { promptsApiService } from "@/lib/apis/prompts-api.service";
import { userApiService } from "@/lib/apis/user-api.service";
import { ValidationError } from "@/lib/error";
import { cacheKeys } from "@/lib/query-client/cache-keys";

// Run with: pnpm exec tsx --test tests/unit/settings/account-api-routes.test.ts
//
// There are no workspaces: every row is the signed-in account's, and the
// server takes the account from the session. These tests pin the paths and
// bodies the services send, so a workspace id cannot find its way back in.

type Call = { arguments: unknown[] };
const urls = (calls: Call[]) => calls.map((call) => call.arguments[0]);
const bodyOf = (call: Call) => (call.arguments[2] as { body?: unknown } | undefined)?.body;

const SETTINGS = { chat_instructions: "", citations_enabled: true, api_access_enabled: false };

test("account settings are read and changed under /users/me/settings", async (t) => {
	const get = mock.method(baseApiService, "get", async () => SETTINGS);
	const patch = mock.method(baseApiService, "patch", async () => SETTINGS);
	t.after(() => mock.restoreAll());

	await userApiService.getSettings();
	assert.deepEqual(urls(get.mock.calls), ["/api/v1/users/me/settings"]);
	assert.equal(get.mock.calls[0].arguments[1], userSettings);

	// Any subset is a change; null clears the instructions.
	await userApiService.updateSettings({ citations_enabled: false });
	await userApiService.updateSettings({ chat_instructions: null });
	assert.deepEqual(urls(patch.mock.calls), [
		"/api/v1/users/me/settings",
		"/api/v1/users/me/settings",
	]);
	assert.deepEqual(bodyOf(patch.mock.calls[0]), { citations_enabled: false });
	assert.deepEqual(bodyOf(patch.mock.calls[1]), { chat_instructions: null });
});

test("a settings change the server would refuse never leaves the browser", async (t) => {
	const patch = mock.method(baseApiService, "patch", async () => SETTINGS);
	t.after(() => mock.restoreAll());

	// An unknown key is a 422 on the server.
	await assert.rejects(
		userApiService.updateSettings({ citations: true } as never),
		ValidationError
	);
	// So are instructions over 4000 characters; exactly 4000 is allowed.
	await assert.rejects(
		userApiService.updateSettings({ chat_instructions: "x".repeat(4001) }),
		ValidationError
	);
	assert.equal(patch.mock.callCount(), 0);
	assert.equal(
		updateUserSettingsRequest.safeParse({ chat_instructions: "x".repeat(4000) }).success,
		true
	);
});

test("the account read has no credit balance", () => {
	const me = user.parse({
		id: "7b0e0c0e-2f0a-4a3e-9a51-3f1f6d1f0a11",
		email: "person@example.com",
		is_active: true,
		is_superuser: false,
		is_verified: true,
	});
	assert.equal(Object.hasOwn(me, "credit_micros_balance"), false);
});

test("prompts are listed and made with no workspace id", async (t) => {
	const get = mock.method(baseApiService, "get", async () => []);
	const post = mock.method(baseApiService, "post", async () => ({}));
	t.after(() => mock.restoreAll());

	await promptsApiService.list();
	assert.deepEqual(urls(get.mock.calls), ["/api/v1/prompts"]);

	await promptsApiService.create({ name: "Shorten", prompt: "Shorten this.", mode: "transform" });
	assert.deepEqual(urls(post.mock.calls), ["/api/v1/prompts"]);
	assert.deepEqual(bodyOf(post.mock.calls[0]), {
		name: "Shorten",
		prompt: "Shorten this.",
		mode: "transform",
	});

	// Neither the request nor a prompt read keeps one that is passed in.
	const stripped = promptCreateRequest.parse({
		name: "Shorten",
		prompt: "Shorten this.",
		mode: "transform",
		workspace_id: 7,
	});
	assert.equal(Object.hasOwn(stripped, "workspace_id"), false);
	const read = promptRead.parse({
		id: 1,
		name: "Shorten",
		prompt: "Shorten this.",
		mode: "transform",
		is_public: false,
		version: 1,
		created_at: "2026-11-11T09:00:00Z",
		workspace_id: 7,
	});
	assert.equal(Object.hasOwn(read, "workspace_id"), false);
});

test("the activity log is the account's: list, create and summary name no workspace", async (t) => {
	const get = mock.method(baseApiService, "get", async () => []);
	const post = mock.method(baseApiService, "post", async () => ({}));
	t.after(() => mock.restoreAll());

	await logsApiService.getLogs({ queryParams: { level: "ERROR" } });
	await logsApiService.getLogSummary({ hours: 24 });
	await logsApiService.getLogSummary({});
	assert.deepEqual(urls(get.mock.calls), [
		"/api/v1/logs?level=ERROR",
		"/api/v1/logs/summary?hours=24",
		"/api/v1/logs/summary",
	]);

	await logsApiService.createLog({ level: "INFO", status: "SUCCESS", message: "Done" });
	assert.deepEqual(urls(post.mock.calls), ["/api/v1/logs"]);
	assert.deepEqual(bodyOf(post.mock.calls[0]), {
		level: "INFO",
		status: "SUCCESS",
		message: "Done",
	});

	const entry = { level: "INFO", status: "SUCCESS", message: "Done", workspace_id: 7 };
	assert.equal(Object.hasOwn(createLogRequest.parse(entry), "workspace_id"), false);
	assert.equal(
		Object.hasOwn(
			log.parse({ ...entry, id: 1, created_at: "2026-11-11T09:00:00Z" }),
			"workspace_id"
		),
		false
	);
	const listed = getLogsRequest.parse({ queryParams: { workspace_id: 7, level: "ERROR" } });
	assert.deepEqual(listed.queryParams, { level: "ERROR" });
});

test("every log summary query sits under the one key that refreshes them all", () => {
	assert.deepEqual(cacheKeys.logs.summary(), ["logs", "summary"]);
	assert.deepEqual(cacheKeys.logs.list(), ["logs", "list"]);
});

test("model connections, roles and setup status are the account's", async (t) => {
	const get = mock.method(baseApiService, "get", async () => []);
	const put = mock.method(baseApiService, "put", async () => ({}));
	const post = mock.method(baseApiService, "post", async () => ({}));
	t.after(() => mock.restoreAll());

	await modelConnectionsApiService.getConnections();
	await modelChoiceApi.readRoles();
	await modelChoiceApi.read("quick");
	await modelChoiceApi.read("studio");
	await modelConnectionsApiService.getLlmSetupStatus();
	assert.deepEqual(urls(get.mock.calls), [
		"/api/v1/model-connections",
		"/api/v1/users/me/model-roles",
		"/api/v1/users/me/quick-model",
		"/api/v1/users/me/studio-model",
		"/api/v1/users/me/llm-setup-status",
	]);

	// A model is chosen by its proxy name, null for the default; a role's
	// body names only that role, so the other is left as it was.
	await modelChoiceApi.chooseRole("vision", "watcher");
	await modelChoiceApi.choose("quick", null);
	await modelChoiceApi.choose("studio", "smart");
	assert.deepEqual(urls(put.mock.calls), [
		"/api/v1/users/me/model-roles",
		"/api/v1/users/me/quick-model",
		"/api/v1/users/me/studio-model",
	]);
	assert.deepEqual(bodyOf(put.mock.calls[0]), { vision: "watcher" });
	assert.deepEqual(bodyOf(put.mock.calls[1]), { model: null });
	assert.deepEqual(bodyOf(put.mock.calls[2]), { model: "smart" });

	await modelConnectionsApiService.createConnection({
		provider: "openai",
		scope: "user",
		extra: {},
		enabled: true,
		models: [],
	});
	assert.deepEqual(urls(post.mock.calls), ["/api/v1/model-connections"]);
	assert.deepEqual(bodyOf(post.mock.calls[0]), {
		provider: "openai",
		scope: "user",
		extra: {},
		enabled: true,
		models: [],
	});
});

test("the account's own connections are not refetched with the shared ones", () => {
	// Invalidation matches by prefix: the own list's key must not start any
	// of the shared model keys, and none of them may start it.
	const own = cacheKeys.modelConnections.all();
	for (const other of [
		cacheKeys.modelConnections.global(),
		cacheKeys.modelConnections.globalConfigStatus(),
		cacheKeys.modelConnections.providers(),
	]) {
		assert.notDeepEqual(other.slice(0, own.length), [...own]);
		assert.notDeepEqual(own.slice(0, other.length), [...other]);
	}
	assert.deepEqual(cacheKeys.modelConnections.roles(), ["model-roles"]);
	assert.deepEqual(cacheKeys.modelConnections.setupStatus(), ["llm-setup-status"]);
});

test("notifications are asked for with their filters and no workspace id", async (t) => {
	const get = mock.method(baseApiService, "get", async () => ({}));
	const del = mock.method(baseApiService, "delete", async () => ({}));
	t.after(() => mock.restoreAll());

	await notificationsApiService.getNotifications({
		queryParams: { category: "status", limit: 20 },
	});
	await notificationsApiService.getSourceTypes();
	await notificationsApiService.getUnreadCount();
	await notificationsApiService.getUnreadCount(undefined, "announcements");
	await notificationsApiService.getBatchUnreadCounts();
	assert.deepEqual(urls(get.mock.calls), [
		"/api/v1/notifications?category=status&limit=20",
		"/api/v1/notifications/source-types",
		"/api/v1/notifications/unread-count",
		"/api/v1/notifications/unread-count?category=announcements",
		"/api/v1/notifications/unread-counts-batch",
	]);

	await notificationsApiService.clearNotifications({
		queryParams: { category: "status", filter: "unread" },
	});
	assert.deepEqual(urls(del.mock.calls), ["/api/v1/notifications?category=status&filter=unread"]);

	// A workspace id handed to either request, or sent on a notification, is dropped.
	const asked = getNotificationsRequest.parse({ queryParams: { workspace_id: 7, limit: 20 } });
	assert.deepEqual(asked.queryParams, { limit: 20 });
	const cleared = clearNotificationsRequest.parse({ queryParams: { workspace_id: 7 } });
	assert.deepEqual(cleared.queryParams, {});
	const item = inboxItem.parse({
		id: 1,
		user_id: "owner",
		workspace_id: 7,
		type: "document_processing",
		title: "Done",
		message: "",
		read: false,
		metadata: {},
		created_at: "2026-11-11T09:00:00Z",
		updated_at: null,
	});
	assert.equal(Object.hasOwn(item, "workspace_id"), false);
	assert.deepEqual(cacheKeys.notifications.batchUnreadCounts(), [
		"notifications",
		"unread-counts-batch",
	]);
});

test("agent permission rules live at /agent/permissions/rules", async (t) => {
	// The Go API's rule has no user_id: every rule is the account's.
	const rule = {
		id: 3,
		thread_id: null,
		permission: "mcp:search",
		pattern: "*",
		action: "allow",
		created_at: "2026-11-11T09:00:00Z",
	};
	const get = mock.method(baseApiService, "get", async () => [rule]);
	const post = mock.method(baseApiService, "post", async () => rule);
	const patch = mock.method(baseApiService, "patch", async () => rule);
	const del = mock.method(baseApiService, "delete", async () => undefined);
	t.after(() => mock.restoreAll());

	await agentPermissionsApiService.list();
	await agentPermissionsApiService.create({
		permission: "mcp:search",
		pattern: "*",
		action: "allow",
	});
	await agentPermissionsApiService.update(3, { action: "deny" });
	await agentPermissionsApiService.remove(3);

	assert.deepEqual(urls(get.mock.calls), ["/api/v1/agent/permissions/rules"]);
	assert.deepEqual(urls(post.mock.calls), ["/api/v1/agent/permissions/rules"]);
	assert.deepEqual(bodyOf(post.mock.calls[0]), {
		permission: "mcp:search",
		pattern: "*",
		action: "allow",
	});
	assert.deepEqual(urls(patch.mock.calls), ["/api/v1/agent/permissions/rules/3"]);
	assert.deepEqual(urls(del.mock.calls), ["/api/v1/agent/permissions/rules/3"]);
});
