import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import {
	INFOGRAPHIC_STYLES,
	STUDIO_TILES,
	VIDEO_STYLES,
} from "@/features/notebooks/lib/studio-catalog";
import {
	appendSuggestion,
	buildStudioRequest,
	initialDialogState,
	languageForRequest,
	needsStyleDescription,
} from "@/features/notebooks/lib/studio-request";

const ctx = { enabledSourceIds: [1, 2, 3], title: "Quiz: Iron", requestId: "r1" };

test("defaults map to the backend's default presets and omit the source list", () => {
	const body = buildStudioRequest(initialDialogState("quiz", [3, 1, 2]), ctx);
	assert.deepEqual(body, {
		kind: "quiz",
		title: "Quiz: Iron",
		request_id: "r1",
		count: "standard",
		difficulty: "medium",
	});
});

test("a narrowed source choice is sent, sorted and deduplicated", () => {
	const state = { ...initialDialogState("mindmap", [1, 2, 3]), sourceIds: [3, 1, 3] };
	assert.deepEqual(buildStudioRequest(state, ctx).source_ids, [1, 3]);
});

test("unknown option values fall back to the preset default", () => {
	const state = initialDialogState("audio", [1, 2, 3]);
	state.options.format = "rap-battle";
	state.options.length = "long";
	const body = buildStudioRequest(state, ctx);
	assert.equal(body.format, "deep_dive");
	assert.equal(body.length, "long");
});

test("report sends file type only for documents, plus its template", () => {
	const doc = initialDialogState("report", [1, 2, 3]);
	doc.options.file_type = "docx";
	doc.templateKey = "study_guide";
	doc.templatePrompt = "  Edited prompt.  ";
	const docBody = buildStudioRequest(doc, ctx);
	assert.equal(docBody.format, "document");
	assert.equal(docBody.file_type, "docx");
	assert.equal(docBody.template_key, "study_guide");
	assert.equal(docBody.template_prompt, "Edited prompt.");

	const interactive = { ...doc, options: { ...doc.options, format: "interactive" } };
	assert.equal("file_type" in buildStudioRequest(interactive, ctx), false);
});

test("focus and language are trimmed and only sent when set", () => {
	const state = {
		...initialDialogState("video", [1, 2, 3]),
		focus: "  the key finding ",
		language: "es",
	};
	const body = buildStudioRequest(state, ctx);
	assert.equal(body.focus, "the key finding");
	assert.equal(body.language, "Spanish");
	assert.equal("focus" in buildStudioRequest(initialDialogState("video", []), ctx), false);
});

test("audio keeps the language tag its voices are keyed by", () => {
	assert.equal(languageForRequest("audio", "pt-BR"), "pt-BR");
	assert.equal(languageForRequest("slides", "ko"), "Korean");
	assert.equal(languageForRequest("quiz", "  "), null);
});

test("suggestions append without repeating", () => {
	assert.equal(appendSuggestion("", "Explain X"), "Explain X");
	assert.equal(appendSuggestion("Focus on Y", "Explain X"), "Focus on Y. Explain X");
	assert.equal(appendSuggestion("Explain X", "Explain X"), "Explain X");
});

test("every tile's default is one of its values", () => {
	for (const tile of STUDIO_TILES) {
		for (const group of tile.groups) {
			assert.ok(group.values.includes(group.defaultValue), `${tile.kind}.${group.field}`);
		}
	}
});

test("the infographic dialog preselects Auto and sends the chosen style", () => {
	const defaults = buildStudioRequest(initialDialogState("infographic", [1, 2, 3]), ctx);
	assert.equal(defaults.style, "auto");
	const state = initialDialogState("infographic", [1, 2, 3]);
	state.options.style = "clay";
	assert.equal(buildStudioRequest(state, ctx).style, "clay");
	state.options.style = "photoreal";
	assert.equal(buildStudioRequest(state, ctx).style, "auto");
});

test("every infographic style card has its bundled rocket preview", () => {
	const group = STUDIO_TILES.find((tile) => tile.kind === "infographic")?.groups.find(
		(candidate) => candidate.field === "style"
	);
	assert.ok(group);
	assert.equal(group.presentation, "gallery");
	assert.equal(group.previewShape, "square");
	assert.equal(INFOGRAPHIC_STYLES.length, 11);
	for (const style of INFOGRAPHIC_STYLES) {
		const preview: string | undefined = group.previews?.[style];
		assert.equal(preview, `/infographic-styles/${style}.webp`);
		assert.ok(existsSync(join(process.cwd(), "public", preview ?? "")), `${style} preview`);
	}
});

test("the infographic dialog sends orientation, style and level of detail", () => {
	const defaults = buildStudioRequest(initialDialogState("infographic", [1, 2, 3]), ctx);
	assert.deepEqual(
		{ orientation: defaults.orientation, style: defaults.style, detail: defaults.detail },
		{ orientation: "landscape", style: "auto", detail: "standard" }
	);
	const state = initialDialogState("infographic", [1, 2, 3]);
	state.options.orientation = "portrait";
	state.options.style = "bento-grid";
	state.options.detail = "concise";
	state.focus = "  Use a blue color theme and highlight the 3 key stats ";
	const body = buildStudioRequest(state, ctx);
	assert.equal(body.orientation, "portrait");
	assert.equal(body.style, "bento-grid");
	assert.equal(body.detail, "concise");
	assert.equal(body.focus, "Use a blue color theme and highlight the 3 key stats");
	state.options.orientation = "panorama";
	assert.equal(buildStudioRequest(state, ctx).orientation, "landscape");
});

test("the video style defaults to Auto and a custom one sends its description", () => {
	const auto = buildStudioRequest(initialDialogState("video", [1, 2, 3]), ctx);
	assert.equal(auto.visual_style, "auto");
	assert.equal("style_prompt" in auto, false);

	const state = initialDialogState("video", [1, 2, 3]);
	state.options.visual_style = "custom";
	assert.equal(needsStyleDescription(state), true);
	state.stylePrompt = "  retro   neon\n grid ";
	assert.equal(needsStyleDescription(state), false);
	const custom = buildStudioRequest(state, ctx);
	assert.equal(custom.visual_style, "custom");
	assert.equal(custom.style_prompt, "retro neon grid");

	state.options.visual_style = "whiteboard";
	const named = buildStudioRequest(state, ctx);
	assert.equal(named.visual_style, "whiteboard");
	assert.equal("style_prompt" in named, false);
});

test("every video style has a bundled preview image", () => {
	const group = STUDIO_TILES.find((tile) => tile.kind === "video")?.groups[1];
	assert.equal(group?.presentation, "gallery");
	for (const style of VIDEO_STYLES) {
		const preview: string | undefined = group?.previews?.[style];
		assert.equal(preview, `/video-styles/${style}.svg`);
		assert.ok(preview && existsSync(join(process.cwd(), "public", preview)), preview);
	}
});
