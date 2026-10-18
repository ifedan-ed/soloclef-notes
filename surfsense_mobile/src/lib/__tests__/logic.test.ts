/**
 * @jest-environment node
 */
import { flashcardDeckSchema, quizSchema, quizScore, shuffled } from "../artifacts";
import { countNodes, parseMindMap } from "../markdown/mindmap";
import { inlineText, parseInline, parseMarkdown } from "../markdown/parse";
import { formatDuration, isInFlight, knownPhase, outputsRefetchInterval } from "../studio/catalog";
import {
	appendSuggestion,
	buildStudioRequest,
	initialDialogState,
	languageForRequest,
	needsStyleDescription,
	newRequestId,
	suggestionVariant,
	suggestionWindow,
	windowCount,
} from "../studio/request";
import { contentSearchQuery, homeSections, relativeTime, snippetParts, withToggled } from "../util/notebooks";
import { acceptedExtensions, fileExtension, linkSource, normalizeLink, resolveParserChoice } from "../util/uploads";
import type { EtlCapabilities, Notebook, NotebookSource } from "../api/schemas";

// eslint-disable-next-line @typescript-eslint/no-require-imports -- the config plugin is CommonJS
const { addReleaseSigning } = require("../../../plugins/with-release-signing");

function notebook(id: number, title: string, updated: string, featured = false): Notebook {
	return {
		id,
		workspace_id: 1,
		title,
		icon: null,
		created_at: updated,
		updated_at: updated,
		source_count: 0,
		enabled_source_count: 0,
		chat_goal: "default",
		response_length: "default",
		featured,
	};
}

describe("home sections (web: home-filter.ts)", () => {
	const list = [
		notebook(1, "Asthma QI", "2026-09-01T00:00:00Z"),
		notebook(2, "Bronchiolitis", "2026-09-03T00:00:00Z", true),
		notebook(3, "asthma guidelines", "2026-09-02T00:00:00Z"),
	];
	it("orders newest first, featured separately in All", () => {
		const all = homeSections(list, "all", "");
		expect(all.featured.map((n) => n.id)).toEqual([2]);
		expect(all.notebooks.map((n) => n.id)).toEqual([2, 3, 1]);
	});
	it("searches titles case-insensitively; Recent has no featured row", () => {
		const recent = homeSections(list, "recent", "ASTHMA");
		expect(recent.featured).toEqual([]);
		expect(recent.notebooks.map((n) => n.id)).toEqual([3, 1]);
	});
});

describe("search inside notebooks (web: lib/content-search.ts)", () => {
	it("sends only queries of two or more characters, whitespace collapsed", () => {
		expect(contentSearchQuery(" a ")).toBe("");
		expect(contentSearchQuery("  school   nurses ")).toBe("school nurses");
	});

	it("cuts a snippet into highlighted runs, tolerating bad ranges", () => {
		const parts = snippetParts("asthma and asthmatics", [
			[11, 17],
			[0, 6],
			[3, 5],
			[30, 40],
			[8, 8],
		]);
		expect(parts).toEqual([
			{ text: "asthma", hit: true, start: 0 },
			{ text: " and ", hit: false, start: 6 },
			{ text: "asthma", hit: true, start: 11 },
			{ text: "tics", hit: false, start: 17 },
		]);
	});
});

describe("sources", () => {
	const source = (id: number, enabled: boolean): NotebookSource => ({
		id,
		kind: "document",
		target_id: id * 10,
		title: `S${id}`,
		document_type: "FILE",
		status: "ready",
		enabled,
		added_at: "2026-09-01T00:00:00Z",
	});
	it("toggles one or all optimistically", () => {
		const list = [source(1, true), source(2, false)];
		expect(withToggled(list, 2, true).map((s) => s.enabled)).toEqual([true, true]);
		expect(withToggled(list, "all", false).map((s) => s.enabled)).toEqual([false, false]);
		expect(list[1].enabled).toBe(false);
	});
});

describe("relative time", () => {
	it("is coarse and never in the future", () => {
		const now = new Date("2026-09-24T12:00:00Z");
		expect(relativeTime("2026-09-21T12:00:00Z", "en", now)).toMatch(/3 days ago/);
		expect(relativeTime("2026-09-25T12:00:00Z", "en", now)).toMatch(/now|0 seconds/);
	});
});

