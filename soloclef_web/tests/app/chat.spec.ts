import type { APIRequestContext, Page } from "@playwright/test";
import { authHeaders, BACKEND_URL } from "../helpers/api/auth";
import {
	addNoteSource,
	createNotebook,
	documentChunkIds,
	listNotes,
	listSources,
	listThreads,
} from "../helpers/api/notebooks";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { assistantMessages, send, userMessages } from "../helpers/ui/chat";
import { dropFiles } from "../helpers/ui/drop";
import { expect, nbPath, openNotebook, showPanel, test, waitForChatReady } from "./fixtures";

/**
 * Notebook chat: one chat per notebook, welcome card, streaming answers,
 * citations, follow-ups, save to note.
 *
 * Answers come from the real backend's /api/v1/new_chat stream with the E2E
 * fake chat model (tests/e2e/fakes/chat_llm.py): "E2E_ANSWER" gets a fixed
 * answer and "E2E_CITE:<chunk id>" gets one citing that chunk. Suggested
 * questions, follow-ups and source guides come from the fake quick-tasks model.
 * "E2E_KB_SCOPE" runs the real knowledge-base subagent and search, and the fake
 * answer lists every SCOPE-* canary the search could reach. "E2E_WEB_PROBE"
 * makes the main agent delegate to the Google Search specialist and answers
 * "web blocked" or "web ran" depending on whether the turn allowed it.
 */

const assistant = assistantMessages;

/** The workspace cookie the removed Sources / + Web switch used to write. */
async function setScopeCookie(
	page: Page,
	request: APIRequestContext,
	token: string,
	baseURL: string | undefined,
	workspaceId: number,
	scope: "documents" | "web" | "all"
): Promise<void> {
	const me = (await (
		await request.get(`${BACKEND_URL}/users/me`, { headers: authHeaders(token) })
	).json()) as { id: string };
	await page.context().addCookies([
		{
			name: "surfsense_retrieval_scope_v1",
			value: encodeURIComponent(`${me.id}:${scope}`),
			url: `${baseURL}/dashboard/${workspaceId}`,
		},
	]);
}

