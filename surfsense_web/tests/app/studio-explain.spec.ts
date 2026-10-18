import type { Locator, Page, Request } from "@playwright/test";
import {
	addNoteSource,
	completeOutput,
	listStudioOutputs,
	type SeedKind,
	seedOutput,
} from "../helpers/api/notebooks";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { assistantMessages, composer, userMessages } from "../helpers/ui/chat";
import { expect, openNotebook, test, waitForChatReady } from "./fixtures";

/**
 * Studio outputs as NotebookLM shows them (ui/studio/output-view.tsx):
 *
 * - desktop: an output opens in the widened Studio column with the chat
 *   usable beside it, or expanded to the whole window (reports open
 *   expanded); "Explain" sends a prefilled message to the notebook chat and,
 *   from an expanded output, drops it back into the column at the same place;
 * - phone: an output fills the screen; Explain switches to the Chat tab and
 *   "Back to <title>" returns to the output where it was.
 *
 * The fake chat model answers any Explain message with EXPLAIN_ANSWER
 * (surfsense_backend/tests/e2e/fakes/chat_llm.py).
 */

const EXPLAIN_ANSWER = "E2E explain answer";

const row = (page: Page, title: string) =>
	page.getByRole("button", { name: new RegExp(`^${title}`) }).first();
const view = (page: Page) => page.locator("[data-output-view]");

async function openOutput(page: Page, ws: number, nb: number, title: string) {
	await openNotebook(page, ws, nb, "Studio");
	const button = row(page, title);
	await expect(button).toBeEnabled({ timeout: 30_000 });
	await button.click();
	await expect(view(page)).toBeVisible();
}

/** The next chat send, and the message text it carried. */
function nextChatSend(page: Page): Promise<Request> {
	return page.waitForRequest((r) => r.url().includes("/new_chat") && r.method() === "POST");
}

/** The request body contains ``message`` exactly (JSON-escaped: quotes, newlines). */
function carries(request: Request, message: string): boolean {
	return (request.postData() ?? "").includes(JSON.stringify(message).slice(1, -1));
}

async function expectAnswerBeside(page: Page, message: string) {
	await expect(userMessages(page).filter({ hasText: message.split("\n")[0] })).toBeVisible();
	await expect(assistantMessages(page).last()).toContainText(EXPLAIN_ANSWER, { timeout: 45_000 });
}

async function expectChatTab(page: Page) {
	await expect(page.getByRole("tab", { name: /^Chat/ })).toHaveAttribute("aria-selected", "true");
	await expect(view(page)).toBeHidden();
}

/** A phone: back from the Chat tab to the output, where it was. */
async function backToOutput(page: Page, title: string) {
	await page.getByRole("button", { name: `Back to ${title}` }).click();
	await expect(view(page)).toBeVisible();
}

/** The Studio column is wide, the chat beside it visible and usable. */
async function expectColumnBesideChat(page: Page) {
	await expect(view(page)).toHaveAttribute("data-output-size", "column");
	const box = await view(page).boundingBox();
	const viewport = page.viewportSize();
	expect(box && viewport).toBeTruthy();
	if (!box || !viewport) return;
	expect(box.width).toBeGreaterThan(420);
	expect(box.width).toBeLessThan(viewport.width * 0.62);
	await expect(composer(page)).toBeInViewport({ timeout: 30_000 });
	const chat = await composer(page).boundingBox();
	expect(chat && chat.x + chat.width <= box.x + 1).toBe(true);
	// No backdrop over the notebook: the composer takes a click.
	await composer(page).click();
}

async function expectExpanded(page: Page) {
	await expect(view(page)).toHaveAttribute("data-output-size", "expanded");
	const box = await view(page).boundingBox();
	const viewport = page.viewportSize();
	expect(box && viewport && box.width > viewport.width * 0.9).toBe(true);
}

