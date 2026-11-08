import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { createTranslator } from "next-intl";
import { optionFeedback } from "@/features/artifacts/rendering/formats/quiz/rationale";
import { QuizQuestionSchema } from "@/features/artifacts/rendering/formats/quiz/schema";
import {
	flashcardExplainMessage,
	MAX_PASSAGE_CHARS,
	passageForChat,
	quizExplainMessage,
	reportPassageMessage,
} from "@/features/notebooks/lib/explain-messages";
import {
	defaultOutputSize,
	forgetOutputSizes,
	outputSize,
	rememberOutputSize,
} from "@/features/notebooks/lib/output-size";

const LOCALES = ["en", "es", "pt", "hi", "ko", "zh"];

function messages(locale: string) {
	return JSON.parse(readFileSync(join(process.cwd(), "messages", `${locale}.json`), "utf8"));
}

function explainT(locale: string) {
	const t = createTranslator({ locale, messages: messages(locale), namespace: "explain" });
	return (key: string, values: Record<string, string>) =>
		t(key as Parameters<typeof t>[0], values as never);
}

test("the flashcard Explain message is NotebookLM's, word for word", () => {
	assert.equal(
		flashcardExplainMessage(explainT("en"), {
			front: " What does the Calvin cycle fix? ",
			back: "Carbon dioxide, into sugar",
		}),
		"I'm reviewing flashcards based on the source material and I'd like to expand my understanding of one of them.\n\n" +
			'On the front it reads: "What does the Calvin cycle fix?"\n' +
			'The answer on the back reads: "Carbon dioxide, into sugar"\n\n' +
			"Explain this topic in more detail."
	);
});

test("the quiz Explain message says whether the chosen answer was correct", () => {
	const t = explainT("en");
	const question = {
		question: "Which gas is fixed?",
		options: ["Oxygen", "Carbon dioxide", "Nitrogen", "Argon"],
		correct: 1,
	};
	assert.equal(
		quizExplainMessage(t, { ...question, chosen: 1 }),
		'I am taking a quiz on this material and was given this question: "Which gas is fixed?"\n\n' +
			'I chose the correct answer: "Carbon dioxide"\n\n' +
			"Help me understand more about this topic."
	);
	assert.equal(
		quizExplainMessage(t, { ...question, chosen: 2 }),
		'I am taking a quiz on this material and was given this question: "Which gas is fixed?"\n\n' +
			'I chose "Nitrogen", but the correct answer is "Carbon dioxide"\n\n' +
			"Help me understand more about this topic."
	);
});

test("LaTeX and braces in card text pass through untouched", () => {
	const message = flashcardExplainMessage(explainT("en"), {
		front: String.raw`Solve \(x^{2} = 4\)`,
		back: "{x = ±2}",
	});
	assert.ok(message.includes(String.raw`"Solve \(x^{2} = 4\)"`));
	assert.ok(message.includes('"{x = ±2}"'));
});

test("a report passage is folded, capped and quoted with the report's title", () => {
	assert.equal(passageForChat("  one\n\n two\tthree "), "one two three");
	const long = passageForChat("word ".repeat(1000));
	assert.ok(long.length <= MAX_PASSAGE_CHARS && long.endsWith("…"));
	assert.equal(
		reportPassageMessage(explainT("en"), {
			title: "PICU Orientation",
			passage: "Call the\nfellow first.",
		}),
		'I\'m reading the report "PICU Orientation". This part says: "Call the fellow first."\n\n' +
			"Explain this in more detail."
	);
});

