/**
 * @jest-environment node
 *
 * The app's API client, chat stream and upload path against a real SurfSense
 * backend: the hermetic e2e stack (docker/docker-compose.e2e.yml — Postgres,
 * Redis, the FastAPI app and a Celery worker, with fake LLM/embedding
 * providers and no internet egress). Skipped unless SURFSENSE_E2E_URL points
 * at one, e.g.
 *
 *   docker compose -f docker/docker-compose.e2e.yml up -d --build --wait
 *   SURFSENSE_E2E_URL=http://127.0.0.1:8000 npm test -- backend.integration
 *
 * It registers its own throwaway user, so never point it at a real server.
 */
import { ApiClient, type TokenPair, type TokenStore } from "../api/client";
import { createEndpoints } from "../api/endpoints";
import { ApiError, SessionExpiredError } from "../api/errors";
import { uploadResponseSchema } from "../api/schemas";
import { buildChatRequest } from "../chat/request";
import { streamChatTurn } from "../chat/send";
import { type ContentPart, answerText, partsFromStored } from "../chat/stream-state";
import { buildStudioRequest, initialDialogState, newRequestId } from "../studio/request";
import { resolveParserChoice } from "../util/uploads";

const BASE = process.env.SURFSENSE_E2E_URL;
const suite = BASE ? describe : describe.skip;

class MemoryStore implements TokenStore {
	tokens: TokenPair | null = null;
	async load() {
		return this.tokens;
	}
	async save(tokens: TokenPair) {
		this.tokens = tokens;
	}
	async clear() {
		this.tokens = null;
	}
}

const nodeFetch = (url: string, init: RequestInit) => fetch(url, init);

async function poll<T>(read: () => Promise<T>, done: (value: T) => boolean, timeoutMs = 60_000): Promise<T> {
	const started = Date.now();
	let value = await read();
	while (!done(value)) {
		if (Date.now() - started > timeoutMs) return value;
		await new Promise((r) => setTimeout(r, 1000));
		value = await read();
	}
	return value;
}