test.describe("Notebook chat", () => {
	test("welcome card without sources offers guided starts and adding sources", async ({
		page,
		workspace,
		notebook,
		isPhone,
	}) => {
		await openNotebook(page, workspace.id, notebook.id, "Chat");
		await expect(page.getByText("Let's start your notebook")).toBeVisible();
		for (const start of [
			"Learn about a new topic",
			"Create something new",
			"Make progress on a project",
		]) {
			await expect(page.getByRole("button", { name: start })).toBeVisible();
		}
		await expect(page.getByRole("button", { name: "No sources" })).toBeVisible();
		await expectNoOverflow(page, "chat welcome (no sources)");

		// "Add sources" from the welcome card opens the dialog (on a phone it
		// also switches to the Sources tab).
		const welcome = page
			.locator("main, section")
			.filter({ hasText: "Let's start your notebook" })
			.last();
		await welcome.getByRole("button", { name: "Add sources" }).click();
		await expect(page.getByRole("dialog", { name: "Add sources" })).toBeVisible();
		await expectNoOverflow(page, "Add sources dialog from the welcome card");
		await page.keyboard.press("Escape");
		if (isPhone) {
			await expect(page.getByRole("tab", { name: /^Sources/ })).toHaveAttribute(
				"aria-selected",
				"true"
			);
		}
	});

	test("one chat per notebook: the thread is created once and reused", async ({
		page,
		workspace,
		notebook,
		request,
		token,
	}) => {
		await openNotebook(page, workspace.id, notebook.id, "Chat");
		await expect
			.poll(async () => (await listThreads(request, token, workspace.id, notebook.id)).length)
			.toBe(1);
		const [first] = await listThreads(request, token, workspace.id, notebook.id);
		await openNotebook(page, workspace.id, notebook.id, "Chat");
		const threads = await listThreads(request, token, workspace.id, notebook.id);
		expect(threads.map((t) => t.id)).toEqual([first.id]);
		// The URL never names the chat.
		expect(new URL(page.url()).pathname).toBe(nbPath(workspace.id, notebook.id));
		expect(new URL(page.url()).searchParams.has("chat")).toBe(false);
	});

	test("suggested question → streamed answer → follow-ups → save to note", async ({
		page,
		workspace,
		notebook,
		request,
		token,
	}) => {
		await addNoteSource(
			request,
			token,
			workspace.id,
			notebook.id,
			"Photosynthesis basics",
			"Light to sugar."
		);
		await openNotebook(page, workspace.id, notebook.id, "Chat");
		await expect(page.getByText("Ask your sources")).toBeVisible();
		const suggestions = page.getByRole("navigation", { name: "Suggested questions" });
		await expect(suggestions.getByRole("button").first()).toContainText("E2E idea");
		await expect(page.getByRole("button", { name: "1 source" })).toBeVisible();
		await expectNoOverflow(page, "chat welcome (with sources)");

		// Send our own question so the fake answers deterministically.
		await send(page, "E2E_ANSWER what do my sources say?");
		await expect(page.locator('[data-role="user"]').last()).toContainText("E2E_ANSWER");
		await expect(assistant(page).last()).toContainText("E2E notebook answer", { timeout: 45_000 });
		await expectNoOverflow(page, "chat with an answer");

		const followups = page.getByRole("navigation", { name: "Suggested follow-up questions" });
		await expect(followups.getByRole("button")).toHaveCount(3, { timeout: 20_000 });
		await expect(followups.getByRole("button").first()).toContainText("E2E follow-up");
		await expectNoOverflow(page, "chat with follow-ups");

		// Save the answer to a note (inline toast, no popup).
		await assistant(page).last().hover();
		await assistant(page).last().getByRole("button", { name: "Save to note" }).click();
		await expect(page.getByText("Saved to notes")).toBeVisible();
		await expect
			.poll(async () => (await listNotes(request, token, workspace.id, notebook.id)).length)
			.toBeGreaterThanOrEqual(2); // the source note + the saved answer

		// A follow-up click sends it.
		await followups.getByRole("button").first().click();
		await expect(page.locator('[data-role="user"]').last()).toContainText("E2E follow-up");
		await expect(assistant(page)).toHaveCount(2, { timeout: 45_000 });

		// The conversation is still there after a reload.
		await page.reload();
		await showPanel(page, "Chat");
		await expect(page.locator('[data-role="user"]').first()).toContainText("E2E_ANSWER");
	});

	test("citations open the cited source", async ({
		page,
		workspace,
		notebook,
		request,
		token,
		isPhone,
	}) => {
		const source = await addNoteSource(
			request,
			token,
			workspace.id,
			notebook.id,
			"Citable source",
			"# Citable\n\nThe canary sentence is CITE-ME-91."
		);
		const [chunk] = await documentChunkIds(request, token, source.target_id);
		await openNotebook(page, workspace.id, notebook.id, "Chat");
		await send(page, `E2E_CITE:${chunk} cite the source`);
		const citation = page.getByRole("button", { name: `View cited chunk ${chunk}` });
		await expect(citation).toBeVisible({ timeout: 45_000 });
		await citation.click();
		if (isPhone) {
			await expect(page.getByRole("dialog")).toBeVisible();
			await expect(
				page
					.getByRole("dialog")
					.getByText(/CITE-ME-91|Citable source/)
					.first()
			).toBeVisible();
		} else {
			await expect(page.getByText(/CITE-ME-91/).first()).toBeVisible();
		}
		await expectNoOverflow(page, "citation panel");
	});

	test("answers only from this notebook's sources, not other workspace documents", async ({
		page,
		workspace,
		notebook,
		request,
		token,
	}) => {
		// Regression: a notebook chat searched the whole workspace (other
		// notebooks' sources, Studio outputs) after its own sources.
		const inside = await addNoteSource(
			request,
			token,
			workspace.id,
			notebook.id,
			"Scope inside",
			"The inside canary is SCOPE-IN-4417."
		);
		const other = await createNotebook(request, token, workspace.id, "Other notebook");
		const outside = await addNoteSource(
			request,
			token,
			workspace.id,
			other.id,
			"Scope outside",
			"The outside canary is SCOPE-OUT-8812."
		);
		await documentChunkIds(request, token, inside.target_id);
		await documentChunkIds(request, token, outside.target_id);

		await openNotebook(page, workspace.id, notebook.id, "Chat");
		await send(page, "E2E_KB_SCOPE which canaries can you see?");
		const answer = assistant(page).last();
		await expect(answer).toContainText("E2E scope answer:", { timeout: 60_000 });
		await expect(answer).toContainText("SCOPE-IN-4417");
		await expect(answer).not.toContainText("SCOPE-OUT-8812");
	});

	test("a file dropped on the chat becomes a source the chat can read, even with the Web scope", async ({
		page,
		workspace,
		notebook,
		request,
		token,
		baseURL,
	}) => {
		// Regression (notebook 10): the only source was a root-level file
		// dropped onto the notebook, and the composer's saved retrieval scope was
		// "web"; every knowledge-base tool was denied and the agent looped.
		await setScopeCookie(page, request, token, baseURL, workspace.id, "web");

		await openNotebook(page, workspace.id, notebook.id, "Chat");
		// A drop on the chat is a source, not a message attachment.
		await dropFiles(page, page.locator(".aui-composer-root"), [
			{
				name: "premature-birth.md",
				type: "text/markdown",
				text: "# Premature birth\n\nThe dropped canary is SCOPE-DROP-5521.",
			},
		]);
		const dialog = page.getByRole("dialog", { name: "Add sources" });
		await expect(dialog.getByRole("tab", { name: /Upload/ })).toHaveAttribute(
			"aria-selected",
			"true"
		);
		await expect(dialog.getByText("premature-birth.md")).toBeVisible();
		const local = dialog.getByRole("button", { name: /^Local/ });
		if (await local.isVisible()) await local.click();
		await dialog.getByRole("button", { name: /^Upload 1 file/ }).click();
		await expect(dialog).toBeHidden({ timeout: 60_000 });

		let documentId = 0;
		await expect
			.poll(
				async () => {
					const sources = (await listSources(
						request,
						token,
						workspace.id,
						notebook.id
					)) as unknown as {
						title: string;
						target_id: number;
						status?: string;
					}[];
					const source = sources.find((s) => s.title.includes("premature-birth"));
					documentId = source?.target_id ?? 0;
					return source ? (source.status ?? "ready") : "missing";
				},
				{ timeout: 60_000 }
			)
			.toBe("ready");
		await documentChunkIds(request, token, documentId);

		await showPanel(page, "Chat");
		await send(page, "E2E_KB_SCOPE which canaries can you see?");
		const answer = assistant(page).last();
		await expect(answer).toContainText("E2E scope answer:", { timeout: 60_000 });
		await expect(answer).toContainText("SCOPE-DROP-5521");
	});

	test("sources only: with the Web cookie set, the chat answers from its sources and never searches the web", async ({
		page,
		workspace,
		notebook,
		request,
		token,
		baseURL,
	}) => {
		// The old Sources / + Web switch is gone but its workspace cookie can
		// still say "web"; the server keeps a notebook chat on its sources.
		await setScopeCookie(page, request, token, baseURL, workspace.id, "web");
		const inside = await addNoteSource(
			request,
			token,
			workspace.id,
			notebook.id,
			"Sources only",
			"The sources-only canary is SCOPE-ONLY-3391."
		);
		await documentChunkIds(request, token, inside.target_id);

		await openNotebook(page, workspace.id, notebook.id, "Chat");
		await send(page, "E2E_KB_SCOPE which canaries can you see?");
		await expect(assistant(page).last()).toContainText("SCOPE-ONLY-3391", { timeout: 60_000 });

		// The fake model tries the Google Search specialist anyway.
		const sent = page.waitForRequest(
			(r) => r.url().endsWith("/api/v1/new_chat") && r.method() === "POST"
		);
		await send(page, "E2E_WEB_PROBE what is new on the web?");
		const body = (await sent).postDataJSON() as { retrieval_scope?: string };
		expect(body.retrieval_scope).toBe("web");
		await expect(assistant(page).last()).toContainText("E2E web probe: web blocked", {
			timeout: 60_000,
		});
	});

	test("a web tool picked from the Tools menu runs for that one message only", async ({
		page,
		workspace,
		notebook,
	}) => {
		await openNotebook(page, workspace.id, notebook.id, "Chat");
		await page.getByRole("button", { name: "Pick a tool for the next message" }).click();
		await page.getByRole("menuitem", { name: "Google Search" }).click();
		const sent = page.waitForRequest(
			(r) => r.url().endsWith("/api/v1/new_chat") && r.method() === "POST"
		);
		await send(page, "E2E_WEB_PROBE search the web");
		const body = (await sent).postDataJSON() as { preferred_tool?: string };
		expect(body.preferred_tool).toBe("google_search");
		await expect(assistant(page).last()).toContainText("E2E web probe: web ran", {
			timeout: 60_000,
		});

		// The pick lasted one message; the next one is sources-only again.
		await send(page, "E2E_WEB_PROBE and again");
		await expect(assistant(page)).toHaveCount(2, { timeout: 60_000 });
		await expect(assistant(page).last()).toContainText("E2E web probe: web blocked", {
			timeout: 60_000,
		});
	});

	test("delete chat history clears the conversation", async ({
		page,
		workspace,
		notebook,
		request,
		token,
	}) => {
		await openNotebook(page, workspace.id, notebook.id, "Chat");
		await send(page, "E2E_ANSWER first question");
		await expect(assistant(page).last()).toContainText("E2E notebook answer", { timeout: 45_000 });
		const before = new URL(page.url()).searchParams.get("chat");

		await page.getByRole("button", { name: "Notebook options" }).click();
		await page.getByRole("menuitem", { name: "Delete chat history" }).click();
		const dialog = page.getByRole("dialog", { name: "Delete chat history" });
		await expect(dialog).toBeVisible();
		await expectNoOverflow(page, "delete chat history dialog");
		await dialog.getByRole("button", { name: "Delete my chats" }).click();
		await expect(dialog).toBeHidden();
		await expect(page.getByText("Your chat history in this notebook was deleted.")).toBeVisible();
		await expect(page.locator('[data-role="user"]')).toHaveCount(0);
		await expect
			.poll(async () =>
				(await listThreads(request, token, workspace.id, notebook.id)).map((t) => String(t.id))
			)
			.not.toContain(before);
	});

	test("text typed while the chat is still loading is kept and sent", async ({
		page,
		workspace,
		notebook,
	}) => {
		// Regression: the notebook welcome swaps in its own composer once the
		// chat's messages load; keystrokes made before that were dropped.
		await page.route(/\/api\/v1\/threads\/\d+(\/full)?$/, async (route) => {
			await new Promise((r) => setTimeout(r, 1500));
			await route.continue();
		});
		// Not openNotebook(…, "Chat"): that waits for the chat to finish loading.
		await openNotebook(page, workspace.id, notebook.id);
		await showPanel(page, "Chat");
		await send(page, "E2E_ANSWER typed during loading");
		await expect(userMessages(page).last()).toContainText("E2E_ANSWER typed during loading");
		await expect(assistant(page).last()).toContainText("E2E notebook answer", { timeout: 45_000 });
	});

	test("legacy chat URLs land in the notebook (or the home) without a chat param", async ({
		page,
		workspace,
		notebook,
		request,
		token,
	}) => {
		await openNotebook(page, workspace.id, notebook.id, "Chat");
		await expect
			.poll(async () => (await listThreads(request, token, workspace.id, notebook.id)).length)
			.toBe(1);
		const [thread] = await listThreads(request, token, workspace.id, notebook.id);

		// The retired standalone chat route forwards to the notebooks home.
		await page.goto(`/dashboard/${workspace.id}/new-chat/${thread.id}`);
		await expect(page).toHaveURL(new RegExp(`/dashboard/${workspace.id}/notebooks$`));

		// An old notebook link naming its chat opens the notebook, and drops the param.
		await page.goto(`${nbPath(workspace.id, notebook.id)}?chat=${thread.id}`);
		await expect(page).toHaveURL(new RegExp(`${nbPath(workspace.id, notebook.id)}$`));
		await showPanel(page, "Chat");
		await waitForChatReady(page);

		// Chatting never writes the chat into the URL.
		await send(page, "E2E_ANSWER no chat id in the address");
		await expect(assistant(page).last()).toContainText("E2E notebook answer", { timeout: 45_000 });
		const url = new URL(page.url());
		expect(url.pathname).toBe(nbPath(workspace.id, notebook.id));
		expect(url.searchParams.has("chat")).toBe(false);
		expect(page.url()).not.toContain("new-chat");
	});
});