describe("uploads (web: parser-choice.ts)", () => {
	const capabilities: EtlCapabilities = {
		default_parser: "cloud",
		parsers: [
			{
				parser: "local",
				service: "PYMUPDF4LLM",
				available: true,
				unavailable_reason: null,
				page_multiplier: 0,
				document_extensions: [".pdf"],
			},
			{
				parser: "cloud",
				service: "LLAMACLOUD",
				available: true,
				unavailable_reason: null,
				page_multiplier: 3,
				document_extensions: [".pdf", ".docx", ".pptx"],
			},
		],
		always_handled: { plaintext: [".md", ".txt"], direct_convert: [".csv"], audio: [".mp3"], image: [".png"] },
		page_credit_billing_enabled: false,
	};
	it("defaults to the server's parser and falls back when one is unavailable", () => {
		expect(resolveParserChoice(capabilities, undefined).effectiveParser).toBe("cloud");
		expect(resolveParserChoice(capabilities, "local").effectiveParser).toBe("local");
		const cloudDown = {
			...capabilities,
			parsers: capabilities.parsers.map((p) =>
				p.parser === "cloud" ? { ...p, available: false, unavailable_reason: "missing_api_key" } : p
			),
		};
		expect(resolveParserChoice(cloudDown, undefined).effectiveParser).toBe("local");
		expect(resolveParserChoice(undefined, "local").effectiveParser).toBeUndefined();
		expect(resolveParserChoice(capabilities, undefined).options[1].costMultiplier).toBeNull();
	});
	it("accepts what the chosen parser reads, never images without vision", () => {
		const local = acceptedExtensions(capabilities, "local");
		expect(local.has(".pdf")).toBe(true);
		expect(local.has(".docx")).toBe(false);
		expect(local.has(".md")).toBe(true);
		expect(local.has(".png")).toBe(false);
		expect(acceptedExtensions(capabilities, "cloud").has(".docx")).toBe(true);
		expect(fileExtension("Report.Final.PDF")).toBe(".pdf");
		expect(fileExtension(".env")).toBe("");
	});
	it("normalizes pasted links and spots YouTube", () => {
		expect(normalizeLink("example.org/page")).toBe("https://example.org/page");
		expect(normalizeLink("javascript:alert(1)")).toBeNull();
		expect(normalizeLink("not a link")).toBeNull();
		expect(normalizeLink("ftp://example.org")).toBeNull();
		expect(linkSource("https://www.youtube.com/watch?v=x")).toBe("youtube");
		expect(linkSource("https://youtu.be/x")).toBe("youtube");
		expect(linkSource("https://example.org")).toBe("web");
	});
});

describe("studio request (web: studio-request.ts)", () => {
	it("sends only known presets, and sources only when they differ from the enabled set", () => {
		const state = initialDialogState("audio", [3, 1]);
		state.options.format = "nonsense";
		const body = buildStudioRequest(state, { enabledSourceIds: [1, 3], title: "Audio Overview: X", requestId: "r1" });
		expect(body).toEqual({
			kind: "audio",
			title: "Audio Overview: X",
			request_id: "r1",
			format: "deep_dive",
			length: "default",
		});
		const narrowed = buildStudioRequest(
			{ ...state, sourceIds: [3] },
			{ enabledSourceIds: [1, 3], title: "t", requestId: "r" }
		);
		expect(narrowed.source_ids).toEqual([3]);
	});
	it("drops file_type for interactive reports and keeps the template", () => {
		const state = { ...initialDialogState("report", [1]), templateKey: "briefing_doc", templatePrompt: "  Brief it  " };
		state.options.format = "interactive";
		const body = buildStudioRequest(state, { enabledSourceIds: [1], title: "t", requestId: "r" });
		expect(body.file_type).toBeUndefined();
		expect(body.template_key).toBe("briefing_doc");
		expect(body.template_prompt).toBe("Brief it");
	});
	it("needs a description for a custom video style and trims it", () => {
		const state = initialDialogState("video", [1]);
		state.options.visual_style = "custom";
		expect(needsStyleDescription(state)).toBe(true);
		state.stylePrompt = "  neon   grid ";
		expect(needsStyleDescription(state)).toBe(false);
		expect(buildStudioRequest(state, { enabledSourceIds: [1], title: "t", requestId: "r" }).style_prompt).toBe(
			"neon grid"
		);
	});
	it("names languages in English except for audio, which takes the tag", () => {
		expect(languageForRequest("report", "es")).toBe("Spanish");
		expect(languageForRequest("report", "pt-BR")).toBe("Portuguese");
		expect(languageForRequest("audio", "es-MX")).toBe("es-MX");
		expect(languageForRequest("quiz", "")).toBeNull();
	});
	it("appends focus ideas without repeating them", () => {
		expect(appendSuggestion("", "Methods")).toBe("Methods");
		expect(appendSuggestion("Focus on dosing", "Methods")).toBe("Focus on dosing. Methods");
		expect(appendSuggestion("Methods", "Methods")).toBe("Methods");
	});
	it("rotates three ideas at a time through the pool", () => {
		const pool = ["a", "b", "c", "d", "e"];
		expect(suggestionWindow(pool, ["x", "y"], 0)).toEqual({ suggestions: ["a", "b", "c"], placeholder: "x" });
		expect(suggestionWindow(pool, ["x", "y"], 1)).toEqual({ suggestions: ["d", "e", "a"], placeholder: "y" });
		expect(windowCount(5)).toBe(2);
		expect(suggestionVariant("quiz", { difficulty: "hard" })).toBe("hard");
		expect(suggestionVariant("mindmap", { detail: "overview" })).toBe("");
	});
	it("makes 32-character request ids", () => {
		expect(newRequestId()).toMatch(/^[0-9a-z]{32}$/i);
	});
	it("polls only while something generates", () => {
		expect(outputsRefetchInterval(["ready", "running"])).toBe(4000);
		expect(outputsRefetchInterval(["ready", "failed"])).toBe(false);
		expect(isInFlight("queued")).toBe(true);
		expect(knownPhase("narrating")).toBe("narrating");
		expect(knownPhase("mystery")).toBeNull();
		expect(formatDuration(487)).toBe("8:07");
		expect(formatDuration(3725)).toBe("1:02:05");
	});
});

