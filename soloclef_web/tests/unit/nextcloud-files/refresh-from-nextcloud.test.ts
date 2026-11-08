import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { NextIntlClientProvider } from "next-intl";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
	type RefreshState,
	refreshFailure,
	refreshLabelKey,
	refreshOutcome,
} from "@/features/nextcloud-files/lib/refresh-state";
import { RefreshFromNextcloudLabel } from "@/features/nextcloud-files/ui/refresh-from-nextcloud-label";
import { AppError } from "@/lib/error";

const messagesDir = join(process.cwd(), "messages");
const load = (file: string) => JSON.parse(readFileSync(join(messagesDir, file), "utf8"));

function render(state: RefreshState, locale = "en"): string {
	return renderToStaticMarkup(
		createElement(NextIntlClientProvider, {
			locale,
			messages: load(`${locale}.json`),
			timeZone: "UTC",
			// biome-ignore lint/correctness/noChildrenProp: the provider's props type requires children.
			children: createElement(RefreshFromNextcloudLabel, { state }),
		})
	);
}

const STATES: RefreshState[] = [
	{ phase: "idle" },
	{ phase: "pending" },
	{ phase: "updated" },
	{ phase: "unchanged" },
	{ phase: "error", kind: "auth" },
	{ phase: "error", kind: "not_found" },
	{ phase: "error", kind: "other" },
];

test("a changed file reads as updated, an unchanged one as up to date", () => {
	assert.deepEqual(refreshOutcome("queued"), { phase: "updated" });
	assert.deepEqual(refreshOutcome("unchanged"), { phase: "unchanged" });
});

test("failures keep their kind, so the item can say what went wrong", () => {
	assert.deepEqual(refreshFailure(new AppError("gone", 404)), {
		phase: "error",
		kind: "not_found",
	});
	assert.deepEqual(refreshFailure(new Error("boom")), { phase: "error", kind: "other" });
});

test("each state renders its own inline label, with no alert", () => {
	const labels = STATES.map((state) => render(state));
	assert.match(labels[0], /Refresh from Nextcloud/);
	assert.match(labels[1], /Refreshing…/);
	assert.match(labels[2], /Updated/);
	assert.match(labels[3], /Already up to date/);
	assert.match(labels[4], /Reconnect Nextcloud/);
	assert.match(labels[5], /No longer in Nextcloud/);
	assert.match(labels[6], /Couldn&#x27;t refresh/);
	assert.equal(new Set(labels).size, STATES.length);
	for (const label of labels.slice(1)) assert.match(label, /role="status"/);
	assert.doesNotMatch(labels[0], /role="status"/);
});

test("every locale has every refresh label", () => {
	const locales = readdirSync(messagesDir).filter((file) => file.endsWith(".json"));
	assert.equal(locales.length, 6);
	for (const file of locales) {
		const section = load(file).nextcloud_files;
		for (const state of STATES) {
			const key = refreshLabelKey(state);
			assert.ok(section[key], `${file} is missing nextcloud_files.${key}`);
		}
		const locale = file.replace(/\.json$/, "");
		assert.ok(render({ phase: "idle" }, locale).includes("Nextcloud"));
	}
});
