import assert from "node:assert/strict";
import test from "node:test";
import {
	firstQuestionProblem,
	initialStructuredAnswers,
	parseStructuredQuestion,
	type StructuredQuestionData,
	structuredQuestionAnswer,
	structuredQuestionCancel,
} from "@/features/chat-messages/hitl/questions/structured-question-contract";
import { INFOGRAPHIC_STYLES } from "@/features/notebooks/lib/studio-catalog";

// Run with: pnpm exec tsx --test tests/unit/hitl/structured-question.test.ts

const STYLE_LABELS: Record<string, string> = {
	auto: "Auto-select",
	"sketch-note": "Sketch Note",
	editorial: "Editorial",
	instructional: "Instructional",
	"bento-grid": "Bento Grid",
	scientific: "Scientific",
	professional: "Professional",
};

/** The chat's infographic question, as the server asks it (preset version 2): the look, then the shape. */
const payload = {
	type: "structured_question",
	version: 1,
	title: "Infographic",
	origin: {
		kind: "preset",
		preset_id: "infographic.visual-style",
		preset_version: 2,
	},
	questions: [
		{
			id: "visual-style",
			prompt: "Which style?",
			input_type: "single_select",
			presentation: "visual_cards",
			required: true,
			minimum_selections: 1,
			maximum_selections: 1,
			options: INFOGRAPHIC_STYLES.map((id) => ({
				id: id as string,
				label: STYLE_LABELS[id],
				preview_asset: `infographic-style/${id}`,
			})),
		},
		{
			id: "orientation",
			prompt: "Which orientation?",
			input_type: "single_select",
			presentation: "list",
			required: true,
			minimum_selections: 1,
			maximum_selections: 1,
			options: [
				{ id: "landscape", label: "Landscape" },
				{ id: "portrait", label: "Portrait" },
				{ id: "square", label: "Square" },
			],
		},
	],
} satisfies Record<string, unknown>;

function parsed(): StructuredQuestionData {
	const data = parseStructuredQuestion(payload);
	assert.ok(data);
	return data;
}

test("parses the infographic's two questions, with every style's preview", () => {
	const data = parsed();

	assert.equal(data.origin.preset_id, "infographic.visual-style");
	assert.equal(data.origin.preset_version, 2);
	assert.deepEqual(
		data.questions.map((question) => [question.id, question.presentation]),
		[
			["visual-style", "visual_cards"],
			["orientation", "list"],
		]
	);
	assert.deepEqual(
		data.questions[0].options.map((option) => option.preview_asset),
		INFOGRAPHIC_STYLES.map((id) => `infographic-style/${id}`)
	);
	assert.deepEqual(
		data.questions[1].options.map((option) => option.label),
		["Landscape", "Portrait", "Square"]
	);
});

test("a removed style's preview is not allow-listed, so the option stays without it", () => {
	const old = structuredClone(payload);
	old.questions[0].options[1] = {
		id: "kawaii",
		label: "Kawaii",
		preview_asset: "infographic-style/kawaii",
	};
	const data = parseStructuredQuestion(old);
	assert.equal(data?.questions[0].options[1].id, "kawaii");
	assert.equal(data?.questions[0].options[1].preview_asset, undefined);
});

test("drops non-allowlisted preview assets without dropping the option", () => {
	const unsafe = structuredClone(payload);
	const first = unsafe.questions[0].options[0] as { preview_asset?: string };
	first.preview_asset = "https://example.com/tracker.png";

	const data = parseStructuredQuestion(unsafe);

	assert.equal(data?.questions[0].options.length, INFOGRAPHIC_STYLES.length);
	assert.equal(data?.questions[0].options[0].preview_asset, undefined);
});

test("rejects unknown variants and malformed origins", () => {
	assert.equal(parseStructuredQuestion({ ...payload, type: "approval" }), null);
	assert.equal(
		parseStructuredQuestion({
			...payload,
			origin: { kind: "agent", preset_id: "x", preset_version: 1 },
		}),
		null
	);
});

test("the look starts on Auto and the orientation starts unanswered", () => {
	assert.deepEqual(initialStructuredAnswers(parsed()), {
		"visual-style": ["auto"],
		orientation: [],
	});
});

test("nothing is sent until both questions are answered, and the card turns to the missing one", () => {
	const data = parsed();
	const problem = firstQuestionProblem(data, initialStructuredAnswers(data));
	assert.equal(problem?.index, 1);
	assert.match(problem?.message ?? "", /Which orientation\?/);
	assert.equal(
		firstQuestionProblem(data, { "visual-style": ["bento-grid"], orientation: ["portrait"] }),
		null
	);
	// Two looks for one infographic is not an answer.
	assert.equal(
		firstQuestionProblem(data, {
			"visual-style": ["bento-grid", "editorial"],
			orientation: ["portrait"],
		})?.index,
		0
	);
});

test("the response carries both answers, in the questions' order", () => {
	const data = parsed();
	assert.deepEqual(
		structuredQuestionAnswer(
			data,
			{ "visual-style": ["scientific"], orientation: ["square"] },
			"call_1"
		),
		{
			type: "respond",
			preset_id: "infographic.visual-style",
			preset_version: 2,
			tool_call_id: "call_1",
			answers: [
				{ question_id: "visual-style", option_ids: ["scientific"] },
				{ question_id: "orientation", option_ids: ["square"] },
			],
		}
	);
	assert.deepEqual(structuredQuestionCancel(data, "call_1"), {
		type: "cancel",
		preset_id: "infographic.visual-style",
		preset_version: 2,
		tool_call_id: "call_1",
	});
});