describe("markdown", () => {
	it("reads headings, lists, code, quotes, rules and tables", () => {
		const blocks = parseMarkdown(
			"# Title\n\nPara **bold** and *em* with `code` [link](https://x.org).\n\n- one\n  - nested\n1. first\n\n```py\nprint(1)\n```\n> quoted\n\n---\n| a | b |\n|---|---|\n| 1 | 2 \\| 3 |"
		);
		expect(blocks.map((b) => b.type)).toEqual(["heading", "paragraph", "list", "code", "quote", "hr", "table"]);
		const list = blocks[2];
		expect(list.type === "list" && list.items.map((i) => [i.level, i.ordered])).toEqual([
			[0, false],
			[1, false],
			[0, true],
		]);
		const table = blocks[6];
		expect(table.type === "table" && inlineText(table.rows[0][1])).toBe("2 | 3");
	});
	it("keeps citation tokens as text for the renderer and links bare URLs", () => {
		const inline = parseInline("See [citation:12] and https://example.org/a.");
		expect(inline).toEqual([
			{ type: "text", text: "See [citation:12] and " },
			{ type: "link", href: "https://example.org/a", children: [{ type: "text", text: "https://example.org/a" }] },
			{ type: "text", text: "." },
		]);
	});
	it("leaves unclosed markers and snake_case alone", () => {
		expect(inlineText(parseInline("2 * 3 = 6 and snake_case_name"))).toBe("2 * 3 = 6 and snake_case_name");
	});
});

describe("mind map", () => {
	it("nests headings and list items like markmap", () => {
		const root = parseMindMap("# Asthma\n## Triggers\n- Viral\n  - RSV\n- Allergens\n## Treatment\n- SABA");
		expect(root?.label).toBe("Asthma");
		expect(root?.children.map((c) => c.label)).toEqual(["Triggers", "Treatment"]);
		expect(root?.children[0].children.map((c) => c.label)).toEqual(["Viral", "Allergens"]);
		expect(root?.children[0].children[0].children[0].label).toBe("RSV");
		expect(countNodes(root!)).toBe(7);
	});
	it("uses the title as the centre when there is no single top heading", () => {
		const root = parseMindMap("## A\n## B", "Map");
		expect(root?.label).toBe("Map");
		expect(root?.children).toHaveLength(2);
		expect(parseMindMap("")).toBeNull();
	});
});

describe("study artifacts", () => {
	it("validates decks and quizzes as the web draws them", () => {
		expect(
			flashcardDeckSchema.safeParse({ schema_version: 1, title: "D", cards: [{ front_text: "Q", back_text: "A" }] })
				.success
		).toBe(true);
		const quiz = {
			schema_version: 1,
			title: "Q",
			questions: [
				{ question_text: "?", options: ["a", "b", "c", "d"], correct_option_index: 2, explanation_text: "because" },
			],
		};
		expect(quizSchema.safeParse(quiz).success).toBe(true);
		expect(
			quizSchema.safeParse({ ...quiz, questions: [{ ...quiz.questions[0], correct_option_index: 4 }] }).success
		).toBe(false);
		expect(quizScore(quizSchema.parse(quiz), [2])).toBe(1);
		expect(quizScore(quizSchema.parse(quiz), [null])).toBe(0);
	});
	it("shuffles a copy", () => {
		const items = [1, 2, 3, 4];
		const out = shuffled(items, () => 0);
		expect(out.sort()).toEqual([1, 2, 3, 4]);
		expect(items).toEqual([1, 2, 3, 4]);
	});
});

describe("release signing plugin", () => {
	const gradle = `android {
    signingConfigs {
        debug {
            storeFile file('debug.keystore')
        }
    }
    buildTypes {
        debug {
            signingConfig signingConfigs.debug
        }
        release {
            signingConfig signingConfigs.debug
        }
    }
}`;
	it("adds a property-driven release config and keeps debug for debug builds", () => {
		const out = addReleaseSigning(gradle);
		expect(out).toContain("storeFile file(SURFSENSE_RELEASE_STORE_FILE)");
		expect(out).toContain(
			"signingConfig project.hasProperty('SURFSENSE_RELEASE_STORE_FILE') ? signingConfigs.release : signingConfigs.debug"
		);
		expect(out.match(/signingConfig signingConfigs\.debug/g)).toHaveLength(1);
		expect(addReleaseSigning(out)).toBe(out);
	});
});
