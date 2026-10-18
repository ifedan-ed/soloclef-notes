import type { Page, Route } from "@playwright/test";
import { listSources } from "../helpers/api/notebooks";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, openNotebook, test } from "./fixtures";

/**
 * Research box (top of the Sources panel): web, Exa, Tavily, PubMed, arXiv,
 * medRxiv and Google Scholar.
 *
 * The isolated stack has no internet, so a real search can only fail; that
 * failure path is tested against the real backend. The result list is tested
 * with a seeded stream: the search POST is answered in the browser with the
 * backend's SSE event shape (phase → results → done). The import that follows
 * goes to the real backend. Exa and Tavily need a key connected in the
 * workspace; the e2e workspace has none, so the real backend greys them out,
 * and the result-list test stubs the availability check as well.
 */

type Result = {
	id: string;
	url: string;
	title: string;
	snippet: string;
	source: string;
	paper?: Record<string, unknown> | null;
};

function sse(events: unknown[]): string {
	return events.map((e) => `data: ${JSON.stringify(e)}\n\n`).join("");
}

function resultsFor(source: string): Result[] {
	const paper =
		source === "web"
			? null
			: {
					pmid: source === "pubmed" ? "12345678" : null,
					arxiv_id: source === "arxiv" ? "2401.00001" : null,
					server: source === "medrxiv" ? "medrxiv" : null,
					authors: ["Ada Lovelace", "Alan Turing"],
					venue: "E2E Journal",
					year: 2025,
					badges: [source === "google_scholar" ? "scholar" : source],
					open_access: true,
				};
	return [1, 2].map((n) => ({
		id: `${source}-${n}`,
		url: `https://example.org/${source}/${n}`,
		title: `E2E ${source} result ${n}`,
		snippet: `Seeded ${source} snippet ${n} about photosynthesis.`,
		source,
		paper,
	}));
}

async function mockSearch(page: Page, bodies: { source: string; query: string; mode: string }[]) {
	// As if Exa and Tavily were connected in this workspace.
	await page.route("**/research/sources", (route: Route) =>
		route.fulfill({ status: 200, json: { unavailable: {} } })
	);
	await page.route("**/research/search", async (route: Route) => {
		const body = route.request().postDataJSON() as { source: string; query: string; mode: string };
		bodies.push(body);
		await route.fulfill({
			status: 200,
			headers: { "Content-Type": "text/event-stream" },
			body: sse([
				{ type: "phase", phase: "searching", progress: 20 },
				{ type: "phase", phase: "ranking", progress: 80 },
				{ type: "results", results: resultsFor(body.source) },
				{ type: "done" },
			]),
		});
	});
}

const SOURCES: [label: string | RegExp, id: string][] = [
	["Web", "web"],
	["Exa", "exa"],
	["Tavily", "tavily"],
	["PubMed", "pubmed"],
	["arXiv", "arxiv"],
	[/medRxiv/, "medrxiv"],
	["Google Scholar", "google_scholar"],
];

