import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { NextIntlClientProvider } from "next-intl";
import { type ComponentProps, createElement, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { TooltipProvider } from "@/components/ui/tooltip";
import { STUDIO_TILES } from "@/features/notebooks/lib/studio-catalog";
import type { StudioOutput } from "@/features/notebooks/model/notebook";
import { FocusCards } from "@/features/notebooks/ui/studio/focus-cards";
import { StudioOutputRow } from "@/features/notebooks/ui/studio/studio-output-row";
import { StudioTiles } from "@/features/notebooks/ui/studio/studio-tiles";
import { StyleGallery } from "@/features/notebooks/ui/studio/style-gallery";

const messages = JSON.parse(readFileSync(join(process.cwd(), "messages", "en.json"), "utf8"));

function render(element: ReactElement): string {
	const tooltips: ComponentProps<typeof TooltipProvider> = { children: element };
	const props: ComponentProps<typeof NextIntlClientProvider> = {
		locale: "en",
		messages,
		timeZone: "UTC",
		children: createElement(TooltipProvider, tooltips),
	};
	return renderToStaticMarkup(createElement(NextIntlClientProvider, props));
}

const noop = async () => undefined;

function output(overrides: Partial<StudioOutput>): StudioOutput {
	return {
		id: 1,
		kind: "video",
		title: "Video Overview: Iron",
		status: "ready",
		artifact_id: 8,
		artifact_format: "video",
		duration_seconds: 487,
		source_count: 1,
		options: { format: "explainer" },
		created_at: new Date(Date.now() - 86_400_000).toISOString(),
		...overrides,
	};
}

test("the Studio grid shows every output as a tile, two columns wide", () => {
	const html = render(createElement(StudioTiles, { onPick: () => undefined }));
	assert.match(html, /grid-cols-2/);
	for (const label of [
		"Audio Overview",
		"Video Overview",
		"Slide Deck",
		"Mind Map",
		"Reports",
		"Flashcards",
		"Quiz",
		"Infographic",
		"Data Table",
	]) {
		assert.ok(html.includes(`>${label}<`), label);
	}
	assert.equal((html.match(/<button/g) ?? []).length, STUDIO_TILES.length);
});

test("a finished video reads like the spec and can be played", () => {
	const html = render(
		createElement(StudioOutputRow, {
			output: output({}),
			onOpen: () => undefined,
			onRename: noop,
			onDelete: noop,
			onRetry: () => undefined,
		})
	);
	assert.match(html, /Video Overview: Iron/);
	assert.match(
		html,
		/8:07 · Explainer · 1 source · 1 day ago|8:07 · Explainer · 1 source · yesterday/
	);
	assert.match(html, /aria-label="Play"/);
	assert.match(html, /aria-label="More actions"/);
});

test("a running output shows its live progress instead of a play button", () => {
	const html = render(
		createElement(StudioOutputRow, {
			output: output({
				status: "running",
				progress: 40,
				artifact_id: null,
				duration_seconds: null,
			}),
			onOpen: () => undefined,
			onRename: noop,
			onDelete: noop,
			onRetry: () => undefined,
		})
	);
	assert.match(html, /40%/);
	assert.doesNotMatch(html, /aria-label="Play"/);
});

test("the style gallery and focus cards render their choices", () => {
	const gallery = render(
		createElement(StyleGallery, {
			value: "auto",
			label: "Choose visual style",
			scrollLabels: { previous: "Previous", next: "More styles" },
			onChange: () => undefined,
			options: [
				{
					value: "auto",
					label: "Auto-select",
					description: "d",
					preview: "/video-styles/auto.svg",
				},
				{ value: "dark", label: "Dark", description: "d", preview: "/video-styles/dark.svg" },
			],
		})
	);
	assert.match(gallery, /aria-pressed="true"[^>]*title="d"[^>]*>.*Auto-select/);
	assert.match(gallery, /src="\/video-styles\/dark.svg"/);
	assert.match(gallery, /aria-label="More styles"/);

	const cards = render(
		createElement(FocusCards, {
			label: "Focus",
			suggestions: ["Iron absorption", "Deficiency signs"],
			focus: "Iron absorption",
			onPick: () => undefined,
			onCustom: () => undefined,
			customLabel: "Custom topic",
			customDescription: "Write your own focus below.",
		})
	);
	assert.match(cards, /aria-pressed="true"[^>]*>.*Iron absorption/);
	assert.match(cards, /Custom topic/);
});
