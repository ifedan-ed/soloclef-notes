import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { NextIntlClientProvider } from "next-intl";
import { createElement, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { Tabs } from "@/components/ui/tabs";
import type { RemoteEntry } from "@/features/nextcloud-files/api/nextcloud-files-api";
import { importErrorKind } from "@/features/nextcloud-files/lib/import-errors";
import { NextcloudBreadcrumbs } from "@/features/nextcloud-files/ui/nextcloud-breadcrumbs";
import { NextcloudFileList } from "@/features/nextcloud-files/ui/nextcloud-file-list";
import { NextcloudImportConfirm } from "@/features/nextcloud-files/ui/nextcloud-import-confirm";
import { AddSourcesTabBar } from "@/features/notebooks/ui/sources/add-sources-tab-bar";
import { AppError } from "@/lib/error";

const messages = JSON.parse(readFileSync(join(process.cwd(), "messages", "en.json"), "utf8"));

function render(element: ReactElement): string {
	return renderToStaticMarkup(
		createElement(NextIntlClientProvider, {
			locale: "en",
			messages,
			timeZone: "UTC",
			// biome-ignore lint/correctness/noChildrenProp: the provider's props type requires children.
			children: element,
		})
	);
}

const noop = () => undefined;

const entries: RemoteEntry[] = [
	{
		path: "/Open Notebook",
		name: "Open Notebook",
		is_folder: true,
		size: 100,
		modified: "2025-02-07T15:29:27Z",
		content_type: null,
		etag: "f",
	},
	{
		path: "/Résumé – 2024.pdf",
		name: "Résumé – 2024.pdf",
		is_folder: false,
		size: 256815,
		modified: "2025-06-02T08:00:00Z",
		content_type: "application/pdf",
		etag: "p",
	},
	{
		path: "/Movie.mkv",
		name: "Movie.mkv",
		is_folder: false,
		size: 734003200,
		modified: null,
		content_type: "video/x-matroska",
		etag: "m",
	},
];

test("the file list opens folders, shows size and date, and disables unreadable files", () => {
	const html = render(
		createElement(NextcloudFileList, {
			entries,
			selection: new Map([
				["/Résumé – 2024.pdf", { path: "/Résumé – 2024.pdf", kind: "file" as const }],
			]),
			accepted: new Set([".pdf"]),
			parserName: "Local",
			onToggle: noop,
			onOpenFolder: noop,
		})
	);
	assert.match(html, /aria-label="Open folder Open Notebook"/);
	assert.match(html, /aria-label="Select folder Open Notebook"/);
	assert.match(html, /251 KB · Jun 2, 2025/);
	assert.match(
		html,
		/aria-label="Select Résumé – 2024.pdf"[^>]*data-state="checked"|data-state="checked"[^>]*aria-label="Select Résumé – 2024.pdf"/
	);
	assert.match(html, /Not supported by the Local parser/);
	assert.match(html, /data-unsupported="true"/);
	assert.equal((html.match(/data-unsupported/g) ?? []).length, 1);
});

test("search results say where each file lives", () => {
	const html = render(
		createElement(NextcloudFileList, {
			entries: [{ ...entries[1], path: "/Notes/Résumé – 2024.pdf" }],
			selection: new Map(),
			accepted: new Set([".pdf"]),
			parserName: "Local",
			showLocation: true,
			onToggle: noop,
			onOpenFolder: noop,
		})
	);
	assert.match(html, /\/Notes · 251 KB/);
});

test("breadcrumbs link back to every level but the current one", () => {
	const html = render(
		createElement(NextcloudBreadcrumbs, { path: "/Open Notebook/Week 1", onNavigate: noop })
	);
	assert.match(html, /aria-label="Folder path"/);
	assert.match(html, /<button[^>]*>.*Nextcloud<\/span><\/button>/);
	assert.match(html, /<button[^>]*><span[^>]*>Open Notebook<\/span><\/button>/);
	assert.match(html, /aria-current="page"[^>]*><span[^>]*>Week 1<\/span>/);
});

test("the confirm step counts files and says what is skipped", () => {
	const html = render(
		createElement(NextcloudImportConfirm, {
			preview: {
				file_count: 2,
				total_bytes: 13419431,
				unsupported_count: 1,
				too_large_count: 0,
				over_limit: false,
				max_files: 200,
			},
			busy: false,
			onConfirm: noop,
			onCancel: noop,
		})
	);
	assert.match(html, /Import 2 files \(13 MB\)\?/);
	assert.match(html, /1 file is skipped/);
	assert.match(html, />Import 2 files</);
});

test("a pick over the cap cannot be confirmed", () => {
	const html = render(
		createElement(NextcloudImportConfirm, {
			preview: {
				file_count: 200,
				total_bytes: 1,
				unsupported_count: 0,
				too_large_count: 0,
				over_limit: true,
				max_files: 200,
			},
			busy: false,
			onConfirm: noop,
			onCancel: noop,
		})
	);
	assert.match(html, /more than 200 readable files/);
	assert.doesNotMatch(html, />Import 200 files</);
});

test("the add-sources tabs fit a phone: three equal columns with one-word labels", () => {
	const html = render(createElement(Tabs, { value: "library" }, createElement(AddSourcesTabBar)));
	assert.match(
		html,
		/class="[^"]*\bgrid\b[^"]*\bw-full\b[^"]*\bgrid-cols-3\b[^"]*sm:inline-flex[^"]*sm:w-fit/
	);
	for (const [short, long] of [
		["Workspace", "From your workspace"],
		["Upload", "Upload files"],
		["Connector", "From a connector"],
	]) {
		assert.match(html, new RegExp(`<span class="truncate sm:hidden">${short}</span>`));
		assert.match(html, new RegExp(`<span class="hidden sm:inline">${long}</span>`));
	}
	assert.equal((html.match(/role="tab"/g) ?? []).length, 3);
});

test("failed calls map to the message the picker shows", () => {
	const auth = new AppError("refused", 400, "Bad Request", "nextcloud_auth");
	const many = new AppError("too many", 422, "Unprocessable", "too_many_files");
	const gone = new AppError("gone", 404, "Not Found");
	assert.equal(importErrorKind(auth), "auth");
	assert.equal(importErrorKind(many), "too_many");
	assert.equal(importErrorKind(gone), "not_found");
	assert.equal(importErrorKind(new Error("x")), "other");
});
