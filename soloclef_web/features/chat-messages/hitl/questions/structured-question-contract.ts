import { INFOGRAPHIC_STYLES } from "@/features/notebooks/lib/studio-catalog";
import type { PendingInterruptState } from "../approval";
import type { StructuredQuestionAnswer, StructuredQuestionResponse } from "../types";

export type QuestionOption = {
	id: string;
	label: string;
	description?: string;
	preview_asset?: string;
};

export type Question = {
	id: string;
	prompt: string;
	input_type: "single_select" | "multi_select" | "free_text";
	presentation: "list" | "visual_cards";
	required: boolean;
	minimum_selections: number;
	maximum_selections: number;
	options: QuestionOption[];
};

export type StructuredQuestionData = {
	type: "structured_question";
	version: 1;
	title: string;
	message?: string;
	origin: { kind: "preset"; preset_id: string; preset_version: number };
	questions: Question[];
};

export const PREVIEW_ASSETS: Record<string, string> = Object.fromEntries(
	INFOGRAPHIC_STYLES.map((style) => [
		`infographic-style/${style}`,
		`/infographic-styles/${style}.webp`,
	])
);

export function parseStructuredQuestion(
	value: Record<string, unknown>
): StructuredQuestionData | null {
	if (
		value.type !== "structured_question" ||
		value.version !== 1 ||
		typeof value.title !== "string" ||
		!value.origin ||
		typeof value.origin !== "object" ||
		!Array.isArray(value.questions)
	) {
		return null;
	}
	const origin = value.origin as Record<string, unknown>;
	if (
		origin.kind !== "preset" ||
		typeof origin.preset_id !== "string" ||
		typeof origin.preset_version !== "number"
	) {
		return null;
	}
	const questions: Question[] = [];
	for (const raw of value.questions.slice(0, 8)) {
		if (!raw || typeof raw !== "object") return null;
		const question = raw as Record<string, unknown>;
		if (
			typeof question.id !== "string" ||
			typeof question.prompt !== "string" ||
			!["single_select", "multi_select", "free_text"].includes(String(question.input_type))
		) {
			return null;
		}
		const options = Array.isArray(question.options)
			? question.options.slice(0, 12).flatMap<QuestionOption>((rawOption) => {
					if (!rawOption || typeof rawOption !== "object") return [];
					const option = rawOption as Record<string, unknown>;
					if (typeof option.id !== "string" || typeof option.label !== "string") return [];
					return [
						{
							id: option.id,
							label: option.label,
							...(typeof option.description === "string"
								? { description: option.description }
								: {}),
							...(typeof option.preview_asset === "string" && PREVIEW_ASSETS[option.preview_asset]
								? { preview_asset: option.preview_asset }
								: {}),
						},
					];
				})
			: [];
		questions.push({
			id: question.id,
			prompt: question.prompt,
			input_type: question.input_type as Question["input_type"],
			presentation: question.presentation === "visual_cards" ? "visual_cards" : "list",
			required: question.required !== false,
			minimum_selections:
				typeof question.minimum_selections === "number" ? question.minimum_selections : 1,
			maximum_selections:
				typeof question.maximum_selections === "number" ? question.maximum_selections : 1,
			options,
		});
	}
	return questions.length > 0
		? {
				type: "structured_question",
				version: 1,
				title: value.title,
				...(typeof value.message === "string" ? { message: value.message } : {}),
				origin: {
					kind: "preset",
					preset_id: origin.preset_id,
					preset_version: origin.preset_version,
				},
				questions,
			}
		: null;
}

export function isStructuredQuestionInterrupt(interrupt: PendingInterruptState): boolean {
	return parseStructuredQuestion(interrupt.interruptData) !== null;
}

/** The card's answers so far, by question id: option ids, or a free text. */
export type StructuredAnswers = Record<string, string[] | string>;

/**
 * The answers a card opens with. An "auto" option (the infographic's
 * "choose from the material" look) is chosen to begin with, so a person who
 * does not mind can go straight on; any other question starts unanswered.
 */
export function initialStructuredAnswers(data: StructuredQuestionData): StructuredAnswers {
	return Object.fromEntries(
		data.questions.map((question) => {
			if (question.input_type === "free_text") return [question.id, ""];
			const initial = question.options.find((option) => option.id === "auto");
			return [question.id, initial ? [initial.id] : []];
		})
	);
}

/** Why a question's answer cannot be sent yet, or null when it can. */
export function questionProblem(question: Question, answer: string[] | string | undefined) {
	const count = Array.isArray(answer) ? answer.length : String(answer ?? "").trim().length;
	if (question.required && count === 0) return `Answer “${question.prompt}” before continuing.`;
	if (
		Array.isArray(answer) &&
		(answer.length < question.minimum_selections || answer.length > question.maximum_selections)
	) {
		return `Choose the requested number of options for “${question.prompt}”.`;
	}
	return null;
}

/**
 * The first question whose answer cannot be sent, with why, or null when all
 * can. Every question is checked before the card is sent, not just the one on
 * screen: the server needs each answer (the infographic's look and its
 * orientation), and a payload with one missing would be refused.
 */
export function firstQuestionProblem(
	data: StructuredQuestionData,
	answers: StructuredAnswers
): { index: number; message: string } | null {
	for (const [index, question] of data.questions.entries()) {
		const message = questionProblem(question, answers[question.id]);
		if (message) return { index, message };
	}
	return null;
}

/** The response the card sends: every question's answer, in the questions' order. */
export function structuredQuestionAnswer(
	data: StructuredQuestionData,
	answers: StructuredAnswers,
	interruptId: string
): StructuredQuestionResponse {
	return {
		type: "respond",
		preset_id: data.origin.preset_id,
		preset_version: data.origin.preset_version,
		tool_call_id: interruptId,
		answers: data.questions.map<StructuredQuestionAnswer>((question) => {
			const answer = answers[question.id];
			return question.input_type === "free_text"
				? { question_id: question.id, text: String(answer ?? "").trim() }
				: { question_id: question.id, option_ids: Array.isArray(answer) ? answer : [] };
		}),
	};
}

/** The response for a card the person skipped. */
export function structuredQuestionCancel(
	data: StructuredQuestionData,
	interruptId: string
): StructuredQuestionResponse {
	return {
		type: "cancel",
		preset_id: data.origin.preset_id,
		preset_version: data.origin.preset_version,
		tool_call_id: interruptId,
	};
}
