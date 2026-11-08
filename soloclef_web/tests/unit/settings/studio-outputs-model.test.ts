import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { NextIntlClientProvider } from "next-intl";
import { type ComponentProps, createElement, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
	ALL_AUTO,
	chooseFallback,
	choosePrimary,
	fallbackValue,
	primaryValue,
	type StudioModelRead,
	type StudioModelSettings,
} from "@/components/settings/model-connections/studio-outputs-choice";
import {
	StudioOutputsModelSetting,
	studioModelKey,
} from "@/components/settings/model-connections/studio-outputs-model";

const LOCALES = ["en", "es", "hi", "ko", "pt", "zh"];
const SLOTS = [
	"audio",
	"video",
	"report_interactive",
	"report_pdf",
	"report_docx",
	"slides",
	"mindmap",
	"flashcards",
	"quiz",
	"infographic",
	"data_table",
];
const LUNA = 3;
const FLASH = 2;
const GEMINI = 73;
const MODELS = [LUNA, FLASH, GEMINI];

function messages(locale: string) {
	return JSON.parse(readFileSync(join(process.cwd(), "messages", `${locale}.json`), "utf8"));
}

const recommended: StudioModelSettings = {
	default: { mode: "recommended" },
	overrides: {
		report_interactive: { mode: "model", primary: FLASH, fallback: LUNA },
		quiz: { mode: "model", primary: LUNA, fallback: FLASH },
	},
};

test("the selects show default, Auto, a model, and a gone model's next level", () => {
	assert.equal(primaryValue(recommended, null, MODELS), "recommended");
	assert.equal(primaryValue(recommended, "quiz", MODELS), String(LUNA));
	assert.equal(fallbackValue(recommended, "quiz", MODELS), String(FLASH));
	assert.equal(primaryValue(recommended, "slides", MODELS), "default");
	assert.equal(fallbackValue(recommended, "slides", MODELS), "none");
	assert.equal(primaryValue(ALL_AUTO, null, MODELS), "auto");
	// Luna disabled since it was chosen: the row reads as falling to the default.
	assert.equal(primaryValue(recommended, "quiz", [FLASH]), "default");
	assert.equal(fallbackValue(recommended, "report_interactive", [FLASH]), "none");
});

test("each edit returns the whole setting, keeping a fallback only where it still fits", () => {
	const gemini = choosePrimary(recommended, "quiz", String(GEMINI));
	assert.deepEqual(gemini.overrides.quiz, { mode: "model", primary: GEMINI, fallback: FLASH });
	// Choosing the fallback as the primary drops it as the fallback.
	const flash = choosePrimary(recommended, "quiz", String(FLASH));
	assert.deepEqual(flash.overrides.quiz, { mode: "model", primary: FLASH, fallback: null });
	assert.deepEqual(choosePrimary(recommended, "quiz", "auto").overrides.quiz, { mode: "auto" });
	assert.equal("quiz" in choosePrimary(recommended, "quiz", "default").overrides, false);
	assert.deepEqual(choosePrimary(recommended, null, "auto").default, { mode: "auto" });
	assert.deepEqual(choosePrimary(recommended, null, String(GEMINI)).default, {
		mode: "model",
		primary: GEMINI,
		fallback: null,
	});
	assert.deepEqual(chooseFallback(recommended, "quiz", "none").overrides.quiz, {
		mode: "model",
		primary: LUNA,
		fallback: null,
	});
	// No fallback without a specific primary.
	assert.equal(chooseFallback(recommended, null, String(FLASH)), recommended);
	// The original is never mutated.
	assert.deepEqual(recommended.overrides.quiz, { mode: "model", primary: LUNA, fallback: FLASH });
	assert.deepEqual(ALL_AUTO, { default: { mode: "auto" }, overrides: {} });
});

test("every locale names the card, each output type, and the made-with line", () => {
	const keys = Object.keys(messages("en").studio_model).filter((key) => key !== "slots");
	for (const locale of LOCALES) {
		const all = messages(locale);
		for (const key of keys) assert.ok(all.studio_model[key], `${locale} studio_model.${key}`);
		for (const slot of SLOTS) {
			assert.ok(all.studio_model.slots[slot], `${locale} studio_model.slots.${slot}`);
		}
		for (const key of ["made_with", "made_with_fallback"]) {
			assert.ok(all.notebooks[key]?.includes("{model}"), `${locale} notebooks.${key}`);
		}
	}
});

function render(read: StudioModelRead): string {
	const client = new QueryClient();
	client.setQueryData(studioModelKey(1), read);
	const card = createElement(StudioOutputsModelSetting<{ id: number }>, {
		workspaceId: 1,
		models: MODELS.map((id) => ({ id })),
		renderOption: () => null,
	});
	const props: ComponentProps<typeof NextIntlClientProvider> = {
		locale: "en",
		messages: messages("en"),
		timeZone: "UTC",
		children: createElement(QueryClientProvider, { client }, card) as ReactElement,
	};
	return renderToStaticMarkup(createElement(NextIntlClientProvider, props));
}

const READ: StudioModelRead = {
	...recommended,
	customized: false,
	recommended_primary: { name: "openai-gpt-5.6-luna", id: LUNA },
	recommended_fallback: { name: "ds-deepseek-v4.1-flash", id: FLASH },
	unavailable: [],
	slots: SLOTS,
};

test("the card lists every output type and offers Auto for everything in one click", () => {
	const html = render(READ);
	const en = messages("en").studio_model;
	assert.ok(html.includes(en.title));
	for (const slot of SLOTS) assert.ok(html.includes(en.slots[slot]), slot);
	assert.ok(html.includes(en.all_auto));
	assert.ok(!html.includes(en.reset), "nothing to reset on the recommended setup");
	assert.ok(!html.includes("Not available right now"));
});

test("a changed setup can be reset, and an unusable model is warned about", () => {
	const html = render({ ...READ, customized: true, unavailable: ["default", "quiz"] });
	const en = messages("en").studio_model;
	assert.ok(html.includes(en.reset));
	assert.ok(
		html.includes("Not available right now, so the next setting down is used: Default, Quiz")
	);
});