test("every locale words every Explain message with all of its values", () => {
	for (const locale of LOCALES) {
		const t = explainT(locale);
		const card = flashcardExplainMessage(t, { front: "FRONT", back: "BACK" });
		assert.ok(card.includes("FRONT") && card.includes("BACK"), `${locale} flashcard`);
		const base = { question: "QUESTION", options: ["A0", "A1", "A2", "A3"], correct: 0 };
		const right = quizExplainMessage(t, { ...base, chosen: 0 });
		assert.ok(right.includes("QUESTION") && right.includes("A0"), `${locale} quiz_correct`);
		const wrong = quizExplainMessage(t, { ...base, chosen: 3 });
		assert.ok(
			wrong.includes("QUESTION") && wrong.includes("A3") && wrong.includes("A0"),
			`${locale} quiz_wrong`
		);
		const passage = reportPassageMessage(t, { title: "TITLE", passage: "PASSAGE" });
		assert.ok(passage.includes("TITLE") && passage.includes("PASSAGE"), `${locale} report`);
		for (const key of ["button", "flashcard_label", "quiz_label", "selection_label"]) {
			assert.equal(typeof messages(locale).explain[key], "string", `${locale} explain.${key}`);
		}
		for (const key of [
			"previous",
			"next",
			"finish",
			"skip",
			"right",
			"not_quite",
			"correct_answer",
		]) {
			assert.equal(typeof messages(locale).quiz[key], "string", `${locale} quiz.${key}`);
		}
		const view = createTranslator({
			locale,
			messages: messages(locale),
			namespace: "notebooks.output_view",
		});
		assert.ok(view("view_prompt", { count: 3 }).includes("3"), `${locale} view_prompt`);
		// The retired Good / Bad content rating left no strings behind.
		const outputView = (messages(locale).notebooks as Record<string, Record<string, unknown>>)
			.output_view;
		for (const gone of ["good", "bad", "thanks"]) {
			assert.equal(outputView[gone], undefined, `${locale} output_view.${gone}`);
		}
	}
});

const baseQuestion = {
	question_text: "Q?",
	options: ["A", "B", "C", "D"],
	correct_option_index: 1,
	explanation_text: "B is right because.",
};

test("quizzes without rationales still parse; with them, exactly four", () => {
	assert.equal(QuizQuestionSchema.safeParse(baseQuestion).success, true);
	assert.equal(
		QuizQuestionSchema.safeParse({ ...baseQuestion, option_rationales: ["a", "b", "c", "d"] })
			.success,
		true
	);
	assert.equal(
		QuizQuestionSchema.safeParse({ ...baseQuestion, option_rationales: ["a", "b", "c"] }).success,
		false
	);
	assert.equal(
		QuizQuestionSchema.safeParse({ ...baseQuestion, option_rationales: ["a", "", "c", "d"] })
			.success,
		false
	);
	assert.equal(
		QuizQuestionSchema.safeParse({ ...baseQuestion, option_rationales: null }).success,
		false
	);
});

test("each option shows its verdict and rationale once answered", () => {
	const withRationales = QuizQuestionSchema.parse({
		...baseQuestion,
		option_rationales: ["A is wrong", "B is right", "C is wrong", "D is wrong"],
	});
	// Answered correctly.
	assert.deepEqual(optionFeedback(withRationales, 1, 1), { verdict: "right", text: "B is right" });
	assert.deepEqual(optionFeedback(withRationales, 0, 1), { verdict: null, text: "A is wrong" });
	// Answered wrongly.
	assert.deepEqual(optionFeedback(withRationales, 2, 2), {
		verdict: "not_quite",
		text: "C is wrong",
	});
	assert.deepEqual(optionFeedback(withRationales, 1, 2), {
		verdict: "correct",
		text: "B is right",
	});

	// An old quiz: its one explanation goes under the correct option.
	const old = QuizQuestionSchema.parse(baseQuestion);
	assert.deepEqual(optionFeedback(old, 1, 1), { verdict: "right", text: "B is right because." });
	assert.deepEqual(optionFeedback(old, 3, 1), { verdict: null, text: null });
	assert.deepEqual(optionFeedback(old, 3, 3), { verdict: "not_quite", text: null });
});

test("reports open expanded the first time, the rest in the column; choices stick", () => {
	forgetOutputSizes();
	assert.equal(defaultOutputSize("report"), "expanded");
	for (const kind of ["quiz", "flashcards", "mindmap", "slides", "data_table", "video"] as const) {
		assert.equal(defaultOutputSize(kind), "column");
	}
	assert.equal(outputSize(7, "report"), "expanded");
	rememberOutputSize(7, "column");
	assert.equal(outputSize(7, "report"), "column");
	rememberOutputSize(8, "expanded");
	assert.equal(outputSize(8, "quiz"), "expanded");
	assert.equal(outputSize(9, "quiz"), "column");
	forgetOutputSizes();
	assert.equal(outputSize(7, "report"), "expanded");
});