test.describe("Research", () => {
	test("each source: pick it, search, see its results", async ({ page, workspace, notebook }) => {
		const bodies: { source: string; query: string; mode: string }[] = [];
		await mockSearch(page, bodies);
		await openNotebook(page, workspace.id, notebook.id, "Sources");
		const query = page.getByRole("textbox", { name: "Search the web for new sources" });
		await expect(query).toBeVisible();

		for (const [label, id] of SOURCES) {
			await page.getByRole("button", { name: /^Search in:/ }).click();
			await expectNoOverflow(page, `research source menu (${id})`);
			await page.getByRole("menuitemradio", { name: label }).first().click();
			await expect(page.getByRole("button", { name: /^Search in:/ })).toContainText(
				typeof label === "string" ? label : "medRxiv"
			);
			await query.fill(`photosynthesis ${id}`);
			await page.getByRole("button", { name: "Search", exact: true }).click();
			const results = page.getByRole("region", { name: "Web search results" });
			await expect(results).toBeVisible();
			await expect(results.getByText("2 sources found")).toBeVisible();
			await expect(
				results.getByRole("link", { name: new RegExp(`E2E ${id} result 1`) })
			).toBeVisible();
			await expectNoOverflow(page, `research results (${id})`);
			expect(bodies.at(-1)).toMatchObject({
				source: id,
				query: `photosynthesis ${id}`,
				mode: "fast",
			});
			await results.getByRole("button", { name: "Dismiss" }).click();
			await expect(results).toBeHidden();
		}
	});

	test("Exa and Tavily are greyed out when the workspace has no key for them", async ({
		page,
		workspace,
		notebook,
	}) => {
		// Real backend: the e2e workspace has no Exa or Tavily connector.
		const status = page.waitForResponse(
			(r) => r.url().endsWith("/research/sources") && r.request().method() === "GET"
		);
		await openNotebook(page, workspace.id, notebook.id, "Sources");
		// page_reader: the e2e worker stands in a connected Browserbase.
		expect(await (await status).json()).toEqual({
			unavailable: { exa: "not_connected", tavily: "not_connected" },
			page_reader: "browserbase",
		});
		await page.getByRole("button", { name: /^Search in:/ }).click();
		for (const name of ["Exa", "Tavily"]) {
			const item = page.getByRole("menuitemradio", { name });
			await expect(item).toBeDisabled();
			await expect(item).toContainText("Not connected");
		}
		await expect(page.getByText("To search with Exa or Tavily")).toBeVisible();
		await expect(page.getByRole("menuitemradio", { name: /^Web/ }).first()).toBeEnabled();
		await expectNoOverflow(page, "research source menu (unavailable)");
	});

	test("deep research mode is sent with the request", async ({ page, workspace, notebook }) => {
		const bodies: { source: string; query: string; mode: string }[] = [];
		await mockSearch(page, bodies);
		await openNotebook(page, workspace.id, notebook.id, "Sources");
		await page.getByRole("button", { name: /^Research mode:/ }).click();
		await expectNoOverflow(page, "research mode menu");
		await page.getByRole("menuitemradio", { name: /Deep research/ }).click();
		await page.getByRole("textbox", { name: "Search the web for new sources" }).fill("deep query");
		await page.getByRole("button", { name: "Search", exact: true }).click();
		await expect(page.getByRole("region", { name: "Web search results" })).toBeVisible();
		expect(bodies.at(-1)).toMatchObject({ mode: "deep", query: "deep query" });
	});

	test("select results and import them into the notebook", async ({
		page,
		workspace,
		notebook,
		request,
		token,
	}) => {
		await mockSearch(page, []);
		await openNotebook(page, workspace.id, notebook.id, "Sources");
		await page
			.getByRole("textbox", { name: "Search the web for new sources" })
			.fill("photosynthesis");
		await page.getByRole("button", { name: "Search", exact: true }).click();
		const results = page.getByRole("region", { name: "Web search results" });
		// Every result starts selected; untick one, then Select all again.
		const all = results.getByRole("checkbox", { name: "Select all" });
		await expect(all).toBeChecked();
		await results.getByRole("checkbox", { name: "Select E2E web result 2" }).click();
		await expect(all).toHaveAttribute("aria-checked", "mixed");
		await expect(results.getByRole("button", { name: /^Add 1 source/ })).toBeEnabled();
		await all.click();
		await expect(all).toBeChecked();
		const importRequest = page.waitForRequest(
			(r) => r.url().endsWith("/research/import") && r.method() === "POST"
		);
		await results.getByRole("button", { name: /^Add 2 sources/ }).click();
		const sent = (await importRequest).postDataJSON() as {
			items: { url: string; title: string }[];
		};
		expect(sent.items.map((i) => i.url)).toEqual([
			"https://example.org/web/1",
			"https://example.org/web/2",
		]);
		// The backend adds the pages as sources (their reading fails offline,
		// which is the backend's business; the rows exist).
		await expect
			.poll(async () => (await listSources(request, token, workspace.id, notebook.id)).length, {
				timeout: 30_000,
			})
			.toBeGreaterThanOrEqual(1);
	});

	test("a real search with no internet ends inline (error or no results), no popup", async ({
		page,
		workspace,
		notebook,
	}) => {
		// Real backend, no internet: the search fails.
		await openNotebook(page, workspace.id, notebook.id, "Sources");
		await page
			.getByRole("textbox", { name: "Search the web for new sources" })
			.fill("offline search");
		await page.getByRole("button", { name: "Search", exact: true }).click();
		const alert = page.getByRole("alert").filter({ hasText: /didn.t finish|No sources found/ });
		await expect(alert.or(page.getByText("No sources found"))).toBeVisible({ timeout: 45_000 });
		await expectNoOverflow(page, "research error");
	});
});
