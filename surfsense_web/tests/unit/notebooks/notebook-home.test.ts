import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { NextIntlClientProvider } from "next-intl";
import { type ComponentProps, createElement, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { homeSections, RECENT_LIMIT } from "@/features/notebooks/lib/home-filter";
import { notebookCover } from "@/features/notebooks/lib/notebook-cover";
import { formatNotebookDate } from "@/features/notebooks/lib/notebook-dates";
import type { Notebook } from "@/features/notebooks/model/notebook";
import { NotebookCard } from "@/features/notebooks/ui/home/notebook-card";

const messages = JSON.parse(readFileSync(join(process.cwd(), "messages", "en.json"), "utf8"));

function render(element: ReactElement): string {
	const props: ComponentProps<typeof NextIntlClientProvider> = {
		locale: "en",
		messages,
		timeZone: "UTC",
		children: element,
	};
	return renderToStaticMarkup(createElement(NextIntlClientProvider, props));
}

function notebook(id: number, overrides: Partial<Notebook> = {}): Notebook {
	return {
		id,
		workspace_id: 1,
		title: `Notebook ${id}`,
		icon: "📚",
		created_at: "2026-09-01T10:00:00Z",
		updated_at: `2026-09-${String(id).padStart(2, "0")}T10:00:00Z`,
		source_count: 1,
		enabled_source_count: 1,
		chat_goal: "default",
		chat_instructions: null,
		response_length: "default",
		featured: false,
		title_source: "user",
		title_pending: false,
		...overrides,
	};
}

const noop = async () => undefined;
const actions = { onRename: noop, onDelete: noop, onToggleFeatured: noop, onCopy: noop };

test("All shows featured notebooks and every notebook newest first", () => {
	const list = [notebook(1), notebook(3, { featured: true }), notebook(2)];
	const sections = homeSections(list, "all", "");
	assert.deepEqual(
		sections.featured.map((n) => n.id),
		[3]
	);
	assert.deepEqual(
		sections.notebooks.map((n) => n.id),
		[3, 2, 1]
	);
});

test("Recent has no featured row and is capped", () => {
	const list = Array.from({ length: RECENT_LIMIT + 3 }, (_, i) =>
		notebook(i + 1, { featured: i === 0 })
	);
	const sections = homeSections(list, "recent", "");
	assert.equal(sections.featured.length, 0);
	assert.equal(sections.notebooks.length, RECENT_LIMIT);
	assert.equal(sections.notebooks[0].id, RECENT_LIMIT + 3);
});

test("search narrows both rows by title, ignoring case", () => {
	const list = [
		notebook(1, { title: "Asthma QI", featured: true }),
		notebook(2, { title: "Iron deficiency" }),
	];
	const sections = homeSections(list, "all", "  asthma ");
	assert.deepEqual(
		sections.notebooks.map((n) => n.id),
		[1]
	);
	assert.deepEqual(
		sections.featured.map((n) => n.id),
		[1]
	);
});

test("dates are absolute and localized; covers are stable", () => {
	assert.equal(formatNotebookDate("2026-09-23T12:00:00Z", "en"), "Sep 23, 2026");
	assert.equal(formatNotebookDate("nonsense", "en"), "");
	assert.equal(notebookCover(7), notebookCover(7));
});

test("a grid card shows emoji, title, date and source count, with a menu", () => {
	const html = render(
		createElement(NotebookCard, {
			notebook: notebook(23, { title: "Asthma QI", source_count: 1 }),
			href: "/dashboard/1/notebooks/23",
			variant: "grid",
			...actions,
		})
	);
	assert.match(html, /Asthma QI/);
	assert.match(html, /Sep 23, 2026 · 1 source/);
	assert.match(html, /href="\/dashboard\/1\/notebooks\/23"/);
	assert.match(html, /aria-label="More actions"/);
});

test("a featured card has a cover and a list row keeps the meta", () => {
	const featured = render(
		createElement(NotebookCard, {
			notebook: notebook(4, { featured: true }),
			href: "/x",
			variant: "featured",
			...actions,
		})
	);
	assert.match(featured, new RegExp(notebookCover(4).split(" ")[0]));
	const row = render(
		createElement(NotebookCard, { notebook: notebook(5), href: "/y", variant: "list", ...actions })
	);
	assert.match(row, /Notebook 5/);
	assert.match(row, /1 source/);
});
