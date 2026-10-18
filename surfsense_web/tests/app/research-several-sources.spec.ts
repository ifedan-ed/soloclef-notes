import type { Page, Route } from "@playwright/test";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, openNotebook, test } from "./fixtures";

/**
 * Research box, several sources at once: "Search several at once" in the
 * source menu turns the list into checkboxes (up to four), the search asks
 * every ticked source side by side, and the results arrive merged, each
 * naming the source that found it. The pick is remembered in the browser.
 *
 * As in research.spec.ts, the isolated stack has no internet: the merged list
 * comes from a browser-side SSE stub with the backend's event shape, and the
 * real backend is used for the failure path and the import.
 */

type SearchBody = { query: string; source: string; sources?: string[]; mode: string };

function sse(events: unknown[]): string {
	return events.map((e) => `data: ${JSON.stringify(e)}\n\n`).join("");
}

function row(source: string, n: number) {
	return {
		id: `${source}-${n}`,
		url: `https://example.org/${source}/${n}`,
		title: `E2E ${source} result ${n}`,
		snippet: `Seeded ${source} snippet ${n}.`,
		source,
		paper: null,
	};
}

async function mockSeveralSourceSearch(page: Page, bodies: SearchBody[]) {
	await page.route("**/research/sources", (route: Route) =>
		route.fulfill({ status: 200, json: { unavailable: {} } })
	);
	await page.route("**/research/search", async (route: Route) => {
		const body = route.request().postDataJSON() as SearchBody;
		bodies.push(body);
		const sources = body.sources ?? [body.source];
		// Every source but Reddit answers with two rows; Reddit is busy.
		const answered = sources.filter((s) => s !== "reddit");
		await route.fulfill({
			status: 200,
			headers: { "Content-Type": "text/event-stream" },
			body: sse([
				{ type: "phase", phase: "searching", progress: 5 },
				...sources.map((source) => ({ type: "provider", source, status: "searching" })),
				...sources.map((source) =>
					source === "reddit"
						? {
								type: "provider",
								source,
								status: "error",
								code: "source_busy",
								message: "Reddit didn't answer.",
							}
						: { type: "provider", source, status: "done", count: 2 }
				),
				{ type: "phase", phase: "ranking", progress: 95 },
				{
					type: "results",
					results: [1, 2].flatMap((n) => answered.map((source) => row(source, n))),
				},
				{ type: "done" },
			]),
		});
	});
}

const chip = (page: Page) => page.getByRole("button", { name: /^Search in:/ });

