import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { NextIntlClientProvider } from "next-intl";
import { type ComponentProps, createElement, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { selectValue } from "@/components/settings/model-connections/quick-tasks-model";
import {
	advanceRotation,
	claimOpenOffset,
	startRotation,
	suggestionVariant,
	suggestionWindow,
	windowCount,
} from "@/features/notebooks/lib/suggestion-rotation";
import { MoreIdeasButton } from "@/features/notebooks/ui/studio/more-ideas-button";

const LOCALES = ["en", "es", "hi", "ko", "pt", "zh"];
const KINDS = [
	"audio",
	"video",
	"report",
	"slides",
	"mindmap",
	"flashcards",
	"quiz",
	"data_table",
	"infographic",
];

function messages(locale: string) {
	return JSON.parse(readFileSync(join(process.cwd(), "messages", `${locale}.json`), "utf8"));
}

function render(element: ReactElement): string {
	const props: ComponentProps<typeof NextIntlClientProvider> = {
		locale: "en",
		messages: messages("en"),
		timeZone: "UTC",
		children: element,
	};
	return renderToStaticMarkup(createElement(NextIntlClientProvider, props));
}

const POOL = Array.from({ length: 9 }, (_, n) => `Idea ${n}`);

test("each open of a dialog shows a different three, and the example rotates too", () => {
	const shown = [0, 1, 2, 3].map((offset) => suggestionWindow(POOL, ["E0", "E1", "E2"], offset));
	assert.deepEqual(shown[0].suggestions, ["Idea 0", "Idea 1", "Idea 2"]);
	assert.deepEqual(shown[1].suggestions, ["Idea 3", "Idea 4", "Idea 5"]);
	assert.equal(new Set(shown.slice(0, 3).map((w) => w.suggestions.join())).size, 3);
	assert.deepEqual(shown[3].suggestions, shown[0].suggestions);
	assert.deepEqual(
		shown.map((w) => w.placeholder),
		["E0", "E1", "E2", "E0"]
	);
});

test("every group holds three distinct ideas whatever the pool size", () => {
	for (const size of [3, 4, 5, 7, 8]) {
		const pool = POOL.slice(0, size);
		for (let offset = 0; offset < size * 2; offset++) {
			const ideas = suggestionWindow(pool, [], offset).suggestions;
			assert.equal(new Set(ideas).size, 3, `size ${size}, offset ${offset}`);
		}
	}
	assert.equal(suggestionWindow(POOL, [], 0).placeholder, null);
});

test("the open counter advances per dialog, with or without storage", () => {
	const store = new Map<string, string>();
	const storage = {
		getItem: (key: string) => store.get(key) ?? null,
		setItem: (key: string, value: string) => void store.set(key, value),
	};
	assert.deepEqual(
		[1, 2, 3].map(() => claimOpenOffset("3:quiz::", storage)),
		[0, 1, 2]
	);
	assert.equal(claimOpenOffset("3:audio:debate:", storage), 0);

	const broken = {
		getItem: () => {
			throw new Error("blocked");
		},
		setItem: () => {
			throw new Error("blocked");
		},
	};
	assert.deepEqual(
		[1, 2].map(() => claimOpenOffset("9:mindmap::", broken)),
		[0, 1]
	);
});

test("More ideas steps through the pool, then asks for a fresh one", () => {
	let state = startRotation(5);
	const steps: boolean[] = [];
	for (let click = 0; click < 3; click++) {
		const next = advanceRotation(state, POOL.length);
		steps.push(next.exhausted);
		state = next.state;
	}
	assert.deepEqual(steps, [false, false, true]);
	assert.equal(state.offset, 7);
	assert.equal(windowCount(POOL.length), 3);
	assert.equal(advanceRotation(startRotation(0), 3).exhausted, true);
});

test("the idea flavour follows the dialog's format or difficulty", () => {
	assert.equal(suggestionVariant("audio", { format: "debate", length: "short" }), "debate");
	assert.equal(suggestionVariant("quiz", { count: "more", difficulty: "hard" }), "hard");
	assert.equal(suggestionVariant("data_table", {}), "");
});

test("More ideas renders its label and never shows a spinner on first paint", () => {
	const html = render(
		createElement(MoreIdeasButton, { label: "More ideas", busy: true, onClick: () => undefined })
	);
	assert.match(html, /More ideas/);
	assert.match(html, /aria-busy="true"/);
	assert.doesNotMatch(html, /animate-spin/);
});

test("every locale names each dialog's ideas and the quick tasks model", () => {
	for (const locale of LOCALES) {
		const all = messages(locale);
		for (const kind of KINDS) {
			assert.ok(all.notebooks.kinds[kind].ideas_label, `${locale} ${kind}`);
		}
		assert.ok(all.notebooks.more_ideas && all.notebooks.more_ideas_failed, locale);
		for (const key of [
			"title",
			"description",
			"default_option",
			"default_unavailable",
			"same_as_chat",
			"save_failed",
		]) {
			assert.ok(all.quick_model[key], `${locale} quick_model.${key}`);
		}
	}
});

test("the quick model select keeps default, same-as-chat and a gone model apart", () => {
	assert.equal(selectValue(null, [4]), "default");
	assert.equal(selectValue(0, [4]), "0");
	assert.equal(selectValue(4, [4]), "4");
	assert.equal(selectValue(99, [4]), "default");
});
