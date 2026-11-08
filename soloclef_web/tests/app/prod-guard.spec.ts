import { listNotebooks } from "../helpers/api/notebooks";
import { installReadOnlyGuard, READ_ONLY_POSTS } from "../prod-smoke/guard";
import { expect, test } from "./fixtures";

/**
 * The prod-smoke read-only guard, proven on the isolated stack: writes from
 * the page are aborted before they leave the browser and change nothing.
 */
test.describe("prod-smoke guard", () => {
	test("aborts POST/PATCH/DELETE from the page; reads pass", async ({
		page,
		context,
		workspace,
		notebook,
		request,
		token,
	}) => {
		const blocked = await installReadOnlyGuard(context);
		await page.goto(`/dashboard/${workspace.id}/notebooks`);
		const results = await page.evaluate(
			async ({ ws, nb }) => {
				const attempt = async (method: string, url: string, body?: unknown) => {
					try {
						const r = await fetch(url, {
							method,
							credentials: "include",
							headers: { "Content-Type": "application/json" },
							body: body ? JSON.stringify(body) : undefined,
						});
						return r.status;
					} catch {
						return "aborted";
					}
				};
				return {
					create: await attempt("POST", `/api/v1/workspaces/${ws}/notebooks`, {
						title: "must not exist",
					}),
					rename: await attempt("PATCH", `/api/v1/workspaces/${ws}/notebooks/${nb}`, {
						title: "renamed",
					}),
					remove: await attempt("DELETE", `/api/v1/workspaces/${ws}/notebooks/${nb}`),
					read: await attempt("GET", `/api/v1/workspaces/${ws}/notebooks`),
				};
			},
			{ ws: workspace.id, nb: notebook.id }
		);
		expect(results).toEqual({ create: "aborted", rename: "aborted", remove: "aborted", read: 200 });
		expect(blocked.map((b) => b.method).sort()).toEqual(["DELETE", "PATCH", "POST"]);
		const rows = await listNotebooks(request, token, workspace.id);
		expect(rows.map((r) => r.title)).toEqual(["E2E notebook"]);
		expect(READ_ONLY_POSTS.length).toBeLessThanOrEqual(5);
	});
});