test.describe("Explain from flashcards and quizzes", () => {
	test("a quiz opens beside the chat; answered, every option explains itself; Explain asks the chat", async ({
		page,
		workspace,
		notebook,
		request,
		isPhone,
	}) => {
		const title = "E2E explain quiz";
		await seedOutput(request, workspace.id, notebook.id, "quiz", "ready", title);
		await openOutput(page, workspace.id, notebook.id, title);
		const v = view(page);
		await expect(v.getByRole("heading", { name: "E2E quiz question 1?" })).toBeVisible({
			timeout: 30_000,
		});
		await expect(v.getByRole("navigation", { name: "Studio" })).toContainText("Quiz");
		if (isPhone) await expect(v).toHaveAttribute("data-output-size", "screen");
		else await expectColumnBesideChat(page);
		await expectNoOverflow(page, "quiz in the output view");

		// The prompt and sources chip.
		await v.getByRole("button", { name: /^View prompt and/ }).click();
		await expect(v.getByText("E2E focus: the seeded questions")).toBeVisible();
		await v.getByRole("button", { name: /^View prompt and/ }).click();

		// No Explain before answering; a wrong answer shows every rationale.
		await expect(v.getByRole("button", { name: "Explain this question in the chat" })).toHaveCount(
			0
		);
		await v.getByRole("radio", { name: /Option 1C/ }).click();
		await expect(v.getByText("Not quite")).toBeVisible();
		for (const letter of ["A", "B", "C", "D"]) {
			await expect(
				v.locator(`[data-quiz-rationale]`).filter({ hasText: `Rationale 1${letter}` })
			).toBeVisible();
		}
		await expect(v.locator('[data-quiz-rationale="1"]')).toContainText("Correct answer");
		await expectNoOverflow(page, "quiz rationales");

		const message =
			'I am taking a quiz on this material and was given this question: "E2E quiz question 1?"\n\n' +
			'I chose "Option 1C", but the correct answer is "Option 1B"\n\n' +
			"Help me understand more about this topic.";
		const sent = nextChatSend(page);
		await v.getByRole("button", { name: "Explain this question in the chat" }).click();
		expect(carries(await sent, message)).toBe(true);

		if (isPhone) {
			await expectChatTab(page);
			await expectAnswerBeside(page, message);
			await expectNoOverflow(page, "chat after Explain (phone)");
			await backToOutput(page, title);
		} else {
			// The answer streams in the chat while the quiz stays open beside it.
			await expect(v.getByRole("heading", { name: "E2E quiz question 1?" })).toBeVisible();
			await expectAnswerBeside(page, message);
			await expectColumnBesideChat(page);
		}
		// Where it was: question 1, answered, rationales shown.
		await expect(v.getByRole("heading", { name: "E2E quiz question 1?" })).toBeVisible();
		await expect(v.getByText("Rationale 1B: this is the one.")).toBeVisible();

		// Previous / Next; an old-style question shows its one explanation.
		await v.getByRole("button", { name: "Next" }).click();
		await expect(v.getByRole("heading", { name: "E2E quiz question 2?" })).toBeVisible();
		await v.getByRole("button", { name: "Previous" }).click();
		await expect(v.getByRole("heading", { name: "E2E quiz question 1?" })).toBeVisible();
		await expect(v.getByText("Not quite")).toBeVisible();
		await v.getByRole("button", { name: "Next" }).click();
		for (const n of [2, 3]) {
			await expect(v.getByRole("heading", { name: `E2E quiz question ${n}?` })).toBeVisible();
			await v.getByRole("button", { name: "Skip" }).click();
		}
		await expect(v.getByRole("heading", { name: "E2E quiz question 4?" })).toBeVisible();
		await v.getByRole("radio", { name: /Option 4B/ }).click();
		await expect(v.getByText("That's right!")).toBeVisible();
		await expect(v.getByText("Option 4B is right because the seed says so.")).toBeVisible();
		await expect(v.locator("[data-quiz-rationale]")).toHaveCount(1);

		// The Good / Bad content rating is gone.
		await expect(v.getByRole("button", { name: "Good content" })).toHaveCount(0);
		await expect(v.getByRole("button", { name: "Bad content" })).toHaveCount(0);
		await expectNoOverflow(page, "quiz after answering");
	});

	test("a flashcard's Explain sends NotebookLM's message; the deck keeps its place", async ({
		page,
		workspace,
		notebook,
		request,
		isPhone,
	}) => {
		const title = "E2E explain cards";
		await seedOutput(request, workspace.id, notebook.id, "flashcards", "ready", title);
		await openOutput(page, workspace.id, notebook.id, title);
		const v = view(page);
		await expect(v.getByText("E2E question 1")).toBeVisible({ timeout: 30_000 });
		await v.getByRole("button", { name: "Next card" }).click();
		await expect(v.getByText("E2E question 2")).toBeVisible();

		const message =
			"I'm reviewing flashcards based on the source material and I'd like to expand my understanding of one of them.\n\n" +
			'On the front it reads: "E2E question 2"\n' +
			'The answer on the back reads: "E2E answer 2"\n\n' +
			"Explain this topic in more detail.";
		const sent = nextChatSend(page);
		await v.getByRole("button", { name: "Explain this card in the chat" }).click();
		expect(carries(await sent, message)).toBe(true);
		if (isPhone) {
			await expectChatTab(page);
			await expectAnswerBeside(page, message);
			await backToOutput(page, title);
		} else {
			await expectAnswerBeside(page, message);
			await expectColumnBesideChat(page);
		}
		await expect(v.getByText("E2E question 2")).toBeVisible();
		await expectNoOverflow(page, "flashcards after Explain");
	});
});

