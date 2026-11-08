import fs from "node:fs";
import path from "node:path";
import type { Locator, Page } from "@playwright/test";
import { STEP_ANNOUNCE_SETTLE_MS } from "../../features/chat-messages/timeline/settled-announcement";
import { addNoteSource } from "../helpers/api/notebooks";
import { assistantMessages, send } from "../helpers/ui/chat";
import { stubChatStream } from "../helpers/ui/chat-stream-stub";
import {
	ACCOUNTS,
	ANSWER,
	HEAD,
	QUESTION,
	SEARCHES,
	savedRows,
	search,
	stepEnds,
	stepStarts,
	TAIL,
	TASK,
} from "../helpers/ui/email-search-turn";
import { expect, openNotebook, test } from "./fixtures";

/**
 * An answer's steps are one line that changes as the answer works (the step
 * it is on, with its icon, next to the clock), not a list that grows a row
 * per step; the list opens only from that line, live or reloaded.
 *
 * The turn is the one the owner asked about (tests/helpers/ui/email-search-turn.ts),
 * streamed through the browser-side stub (tests/helpers/ui/chat-stream-stub.ts),
 * so the page can be looked at mid-answer.
 * SHOTS_DIR=<dir> also saves a screenshot at each stage.
 */

async function shot(page: Page, name: string, project: string) {
	const dir = process.env.SHOTS_DIR;
	if (!dir) return;
	fs.mkdirSync(dir, { recursive: true });
	await page.screenshot({ path: path.join(dir, `${project}-${name}.png`) });
}

/**
 * The answer's one line: the header's button (by tag: the phone's Summary
 * sheet hides the page behind it from the accessibility tree).
 */
const lineOf = (answer: Locator) =>
	answer.getByTestId("assistant-turn-header").locator("button").first();

/** Whether the step list is out: inline (desktop) or in the Summary sheet (phone). */
async function listShown(page: Page, answer: Locator, isPhone: boolean): Promise<boolean> {
	if (isPhone) return page.getByRole("dialog", { name: "Summary" }).isVisible();
	const id = await lineOf(answer).getAttribute("aria-controls");
	if (!id) return false;
	const height = await page
		.locator(`[id="${id}"]`)
		.evaluate((el) => el.getBoundingClientRect().height);
	return height > 1;
}

/**
 * The closed list is out of a screen reader's reach: the header reads as its
 * line (and, while the answer works, the step said), nothing of the list.
 */
async function expectListOutOfReach(answer: Locator) {
	const tree = await answer.getByTestId("assistant-turn-header").ariaSnapshot();
	expect(tree).not.toContain("Provider reasoning");
	expect(tree).not.toContain("Listed email accounts");
	const roles = tree
		.split("\n")
		.filter((line) => line.startsWith("- "))
		.map((line) => line.slice(2).split(/[\s:]/)[0]);
	expect(roles.length).toBeGreaterThan(0);
	for (const role of roles) expect(["button", "status"]).toContain(role);
}

/** Whether Tab from the line lands in the inline list (its reasoning box takes focus). */
async function tabFromLineLandsInList(page: Page, answer: Locator): Promise<boolean> {
	await lineOf(answer).focus();
	await page.keyboard.press("Tab");
	const id = await lineOf(answer).getAttribute("aria-controls");
	return page.evaluate(
		(listId) => document.getElementById(listId ?? "")?.contains(document.activeElement) ?? false,
		id
	);
}

async function expectOneLine(page: Page, answer: Locator, isPhone: boolean, title: string) {
	await expect(answer.getByTestId("assistant-turn-header")).toHaveCount(1);
	await expect(lineOf(answer)).toContainText(title);
	await expect(lineOf(answer).getByTestId("assistant-turn-step-icon")).toBeVisible();
	await expect(lineOf(answer)).toHaveAttribute("aria-expanded", "false");
	await expect.poll(() => listShown(page, answer, isPhone)).toBe(false);
	await expectListOutOfReach(answer);
}

/** The step list, opened from the line. */
async function openList(page: Page, answer: Locator, isPhone: boolean): Promise<Locator> {
	await lineOf(answer).click();
	await expect(lineOf(answer)).toHaveAttribute("aria-expanded", "true");
	await expect.poll(() => listShown(page, answer, isPhone)).toBe(true);
	if (isPhone) return page.getByRole("dialog", { name: "Summary" });
	const id = await lineOf(answer).getAttribute("aria-controls");
	return page.locator(`[id="${id}"]`);
}

async function closeList(page: Page, answer: Locator, isPhone: boolean) {
	if (isPhone) await page.keyboard.press("Escape");
	else await lineOf(answer).click();
	await expect.poll(() => listShown(page, answer, isPhone)).toBe(false);
}

