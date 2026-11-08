import type { Quiz } from "./schema";

type Question = Quiz["questions"][number];

export interface OptionFeedback {
	/** "right": the answer chosen and correct; "correct": the correct one, not chosen; "not_quite": chosen, wrong. */
	verdict: "right" | "correct" | "not_quite" | null;
	text: string | null;
}

/**
 * What shows under an option once the question is answered, as NotebookLM
 * does: a verdict on the chosen and the correct option, and each option's own
 * rationale. Quizzes made before rationales existed have one explanation;
 * it goes under the correct option.
 */
export function optionFeedback(question: Question, index: number, chosen: number): OptionFeedback {
	const correct = index === question.correct_option_index;
	const verdict = correct
		? index === chosen
			? "right"
			: "correct"
		: index === chosen
			? "not_quite"
			: null;
	const rationale = question.option_rationales?.[index];
	const text = rationale ?? (correct ? question.explanation_text : null);
	return { verdict, text };
}