test.describe("Output sizes", () => {
	test("every kind of output toggles between the column and the whole window", async ({
		page,
		workspace,
		notebook,
		request,
		isPhone,
	}) => {
		test.skip(isPhone, "a phone shows every output full screen");
		const kinds: SeedKind[] = [
			"quiz",
			"flashcards",
			"mindmap",
			"slides",
			"data_table",
			"infographic",
			"video",
			"report",
		];
		for (const kind of kinds) {
			await seedOutput(request, workspace.id, notebook.id, kind, "ready", `E2E size ${kind}`);
		}
		await openNotebook(page, workspace.id, notebook.id, "Studio");
		for (const kind of kinds) {
			const title = `E2E size ${kind}`;
			await expect(row(page, title)).toBeEnabled({ timeout: 30_000 });
			await row(page, title).click();
			const v = view(page);
			await expect(v).toHaveAttribute("data-output-view", kind === "report" ? "report" : kind);
			const toggle = v.getByTestId("output-size-toggle");
			if (kind === "report") {
				// Reports open expanded the first time.
				await expectExpanded(page);
				await toggle.click();
				await expectColumnBesideChat(page);
			} else {
				await expectColumnBesideChat(page);
				await toggle.click();
				await expectExpanded(page);
				await expectNoOverflow(page, `${kind} expanded`);
				await toggle.click();
				await expect(v).toHaveAttribute("data-output-size", "column");
			}
			await expectNoOverflow(page, `${kind} in the column`);
			await v.getByRole("button", { name: "Close", exact: true }).click();
			await expect(v).toHaveCount(0);
		}
		// The choice is remembered for the session: the report reopens in the column.
		await row(page, "E2E size report").click();
		await expect(view(page)).toHaveAttribute("data-output-size", "column");
		// Escape leaves the whole window.
		await view(page).getByTestId("output-size-toggle").click();
		await expectExpanded(page);
		await page.keyboard.press("Escape");
		await expect(view(page)).toHaveAttribute("data-output-size", "column");
	});

	test("Explain from an expanded quiz drops it into the column beside the answer", async ({
		page,
		workspace,
		notebook,
		request,
		isPhone,
	}) => {
		test.skip(isPhone, "a phone has no expanded size");
		const title = "E2E expanded quiz";
		await seedOutput(request, workspace.id, notebook.id, "quiz", "ready", title);
		await openOutput(page, workspace.id, notebook.id, title);
		const v = view(page);
		await v.getByTestId("output-size-toggle").click();
		await expectExpanded(page);
		await v.getByRole("radio", { name: /Option 1B/ }).click();
		await expect(v.getByText("That's right!")).toBeVisible();
		const message =
			'I am taking a quiz on this material and was given this question: "E2E quiz question 1?"\n\n' +
			'I chose the correct answer: "Option 1B"\n\n' +
			"Help me understand more about this topic.";
		const sent = nextChatSend(page);
		await v.getByRole("button", { name: "Explain this question in the chat" }).click();
		expect(carries(await sent, message)).toBe(true);
		await expectColumnBesideChat(page);
		await expect(v.getByText("That's right!")).toBeVisible();
		await expectAnswerBeside(page, message);
	});
});

function paragraphs(topic: string): string {
	return Array.from(
		{ length: 6 },
		(_, i) =>
			`Paragraph ${i + 1} on ${topic}. ${"The unit runs on routine, clear handovers and early escalation. ".repeat(5)}`
	).join("\n\n");
}

const REPORT = `# E2E explain report

A short welcome for new residents.

## 1. Rounds

${paragraphs("rounds")}

## 2. Escalation

${paragraphs("escalation")}

## 3. Summary

${paragraphs("the summary")}
`;

async function scrollToSection(v: Locator, index: number) {
	await v
		.locator(`[data-report-section="${index}"]`)
		.evaluate((el) => el.scrollIntoView({ block: "start" }));
}

