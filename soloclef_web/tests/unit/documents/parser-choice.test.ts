import assert from "node:assert/strict";
import test from "node:test";

import type { EtlCapabilities } from "@/contracts/types/document.types";
import {
	acceptedExtensions,
	fileExtension,
	partitionByExtension,
	resolveParserChoice,
} from "@/lib/etl/parser-choice";

const caps = (over: Partial<EtlCapabilities> = {}, cloud: Record<string, unknown> = {}) =>
	({
		default_parser: "local",
		parsers: [
			{
				parser: "local",
				service: "PYMUPDF4LLM",
				available: true,
				unavailable_reason: null,
				page_multiplier: 0,
				document_extensions: [".pdf", ".docx", ".png"],
			},
			{
				parser: "cloud",
				service: "LLAMACLOUD",
				available: true,
				unavailable_reason: null,
				page_multiplier: 10,
				document_extensions: [".pdf", ".docx", ".xlsx", ".png", ".webp"],
				...cloud,
			},
		],
		always_handled: {
			plaintext: [".md", ".txt"],
			direct_convert: [".csv"],
			audio: [".mp3"],
			image: [".png", ".webp", ".heic"],
		},
		page_credit_billing_enabled: false,
		...over,
	}) as EtlCapabilities;

test("nothing is offered or sent until capabilities arrive", () => {
	assert.deepEqual(resolveParserChoice(undefined, "cloud"), {
		options: [],
		effectiveParser: undefined,
	});
});

test("the selection is forwarded when it can run", () => {
	assert.equal(resolveParserChoice(caps(), "cloud").effectiveParser, "cloud");
	assert.equal(resolveParserChoice(caps(), "local").effectiveParser, "local");
});

test("no selection takes the server default", () => {
	assert.equal(
		resolveParserChoice(caps({ default_parser: "cloud" }), undefined).effectiveParser,
		"cloud"
	);
});

test("an unavailable cloud is shown disabled with its reason and never sent", () => {
	const choice = resolveParserChoice(
		caps({}, { available: false, unavailable_reason: "missing_api_key" }),
		"cloud"
	);
	const cloud = choice.options.find((option) => option.parser === "cloud");
	assert.equal(cloud?.available, false);
	assert.equal(cloud?.unavailableReason, "missing_api_key");
	assert.equal(choice.effectiveParser, "local");
});

test("cost is only mentioned when page billing is on and the parser charges", () => {
	const off = resolveParserChoice(caps(), "cloud").options;
	assert.deepEqual(
		off.map((option) => option.costMultiplier),
		[null, null]
	);
	const on = resolveParserChoice(caps({ page_credit_billing_enabled: true }), "cloud").options;
	assert.deepEqual(
		on.map((option) => option.costMultiplier),
		[null, 10]
	);
});

test("accepted types follow the chosen parser plus the parser-agnostic ones", () => {
	const local = acceptedExtensions(caps(), "local", { visionRequested: false });
	assert.ok(local.has(".pdf") && local.has(".md") && local.has(".csv") && local.has(".mp3"));
	assert.ok(!local.has(".xlsx"));
	assert.ok(!local.has(".webp"));
	assert.ok(acceptedExtensions(caps(), "cloud", { visionRequested: false }).has(".xlsx"));
});

test("images the parser cannot open are accepted when the vision LLM reads them", () => {
	assert.ok(acceptedExtensions(caps(), "local", { visionRequested: true }).has(".webp"));
});

test("an unavailable parser contributes no document types", () => {
	const accepted = acceptedExtensions(
		caps({}, { available: false, unavailable_reason: "missing_api_key" }),
		"cloud",
		{ visionRequested: false }
	);
	assert.ok(!accepted.has(".xlsx"));
	assert.ok(accepted.has(".md"));
});

test("files are split by extension, case-insensitively", () => {
	const files = [{ name: "a.PDF" }, { name: "b.xlsx" }, { name: "README" }, { name: ".env" }];
	const { accepted, rejected } = partitionByExtension(files, new Set([".pdf"]));
	assert.deepEqual(
		accepted.map((f) => f.name),
		["a.PDF"]
	);
	assert.deepEqual(
		rejected.map((f) => f.name),
		["b.xlsx", "README", ".env"]
	);
	assert.equal(fileExtension("archive.tar.gz"), ".gz");
});