suite("against a real backend", () => {
	jest.setTimeout(180_000);
	const email = `mobile-e2e-${Date.now()}@surfsense.net`;
	const password = "MobileE2e-Password-123!";
	const store = new MemoryStore();
	const expired = jest.fn();
	const api = new ApiClient({ baseUrl: BASE ?? "", store, fetch: nodeFetch, onSessionExpired: expired });
	const ep = createEndpoints(api);
	let workspaceId = 0;
	let notebookId = 0;

	beforeAll(async () => {
		const register = await fetch(`${BASE}/auth/register`, {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ email, password }),
		});
		expect(register.status).toBe(201);
	});

	it("rejects a wrong password with the backend's code", async () => {
		const error = await api.login(email, "wrong-password").catch((e) => e);
		expect(error).toBeInstanceOf(ApiError);
		expect(error.message).toBe("LOGIN_BAD_CREDENTIALS");
	});

	it("signs in the desktop way and reads the account", async () => {
		await api.login(email, password);
		expect(store.tokens?.refreshToken).toBeTruthy();
		const me = await ep.me();
		expect(me.email).toBe(email);
	});

	it("finds or creates a workspace", async () => {
		let spaces = await ep.workspaces();
		if (spaces.length === 0) {
			await api.post("/api/v1/workspaces", { name: "Mobile e2e", description: "throwaway" });
			spaces = await ep.workspaces();
		}
		expect(spaces.length).toBeGreaterThan(0);
		workspaceId = spaces[0].id;
	});

	it("creates, lists, renames a notebook", async () => {
		const created = await ep.createNotebook(workspaceId, { title: "Mobile e2e notebook", icon: "🧪" });
		notebookId = created.id;
		expect(created.chat_goal).toBe("default");
		const list = await ep.notebooks(workspaceId);
		expect(list.map((n) => n.id)).toContain(notebookId);
		const renamed = await ep.updateNotebook(workspaceId, notebookId, { title: "Renamed on the phone" });
		expect(renamed.title).toBe("Renamed on the phone");
	});

	it("uploads a file with the parser choice and links it as a source", async () => {
		const capabilities = await ep.etlCapabilities();
		const { effectiveParser } = resolveParserChoice(capabilities, "local");
		const form = new FormData();
		form.append(
			"files",
			new Blob(["# Mobile upload\n\nAsthma notes for the mobile e2e test."], { type: "text/markdown" }),
			"mobile-e2e.md"
		);
		form.append("workspace_id", String(workspaceId));
		form.append("use_vision_llm", "false");
		if (effectiveParser) form.append("parser", effectiveParser);
		const response = await api.fetchAuthed("/api/v1/documents/fileupload", { method: "POST", body: form });
		expect(response.status).toBeLessThan(300);
		const uploaded = uploadResponseSchema.parse(await response.json());
		const ids = [...uploaded.document_ids, ...(uploaded.duplicate_document_ids ?? [])];
		expect(ids.length).toBe(1);
		const sources = await ep.addSources(
			workspaceId,
			notebookId,
			ids.map((id) => ({ kind: "document" as const, id }))
		);
		expect(sources).toHaveLength(1);
		const settled = await poll(
			() => ep.sources(workspaceId, notebookId),
			(list) => list.every((s) => s.status !== "pending" && s.status !== "processing")
		);
		expect(settled[0].title).toBeTruthy();
		const toggled = await ep.toggleSource(workspaceId, notebookId, settled[0].id, false);
		expect(toggled.enabled).toBe(false);
		await ep.toggleAllSources(workspaceId, notebookId, true);
		const guide = await ep.sourceGuide(workspaceId, notebookId, settled[0].id);
		expect(["ready", "unavailable"]).toContain(guide.status);
	});

	it("imports a pasted link as a pending source", async () => {
		const sources = await ep.importLinks(workspaceId, notebookId, [
			{ url: "https://example.org/mobile-e2e", title: "https://example.org/mobile-e2e", source: "web" },
		]);
		expect(sources.some((s) => s.title.length > 0)).toBe(true);
	});

	it("opens the notebook's one chat and streams an answer", async () => {
		const threads = await ep.threads(workspaceId, notebookId);
		const thread = threads[0] ?? (await ep.createThread(workspaceId, notebookId));
		const updates: string[] = [];
		const userIds: number[] = [];
		let accepted = false;
		const parts: ContentPart[] = await streamChatTurn({
			api,
			fetchImpl: nodeFetch,
			body: buildChatRequest({
				threadId: thread.id,
				workspaceId,
				query: "E2E_NO_RELEVANT_CONTENT_SMOKE",
				history: [],
				mentions: [],
			}),
			effects: { onUserMessageId: (id) => userIds.push(id) },
			onUpdate: (p) => updates.push(answerText(p)),
			onAccepted: () => {
				accepted = true;
			},
		});
		expect(accepted).toBe(true);
		expect(answerText(parts)).toContain("No relevant indexed content found.");
		expect(updates.length).toBeGreaterThan(0);
		expect(userIds).toHaveLength(1);
		const history = await poll(
			() => ep.threadMessages(thread.id),
			(h) => h.messages.some((m) => m.role === "assistant")
		);
		const assistant = history.messages.find((m) => m.role === "assistant");
		expect(answerText(partsFromStored(assistant?.content))).toContain("No relevant indexed content found.");
	});

	it("lists studio outputs and accepts a generate request built like the web's", async () => {
		const outputs = await ep.outputs(workspaceId, notebookId);
		expect(Array.isArray(outputs)).toBe(true);
		const sources = await ep.sources(workspaceId, notebookId);
		const state = initialDialogState(
			"mindmap",
			sources.filter((s) => s.enabled).map((s) => s.id)
		);
		const body = buildStudioRequest(state, {
			enabledSourceIds: state.sourceIds,
			title: "Mind Map: Renamed on the phone",
			requestId: newRequestId(),
		});
		try {
			const output = await ep.generate(workspaceId, notebookId, body);
			expect(output.kind).toBe("mindmap");
			expect(["queued", "running", "ready", "failed"]).toContain(output.status);
			await ep.deleteOutput(workspaceId, notebookId, output.id);
		} catch (error) {
			// A hermetic stack may lack a studio model; the request must still validate.
			expect(error).toBeInstanceOf(ApiError);
			expect((error as ApiError).status).not.toBe(422);
		}
	});

	it("rotates the refresh token; the spent one only buys an access token", async () => {
		const before = store.tokens?.refreshToken;
		expect(await api.refresh()).toBe(true);
		const after = store.tokens?.refreshToken;
		expect(after).toBeTruthy();
		expect(after).not.toBe(before);
		const reuse = await fetch(`${BASE}/auth/jwt/refresh`, {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ refresh_token: before }),
		});
		// Within REFRESH_ROTATION_GRACE_SECONDS a racing reuse gets an access
		// token but no new refresh token; the app then keeps the one it has.
		expect(reuse.status).toBe(200);
		const body = (await reuse.json()) as { access_token?: string; refresh_token?: string | null };
		expect(body.access_token).toBeTruthy();
		expect(body.refresh_token ?? null).toBeNull();
	});

	it("deletes the notebook", async () => {
		await ep.deleteNotebook(workspaceId, notebookId);
		const list = await ep.notebooks(workspaceId);
		expect(list.map((n) => n.id)).not.toContain(notebookId);
	});

	it("logs out: the device's refresh token is revoked", async () => {
		const refreshToken = store.tokens?.refreshToken;
		await api.logout();
		expect(store.tokens).toBeNull();
		const reuse = await fetch(`${BASE}/auth/jwt/refresh`, {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ refresh_token: refreshToken }),
		});
		expect(reuse.status).toBe(401);
		await expect(ep.me()).rejects.toBeInstanceOf(SessionExpiredError);
		expect(expired).toHaveBeenCalled();
	});
});