test.describe("Research: several sources at once", () => {
	test("tick several sources, search them together, add from the merged list", async ({
		page,
		workspace,
		notebook,
	}) => {
		const bodies: SearchBody[] = [];
		await mockSeveralSourceSearch(page, bodies);
		await openNotebook(page, workspace.id, notebook.id, "Sources");

		await chip(page).click();
		const several = page.getByRole("menuitemcheckbox", { name: "Search several at once" });
		await expect(several).not.toBeChecked();
		await several.click();
		await expect(several).toBeChecked();
		await expect(page.getByText("Tick up to 4 sources")).toBeVisible();
		await expectNoOverflow(page, "research source menu (several)");

		const item = (name: RegExp) => page.getByRole("menuitemcheckbox", { name });
		await expect(item(/^Web$/)).toBeChecked();
		for (const name of [/^Reddit/, /^PubMed/, /^arXiv/]) await item(name).click();
		// Four ticked: the rest wait until one is unticked.
		await expect(item(/^YouTube/)).toBeDisabled();
		await item(/^arXiv/).click();
		await expect(item(/^arXiv/)).not.toBeChecked();
		await expect(item(/^YouTube/)).toBeEnabled();
		await page.keyboard.press("Escape");

		await expect(chip(page)).toHaveAccessibleName("Search in: Web, Reddit, PubMed");
		await expect(chip(page)).toContainText("3 sources");

		await page.getByRole("textbox", { name: "Search the web for new sources" }).fill("wheezing");
		await page.getByRole("button", { name: "Search", exact: true }).click();
		const results = page.getByRole("region", { name: "Web search results" });
		await expect(results).toBeVisible();
		expect(bodies.at(-1)).toEqual({
			query: "wheezing",
			source: "web",
			sources: ["web", "reddit", "pubmed"],
			mode: "fast",
		});

		await expect(results.getByText("4 sources found")).toBeVisible();
		await expect(results.getByText("From Web")).toHaveCount(2);
		await expect(results.getByText("From PubMed")).toHaveCount(2);
		await expect(results.getByText("Not every source answered:")).toBeVisible();
		await expect(results.getByText(/Reddit didn.t answer just now/)).toBeVisible();
		await expectNoOverflow(page, "research results (several sources)");

		const importRequest = page.waitForRequest(
			(r) => r.url().endsWith("/research/import") && r.method() === "POST"
		);
		await results.getByRole("button", { name: /^Add 4 sources/ }).click();
		const sent = (await importRequest).postDataJSON() as {
			items: { url: string; source: string }[];
		};
		expect(sent.items.map((i) => i.source)).toEqual(["web", "pubmed", "web", "pubmed"]);
	});

	test("the pick is remembered, and one click goes back to a single source", async ({
		page,
		workspace,
		notebook,
	}) => {
		await mockSeveralSourceSearch(page, []);
		await openNotebook(page, workspace.id, notebook.id, "Sources");
		await chip(page).click();
		await page.getByRole("menuitemcheckbox", { name: "Search several at once" }).click();
		await page.getByRole("menuitemcheckbox", { name: /^PubMed/ }).click();
		await page.keyboard.press("Escape");
		await expect(chip(page)).toContainText("2 sources");

		// A fresh visit (the phone layout reopens on another tab, so go back to Sources).
		await openNotebook(page, workspace.id, notebook.id, "Sources");
		await expect(chip(page)).toContainText("2 sources");
		await expect(chip(page)).toHaveAccessibleName("Search in: Web, PubMed");

		// Turning "several" off keeps the first pick, and single-click picking is back.
		await chip(page).click();
		await page.getByRole("menuitemcheckbox", { name: "Search several at once" }).click();
		await expect(page.getByRole("menuitemradio", { name: /^YouTube/ })).toBeVisible();
		await page.getByRole("menuitemradio", { name: /^YouTube/ }).click();
		await expect(chip(page)).toHaveAccessibleName("Search in: YouTube");
	});

	test("a real several-source search with no internet reports each source and ends inline", async ({
		page,
		workspace,
		notebook,
	}) => {
		// Real backend (no stubs): PubMed and arXiv cannot be reached offline.
		await openNotebook(page, workspace.id, notebook.id, "Sources");
		await chip(page).click();
		await page.getByRole("menuitemcheckbox", { name: "Search several at once" }).click();
		await page.getByRole("menuitemcheckbox", { name: /^PubMed/ }).click();
		await page.getByRole("menuitemcheckbox", { name: /^arXiv/ }).click();
		await page.getByRole("menuitemcheckbox", { name: /^Web$/ }).click();
		await page.keyboard.press("Escape");
		await expect(chip(page)).toHaveAccessibleName("Search in: PubMed, arXiv");

		const response = page.waitForResponse(
			(r) => r.url().endsWith("/research/search") && r.request().method() === "POST",
			{ timeout: 120_000 }
		);
		await page.getByRole("textbox", { name: "Search the web for new sources" }).fill("offline");
		await page.getByRole("button", { name: "Search", exact: true }).click();
		const answer = await response;
		expect(answer.request().postDataJSON()).toMatchObject({ sources: ["pubmed", "arxiv"] });
		const stream = await answer.text();
		expect(stream).toMatch(/"type":\s*"provider",\s*"source":\s*"pubmed"/);
		expect(stream).toMatch(/"type":\s*"provider",\s*"source":\s*"arxiv"/);
		const alert = page.getByRole("alert").filter({ hasText: /didn.t finish/ });
		await expect(alert.or(page.getByText("No sources found"))).toBeVisible({ timeout: 30_000 });
		await expectNoOverflow(page, "research error (several sources)");
	});
});