test.describe("Explain from a report", () => {
	test("a selected passage: the report opens expanded, Explain drops it into the column at the same section", async ({
		page,
		workspace,
		notebook,
		request,
		isPhone,
	}) => {
		const title = "E2E explain passage report";
		await seedOutput(request, workspace.id, notebook.id, "report", "ready", title, {
			markdown: REPORT,
		});
		await openOutput(page, workspace.id, notebook.id, title);
		const v = view(page);
		await expect(v.getByRole("heading", { name: "E2E explain report", level: 1 })).toBeVisible({
			timeout: 30_000,
		});
		if (!isPhone) await expectExpanded(page);
		// Select once the chat has loaded: its composer takes focus (and the
		// selection) when it first appears.
		await expect(composer(page)).toBeAttached({ timeout: 30_000 });
		if (!isPhone) await waitForChatReady(page);
		await scrollToSection(v, 1);
		const passage = v.locator('[data-report-section="1"] p').nth(1);
		const explain = v.getByRole("button", { name: "Explain the selected text in the chat" });
		// (Retried: the chat's composer can take the selection as it first mounts.)
		await expect(async () => {
			await passage.evaluate((el) => {
				const range = document.createRange();
				range.selectNodeContents(el);
				const selection = window.getSelection();
				selection?.removeAllRanges();
				selection?.addRange(range);
			});
			await expect(explain).toBeVisible({ timeout: 1_000 });
		}).toPass({ timeout: 20_000 });
		const text = ((await passage.textContent()) ?? "").replace(/\s+/g, " ").trim();
		const message = `I'm reading the report "E2E explain report". This part says: "${text}"\n\nExplain this in more detail.`;
		const sent = nextChatSend(page);
		await explain.click();
		expect(carries(await sent, message)).toBe(true);

		if (isPhone) {
			await expectChatTab(page);
			await expectAnswerBeside(page, message);
			await backToOutput(page, title);
		} else {
			await expectColumnBesideChat(page);
			await expectAnswerBeside(page, message);
		}
		await expect(v.getByRole("heading", { name: "2. Escalation" })).toBeInViewport();
		await expectNoOverflow(page, "report after Explain");
	});

	test("the report's embedded quiz: Explain keeps the report open at the same place", async ({
		page,
		workspace,
		notebook,
		request,
		token,
		isPhone,
	}) => {
		await addNoteSource(
			request,
			token,
			workspace.id,
			notebook.id,
			"E2E handbook",
			"E2E handbook text about rounds, escalation and handovers."
		);
		const title = "E2E explain embed report";
		await seedOutput(request, workspace.id, notebook.id, "report", "ready", title, {
			markdown: REPORT,
		});
		await openOutput(page, workspace.id, notebook.id, title);
		const v = view(page);
		const add = v.getByRole("button", { name: "Add Quiz: E2E quiz: escalation" });
		await expect(add).toBeVisible({ timeout: 30_000 });
		await add.click();
		let outputId = 0;
		await expect
			.poll(async () => {
				const list = await listStudioOutputs(request, token, workspace.id, notebook.id);
				const added = list.find((o) => o.title === "E2E quiz: escalation");
				outputId = added?.id ?? 0;
				return added?.status;
			})
			.toBe("queued");
		await completeOutput(request, workspace.id, notebook.id, outputId);
		const embed = v.getByRole("region", { name: "Quiz: E2E quiz: escalation" });
		await expect(embed.getByRole("heading", { name: "E2E quiz question 1?" })).toBeVisible({
			timeout: 30_000,
		});
		await embed.scrollIntoViewIfNeeded();
		await embed.getByRole("radio", { name: /Option 1A/ }).click();
		await expect(embed.getByText("Not quite")).toBeVisible();

		const message =
			'I am taking a quiz on this material and was given this question: "E2E quiz question 1?"\n\n' +
			'I chose "Option 1A", but the correct answer is "Option 1B"\n\n' +
			"Help me understand more about this topic.";
		const sent = nextChatSend(page);
		await embed.getByRole("button", { name: "Explain this question in the chat" }).click();
		expect(carries(await sent, message)).toBe(true);

		if (isPhone) {
			await expectChatTab(page);
			await expectAnswerBeside(page, message);
			await expectNoOverflow(page, "chat after Explain from a report (phone)");
			await backToOutput(page, title);
		} else {
			// Not closed: back in the column, beside the answer.
			await expectColumnBesideChat(page);
			await expectAnswerBeside(page, message);
		}
		// The same section and the same embedded card, still answered.
		// (The quiz scrolls inside its own card; its answered option shows.)
		await expect(embed).toBeInViewport({ ratio: 0.5 });
		await expect(embed.getByText("Not quite")).toBeInViewport();
		await expect(embed.getByRole("heading", { name: "E2E quiz question 1?" })).toBeAttached();
		await expectNoOverflow(page, "report after Explain from its quiz");
	});
});
