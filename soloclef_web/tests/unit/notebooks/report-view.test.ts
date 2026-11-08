import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { NextIntlClientProvider } from "next-intl";
import { type ComponentProps, createElement, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { buildHtmlArtifactDocument } from "@/features/file-viewers/html-artifact-document";
import { studioOpenAction } from "@/features/notebooks/lib/open-output";
import {
	BRIDGE_FROM_REPORT,
	parseBridgeMessage,
	REPORT_BRIDGE_SCRIPT,
} from "@/features/notebooks/lib/report-bridge";
import {
	activeSectionIndex,
	headingLabel,
	isReadable,
	splitReport,
} from "@/features/notebooks/lib/report-outline";
import { placeSuggestions, suggestionPhase } from "@/features/notebooks/lib/report-suggestions";
import type { ReportSuggestion, StudioOutput } from "@/features/notebooks/model/notebook";
import { ReportSuggestionCard } from "@/features/notebooks/ui/report/report-suggestion-card";
import { ReportToc } from "@/features/notebooks/ui/report/report-toc";

// The same report the backend's tests split (tests/unit/notebooks/test_report_view.py).
const REPORT = `# CHAM PICU Resident Orientation

*Prepared for new residents*

Welcome to the unit.

## 1. Introduction

The PICU cares for critically ill children.

## 2. Navigating the PICU: Rounds

Day rounds start at 08:00; night handover at 19:00.

### Day shift

Pre-round by 07:30.

\`\`\`python
## not a heading inside code
\`\`\`

## 3. Communication & Escalation

Call the fellow first, then the attending.

## Summary

Be early and ask questions.
`;

test("the outline matches the backend's: title, preamble, listed sections", () => {
	const outline = splitReport(REPORT);
	assert.equal(outline.title, "CHAM PICU Resident Orientation");
	assert.ok(outline.preamble.startsWith("*Prepared for new residents*"));
	assert.deepEqual(
		outline.sections.map((s) => [s.index, s.heading]),
		[
			[0, "Introduction"],
			[1, "Navigating the PICU: Rounds"],
			[2, "Communication & Escalation"],
			[3, "Summary"],
		]
	);
	const rounds = outline.sections[1].markdown;
	assert.ok(rounds.startsWith("## 2. Navigating the PICU: Rounds"));
	assert.ok(rounds.includes("### Day shift"));
	assert.ok(rounds.includes("## not a heading inside code"));
	assert.ok(!rounds.includes("Communication"));
});

test("outline edge cases: H1 sections, a single subheading, no headings", () => {
	const h1 = splitReport("Intro text.\n\n# One\n\nA\n\n# Two\n\nB\n");
	assert.equal(h1.title, null);
	assert.equal(h1.preamble, "Intro text.");
	assert.deepEqual(
		h1.sections.map((s) => s.heading),
		["One", "Two"]
	);
	assert.deepEqual(
		splitReport("# Title\n\nLead.\n\n### Only one\n\nBody.\n").sections.map((s) => s.heading),
		["Only one"]
	);
	const none = splitReport("# Title\n\nJust prose.");
	assert.deepEqual(none.sections, []);
	assert.equal(none.preamble, "Just prose.");
	assert.equal(isReadable(none), false);
	assert.equal(isReadable(splitReport(REPORT)), true);
	assert.equal(isReadable(splitReport(`# T\n\n${"Long prose. ".repeat(50)}`)), true);
});

test("heading labels drop numbering and emphasis, not a leading year", () => {
	assert.equal(headingLabel("1. Introduction"), "Introduction");
	assert.equal(headingLabel("2) **Rounds**"), "Rounds");
	assert.equal(headingLabel("IV. Results"), "Results");
	assert.equal(headingLabel("2024 in review"), "2024 in review");
	assert.equal(headingLabel("10"), "10");
});

test("scroll-spy: the last section past the reading line, the last at the bottom", () => {
	const line = { offset: 96, atBottom: false };
	assert.equal(activeSectionIndex([], line), -1);
	assert.equal(activeSectionIndex([200, 900, 1600], line), 0);
	assert.equal(activeSectionIndex([-500, 40, 700], line), 1);
	assert.equal(activeSectionIndex([-1500, -800, 96], line), 2);
	assert.equal(activeSectionIndex([-1500, -800, 300], { offset: 96, atBottom: true }), 2);
});

function suggestion(overrides: Partial<ReportSuggestion>): ReportSuggestion {
	return {
		id: "g1s1",
		section_index: 1,
		section_heading: "Navigating the PICU: Rounds",
		kind: "slides",
		title: "The Choreography of PICU Rounds",
		focus: "A step-by-step breakdown of the day and night shift workflows.",
		state: "open",
		output_id: null,
		...overrides,
	};
}

test("suggestions sit under their section, by heading when the index drifted", () => {
	const sections = splitReport(REPORT).sections;
	const placed = placeSuggestions(sections, [
		suggestion({ id: "a" }),
		suggestion({ id: "b", section_index: 0, section_heading: "Communication & Escalation" }),
		suggestion({ id: "c", section_index: 9, section_heading: "Gone" }),
		suggestion({ id: "d", section_index: 3, section_heading: "Summary", state: "dismissed" }),
	]);
	assert.deepEqual(
		[...placed].map(([index, items]) => [index, items.map((s) => s.id)]),
		[
			[1, ["a"]],
			[2, ["b"]],
			[3, ["c"]],
		]
	);
	assert.equal(placeSuggestions([], [suggestion({})]).size, 0);
});

function output(overrides: Partial<StudioOutput>): StudioOutput {
	return {
		id: 7,
		kind: "flashcards",
		title: "PICU Communication",
		status: "running",
		artifact_id: null,
		source_count: 2,
		options: {},
		created_at: "2026-09-25T00:00:00Z",
		...overrides,
	};
}

test("a card's phase follows the added output's status", () => {
	const added = suggestion({ state: "added", output_id: 7 });
	assert.equal(suggestionPhase(suggestion({}), undefined, false), "open");
	assert.equal(suggestionPhase(suggestion({}), undefined, true), "starting");
	assert.equal(suggestionPhase(added, undefined, false), "starting");
	assert.equal(suggestionPhase(added, output({ status: "queued" }), false), "running");
	assert.equal(suggestionPhase(added, output({}), false), "running");
	assert.equal(suggestionPhase(added, output({ status: "ready", artifact_id: 3 }), false), "ready");
	assert.equal(suggestionPhase(added, output({ status: "ready" }), false), "failed");
	assert.equal(suggestionPhase(added, output({ status: "failed" }), false), "failed");
	assert.equal(suggestionPhase(added, output({ status: "cancelled" }), false), "failed");
});

test("a ready report opens in the studio output view (expanded the first time)", () => {
	const report = output({ kind: "report", status: "ready", artifact_id: 4 });
	assert.deepEqual(studioOpenAction(report), {
		type: "studio-view",
		outputId: report.id,
		artifactId: 4,
	});
});

test("the bridge only accepts well-formed messages from the report", () => {
	assert.deepEqual(
		parseBridgeMessage({ source: BRIDGE_FROM_REPORT, type: "toc", items: ["A", 3, "B"] }),
		{
			source: BRIDGE_FROM_REPORT,
			type: "toc",
			items: ["A", "B"],
		}
	);
	assert.deepEqual(parseBridgeMessage({ source: BRIDGE_FROM_REPORT, type: "active", index: 2 }), {
		source: BRIDGE_FROM_REPORT,
		type: "active",
		index: 2,
	});
	assert.equal(parseBridgeMessage({ source: BRIDGE_FROM_REPORT, type: "active", index: -1 }), null);
	assert.equal(parseBridgeMessage({ source: "other", type: "toc", items: [] }), null);
	assert.equal(parseBridgeMessage("toc"), null);
	const doc = buildHtmlArtifactDocument("<h1>R</h1>", { bridgeScript: REPORT_BRIDGE_SCRIPT });
	assert.ok(doc.includes("<h1>R</h1>\n<script>(function(){"));
	assert.ok(doc.includes("script-src 'unsafe-inline'"));
	assert.ok(!buildHtmlArtifactDocument("<h1>R</h1>").includes("<script>"));
	// The script is valid JavaScript.
	assert.doesNotThrow(() => new Function(REPORT_BRIDGE_SCRIPT));
});

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

test("the contents list numbers sections and marks the one being read", () => {
	const html = render(
		createElement(ReportToc, {
			items: [
				{ index: 0, label: "Introduction" },
				{ index: 1, label: "Navigating the PICU" },
			],
			active: 1,
			onJump: () => undefined,
		})
	);
	assert.match(html, /<nav aria-label="Contents">/);
	assert.match(html, /1\.<\/span><span[^>]*>Introduction/);
	assert.match(
		html,
		/aria-current="location"[^>]*><span[^>]*>2\.<\/span><span[^>]*>Navigating the PICU/
	);
	assert.equal((html.match(/aria-current/g) ?? []).length, 1);
});

test("suggestion cards: the offer, the progress card and a failure", () => {
	const props = {
		suggestion: suggestion({}),
		output: undefined,
		error: false,
		onAdd: () => undefined,
		onDismiss: () => undefined,
	};
	const offer = render(createElement(ReportSuggestionCard, { ...props, phase: "open" }));
	assert.ok(offer.includes("Recommended · Slide Deck"));
	assert.ok(offer.includes("The Choreography of PICU Rounds"));
	assert.ok(offer.includes('aria-label="Add Slide Deck: The Choreography of PICU Rounds"'));
	assert.ok(offer.includes('aria-label="Dismiss suggestion: The Choreography of PICU Rounds"'));

	const running = render(
		createElement(ReportSuggestionCard, {
			...props,
			phase: "running",
			output: output({ kind: "slides", progress: 40, phase: "rendering" }),
		})
	);
	assert.ok(running.startsWith("<output"));
	assert.ok(running.includes("Generating Slide Deck…"));
	assert.ok(running.includes("Rendering · 40%"));

	const failed = render(
		createElement(ReportSuggestionCard, { ...props, phase: "failed", error: true })
	);
	assert.ok(failed.includes("Couldn&#x27;t generate this Slide Deck"));
	assert.ok(failed.includes("Couldn&#x27;t start it."));
	assert.ok(failed.includes("Try again"));
});
