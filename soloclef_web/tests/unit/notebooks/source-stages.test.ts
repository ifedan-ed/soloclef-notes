import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { NextIntlClientProvider } from "next-intl";
import { createElement, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { documentStatus } from "@/contracts/types/document.types";
import {
	indexingDocumentIds,
	SOURCE_STAGE_KEYS,
	stagesByDocument,
} from "@/features/notebooks/lib/source-stages";
import type { NotebookSource } from "@/features/notebooks/model/notebook";
import { SourceRow } from "@/features/notebooks/ui/sources/source-row";

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

const source = (over: Partial<NotebookSource>): NotebookSource => ({
	id: 1,
	kind: "document",
	target_id: 10,
	title: "Thesis.pdf",
	document_type: "FILE",
	status: "ready",
	enabled: true,
	added_at: "2026-11-11T00:00:00Z",
	...over,
});

const noop = () => undefined;

test("only documents still being indexed are asked about, in id order", () => {
	const ids = indexingDocumentIds([
		source({ id: 1, target_id: 30, status: "processing" }),
		source({ id: 2, target_id: 10, status: "pending" }),
		source({ id: 3, target_id: 20, status: "ready" }),
		source({ id: 4, target_id: 5, status: "failed" }),
		// A folder's target is a folder id, not a document's.
		source({ id: 5, kind: "folder", target_id: 1, status: "processing" }),
	]);
	assert.deepEqual(ids, [10, 30]);
});

test("a stage is kept only for a document that is processing", () => {
	const item = (id: number, status: unknown) => ({
		id,
		title: "t",
		document_type: "FILE" as const,
		status: documentStatus.parse(status),
	});
	const stages = stagesByDocument([
		item(1, { state: "processing", reason: null, stage: "embedding" }),
		item(2, { state: "processing", reason: null, stage: null }),
		item(3, { state: "pending", reason: null, stage: null }),
		// A stage beside a finished state is stale and must not be shown.
		item(4, { state: "ready", reason: null, stage: "indexing" }),
	]);
	assert.deepEqual([...stages], [[1, "embedding"]]);
});

test("the status schema takes the five stages and refuses any other", () => {
	for (const stage of ["parsing", "chunking", "embedding", "indexing", "retrying"]) {
		assert.equal(documentStatus.safeParse({ state: "processing", stage }).success, true, stage);
	}
	assert.equal(
		documentStatus.safeParse({ state: "processing", stage: "uploading" }).success,
		false
	);
	// Outside "processing" the server leaves the key out altogether.
	assert.equal(documentStatus.safeParse({ state: "ready", reason: null }).success, true);
});

test("every stage has its words in English", () => {
	for (const [stage, key] of Object.entries(SOURCE_STAGE_KEYS)) {
		assert.equal(typeof messages.notebooks[key], "string", stage);
	}
});

test("a processing source row names the stage under its title", () => {
	const html = render(
		createElement(SourceRow, {
			source: source({ status: "processing" }),
			stage: "embedding",
			onToggle: noop,
			onOpen: noop,
			onRemove: noop,
			onReindexed: noop,
		})
	);
	assert.match(html, /data-testid="source-stage"[^>]*>Embedding</);
	assert.match(html, /Thesis\.pdf/);
});

test("a ready source shows no stage even if one is handed in", () => {
	const html = render(
		createElement(SourceRow, {
			source: source({ status: "ready" }),
			stage: "embedding",
			onToggle: noop,
			onOpen: noop,
			onRemove: noop,
			onReindexed: noop,
		})
	);
	assert.doesNotMatch(html, /source-stage/);
});

test("a processing source with no stage yet keeps just its spinner", () => {
	const html = render(
		createElement(SourceRow, {
			source: source({ status: "processing" }),
			onToggle: noop,
			onOpen: noop,
			onRemove: noop,
			onReindexed: noop,
		})
	);
	assert.doesNotMatch(html, /source-stage/);
	assert.match(html, /Still being indexed/);
});

test("a source that failed to index says so under its title", () => {
	const html = render(
		createElement(SourceRow, {
			source: source({ status: "failed" }),
			onToggle: noop,
			onOpen: noop,
			onRemove: noop,
			onReindexed: noop,
		})
	);
	assert.match(html, /data-testid="source-failed"[^>]*>Indexing failed</);
	assert.doesNotMatch(html, /Still being indexed/);
});
