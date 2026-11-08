import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { NextIntlClientProvider } from "next-intl";
import { createElement, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { formatCost } from "@/features/api-runs/lib/format";
import { OutputViewer } from "@/features/api-runs/output-viewer";
import { RunStatusBadge } from "@/features/api-runs/run-status-badge";

const LOCALES = ["en", "es", "hi", "ko", "pt", "zh"] as const;

function messages(locale: string) {
	return JSON.parse(readFileSync(join(process.cwd(), "messages", `${locale}.json`), "utf8"));
}

function render(locale: string, element: ReactElement): string {
	return renderToStaticMarkup(
		createElement(NextIntlClientProvider, {
			locale,
			messages: messages(locale),
			timeZone: "UTC",
			// biome-ignore lint/correctness/noChildrenProp: the provider's props type requires children.
			children: element,
		})
	);
}

test("every locale carries every runs-table string, translated", () => {
	const english = messages("en").api_runs as Record<string, string>;
	for (const locale of LOCALES) {
		const catalogue = messages(locale).api_runs as Record<string, string>;
		assert.deepEqual(Object.keys(catalogue).sort(), Object.keys(english).sort(), locale);
		for (const [key, value] of Object.entries(catalogue)) {
			assert.ok(value.trim(), `${locale}.${key} is empty`);
		}
	}
	// Words, not just identical copies of English (brand names and "API"/"JSON" aside).
	assert.notEqual(messages("es").api_runs.col_duration, english.col_duration);
	assert.notEqual(messages("zh").api_runs.load_more, english.load_more);
});

test("the status badge and output viewer speak the viewer's language", () => {
	assert.match(render("es", createElement(RunStatusBadge, { status: "success" })), />Correcta</);
	assert.match(render("ko", createElement(RunStatusBadge, { status: "cancelled" })), />취소됨</);
	const viewer = render("pt", createElement(OutputViewer, { data: { items: [] } }));
	assert.match(viewer, /Nenhum item retornado/);
	assert.match(viewer, />Tabela</);
});

test("a free run shows the translated free label", () => {
	assert.equal(formatCost(0, "Gratis"), "Gratis");
	assert.equal(formatCost(null, "Gratis"), "—");
	assert.equal(formatCost(1234, "Gratis"), "$0.0012");
});
