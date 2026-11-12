import assert from "node:assert/strict";
import test from "node:test";

import { etlCapabilities } from "@/contracts/types/document.types";
import {
	acceptedExtensions,
	fileExtension,
	partitionByExtension,
	resolveParserChoice,
} from "@/lib/etl/parser-choice";
import { capabilities } from "./parser-capabilities.fixture";

test("nothing is offered or sent until capabilities arrive", () => {
	assert.deepEqual(resolveParserChoice(undefined, "llamaparse"), {
		options: [],
		effectiveParser: undefined,
	});
});

test("all four parsers are offered in the server's order, configured or not", () => {
	const { options } = resolveParserChoice(capabilities(), undefined);
	assert.deepEqual(
		options.map((option) => [option.parser, option.available, option.unavailableReason]),
		[
			["local", true, null],
			["datalab", false, "not_configured"],
			["llamaparse", true, null],
			["unstructured", false, "not_configured"],
		]
	);
});

test("with no pick the server's default is the one sent", () => {
	assert.equal(resolveParserChoice(capabilities(), undefined).effectiveParser, "local");
	assert.equal(
		resolveParserChoice(capabilities({ default_parser: "llamaparse" }), undefined).effectiveParser,
		"llamaparse"
	);
});

test("a pick that can run is sent as picked", () => {
	assert.equal(resolveParserChoice(capabilities(), "llamaparse").effectiveParser, "llamaparse");
});

test("a pick that is not configured sends nothing, and no other parser takes its place", () => {
	// Datalab has no key. The default (local) can run, and must not be used
	// instead: the person named where the file goes.
	assert.equal(resolveParserChoice(capabilities(), "datalab").effectiveParser, undefined);
});

test("a default that is not configured sends nothing until the person picks", () => {
	const caps = capabilities({ default_parser: "unstructured" });
	assert.equal(resolveParserChoice(caps, undefined).effectiveParser, undefined);
	assert.equal(resolveParserChoice(caps, "local").effectiveParser, "local");
});

test("accepted types are the chosen parser's plus the ones the server reads itself", () => {
	const local = acceptedExtensions(capabilities(), "local");
	assert.deepEqual([...local].sort(), [".csv", ".docx", ".html", ".md", ".pdf", ".txt"]);
	// Lower-cased, so a server spelling of ".XLSX" still matches "report.xlsx".
	assert.ok(acceptedExtensions(capabilities(), "llamaparse").has(".xlsx"));
	assert.ok(!local.has(".xlsx"));
});

test("a parser that is not configured contributes none of its types", () => {
	const accepted = acceptedExtensions(capabilities(), "datalab");
	assert.ok(!accepted.has(".png"));
	assert.ok(!accepted.has(".pdf"));
	assert.ok(accepted.has(".md"));
});

test("with vision on, pictures are accepted whatever the parser reads", () => {
	const caps = capabilities({ vision_available: true });
	const local = acceptedExtensions(caps, "local", true);
	assert.ok(local.has(".png") && local.has(".heic") && local.has(".pdf"));
	// Off, the parser's list applies as before.
	assert.ok(!acceptedExtensions(caps, "local", false).has(".png"));
	// A switch left on after the server lost its vision model admits none.
	assert.ok(!acceptedExtensions(capabilities(), "local", true).has(".png"));
});

test("with no parser to send only the server-read types remain", () => {
	assert.deepEqual([...acceptedExtensions(capabilities(), undefined)].sort(), [
		".csv",
		".html",
		".md",
		".txt",
	]);
});

test("files are split by extension, case-insensitively, dotfiles and bare names rejected", () => {
	const accepted = new Set([".pdf", ".md"]);
	const { accepted: kept, rejected } = partitionByExtension(
		[{ name: "a.PDF" }, { name: "b.md" }, { name: "c.xlsx" }, { name: "README" }, { name: ".md" }],
		accepted
	);
	assert.deepEqual(
		kept.map((file) => file.name),
		["a.PDF", "b.md"]
	);
	assert.deepEqual(
		rejected.map((file) => file.name),
		["c.xlsx", "README", ".md"]
	);
	assert.equal(fileExtension("archive.tar.GZ"), ".gz");
});

test("the capabilities schema takes the server's answer and refuses the old shape", () => {
	assert.equal(etlCapabilities.safeParse(capabilities()).success, true);
	// "cloud" was the old two-way choice; it is not a parser any more.
	const old = { ...capabilities(), default_parser: "cloud" };
	assert.equal(etlCapabilities.safeParse(old).success, false);
	// The limits are required: the dialog enforces them and has no stand-in.
	const { max_file_size_bytes: _dropped, ...withoutLimit } = capabilities();
	assert.equal(etlCapabilities.safeParse(withoutLimit).success, false);
});