test("an answer's steps are one line that changes as it works; the list opens only from the line", async ({
	page,
	request,
	token,
	workspace,
	notebook,
	isPhone,
}, testInfo) => {
	const project = testInfo.project.name;
	await addNoteSource(request, token, workspace.id, notebook.id, "Inbox notes", "Mail notes.");
	const stream = await stubChatStream(page);
	await openNotebook(page, workspace.id, notebook.id, "Chat");
	await send(page, QUESTION);
	await stream.whenConnected();
	const answer = assistantMessages(page).last();

	// Thinking: the line is the reasoning step.
	await stream.push(HEAD);
	await expect(lineOf(answer)).toContainText("Reasoning");
	await shot(page, "01-thinking", project);
	await expectOneLine(page, answer, isPhone, "Reasoning");

	// Tool steps: each one replaces the last on the same line.
	await stream.push([...TASK, ...stepStarts(ACCOUNTS, 1)]);
	await expectOneLine(page, answer, isPhone, "Listing email accounts");
	await stream.push(stepEnds(ACCOUNTS, 1));
	for (let n = 0; n < 3; n += 1) {
		await stream.push([...stepStarts(search(n), n + 2), ...stepEnds(search(n), n + 2)]);
	}
	await stream.push(stepStarts(search(3), 5));
	await expect(lineOf(answer)).toContainText("Searching email");
	await shot(page, "02-working", project);
	await expectOneLine(page, answer, isPhone, "Searching email");
	// On a computer, Tab passes the closed list by (its reasoning box took focus unseen).
	if (!isPhone) expect(await tabFromLineLandsInList(page, answer)).toBe(false);

	// The list is there on demand, mid-answer.
	const live = await openList(page, answer, isPhone);
	await expect(live.getByText("Listed email accounts", { exact: true })).toBeVisible();
	await expect(live.getByText("Searched email", { exact: true })).toHaveCount(3);
	await shot(page, "03-opened-while-working", project);
	if (!isPhone) expect(await tabFromLineLandsInList(page, answer)).toBe(true);
	await closeList(page, answer, isPhone);

	// Finished: the line keeps the last step.
	await stream.push(stepEnds(search(3), 5));
	for (let n = 4; n < SEARCHES; n += 1) {
		await stream.push([...stepStarts(search(n), n + 2), ...stepEnds(search(n), n + 2)]);
	}
	await stream.push(TAIL);
	await stream.close();
	await expect(answer).toContainText(ANSWER);
	await expect(page.getByRole("button", { name: "Send message" })).toBeVisible();
	await shot(page, "04-finished", project);
	await expectOneLine(page, answer, isPhone, "Searched email");

	// Reloaded from history: still one line, the list closed until opened.
	const threadId = Number((await stream.requestBody()).chat_id);
	expect(threadId).toBeGreaterThan(0);
	await page.route(new RegExp(`/api/v1/threads/${threadId}(\\?.*)?$`), async (route) => {
		if (route.request().method() !== "GET") return route.continue();
		const real = await route.fetch();
		const body = (await real.json()) as { messages: unknown[] };
		await route.fulfill({ response: real, json: { ...body, messages: savedRows(threadId) } });
	});
	await openNotebook(page, workspace.id, notebook.id, "Chat");
	const reloaded = assistantMessages(page).last();
	await expect(reloaded).toContainText(ANSWER);
	await shot(page, "05-reloaded", project);
	await expectOneLine(page, reloaded, isPhone, "Searched email");
	if (!isPhone) expect(await tabFromLineLandsInList(page, reloaded)).toBe(false);
	const history = await openList(page, reloaded, isPhone);
	await expect(history.getByText("Listed email accounts", { exact: true })).toBeVisible();
	await expect(history.getByText("Searched email", { exact: true })).toHaveCount(SEARCHES);
	await expect(history.getByText("Reasoning", { exact: true })).toBeVisible();
	await shot(page, "06-reloaded-opened", project);
});

test("a screen reader hears the step the line settles on, once, and never the clock", async ({
	page,
	request,
	token,
	workspace,
	notebook,
}) => {
	await addNoteSource(request, token, workspace.id, notebook.id, "Inbox notes", "Mail notes.");
	const stream = await stubChatStream(page);
	await openNotebook(page, workspace.id, notebook.id, "Chat");
	await send(page, QUESTION);
	await stream.whenConnected();
	const answer = assistantMessages(page).last();

	// Every text the line's polite status region takes, in order.
	await page.evaluate(() => {
		const said: string[] = [];
		(window as unknown as { __said: string[] }).__said = said;
		let last = "";
		const read = () => {
			const text = [...document.querySelectorAll('[data-testid="assistant-turn-step-status"]')]
				.map((el) => el.textContent ?? "")
				.join("|");
			if (text === last) return;
			last = text;
			if (text) said.push(text);
		};
		new MutationObserver(read).observe(document.body, {
			subtree: true,
			childList: true,
			characterData: true,
		});
		read();
	});
	// The wait before the first step may be said or not ("Spellweaving"), as fast as the stub is.
	const heard = () =>
		page.evaluate(() =>
			(window as unknown as { __said: string[] }).__said.filter((text) => text !== "Spellweaving")
		);
	const settle = () => page.waitForTimeout(STEP_ANNOUNCE_SETTLE_MS + 700);

	await stream.push(HEAD);
	await expect.poll(heard).toEqual(["Reasoning"]);

	// A burst of steps at once: only the step the line stays on is said, without the clock.
	await stream.push([
		...TASK,
		...stepStarts(ACCOUNTS, 1),
		...stepEnds(ACCOUNTS, 1),
		...[0, 1, 2].flatMap((n) => [...stepStarts(search(n), n + 2), ...stepEnds(search(n), n + 2)]),
		...stepStarts(search(3), 5),
	]);
	await expect(lineOf(answer)).toContainText("Searching email");
	const status = answer.getByTestId("assistant-turn-step-status");
	await expect(status).toHaveText("Searching email");
	await expect(lineOf(answer)).toContainText(/\d(\.\d)?s/);
	await settle();
	expect(await heard()).toEqual(["Reasoning", "Searching email"]);

	// Flipping to "Searched email" and back to the step already said says nothing new.
	await stream.push([
		...stepEnds(search(3), 5),
		...stepStarts(search(4), 6),
		...stepEnds(search(4), 6),
		...stepStarts(search(5), 7),
	]);
	await settle();
	expect(await heard()).toEqual(["Reasoning", "Searching email"]);

	// Done: the region goes with the work.
	await stream.push([...stepEnds(search(5), 7), ...TAIL]);
	await stream.close();
	await expect(answer).toContainText(ANSWER);
	await expect(status).toHaveCount(0);
	expect(await heard()).toEqual(["Reasoning", "Searching email"]);
});
