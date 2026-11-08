/**
 * The prefilled chat messages behind the "Explain" buttons on Studio
 * outputs, worded as NotebookLM's. ``t`` is the ``explain`` namespace's
 * translator, so each locale keeps its own phrasing; the card, question and
 * option texts go in as they are (LaTeX included).
 */
export type ExplainTranslate = (key: string, values: Record<string, string>) => string;

export const MAX_PASSAGE_CHARS = 1500;

export function flashcardExplainMessage(
	t: ExplainTranslate,
	card: { front: string; back: string }
): string {
	return t("flashcard", { front: card.front.trim(), back: card.back.trim() });
}

export function quizExplainMessage(
	t: ExplainTranslate,
	question: { question: string; options: readonly string[]; correct: number; chosen: number }
): string {
	const values = {
		question: question.question.trim(),
		option: (question.options[question.chosen] ?? "").trim(),
	};
	if (question.chosen === question.correct) return t("quiz_correct", values);
	return t("quiz_wrong", {
		...values,
		correct: (question.options[question.correct] ?? "").trim(),
	});
}

/** A selected passage, whitespace folded and cut to a chat-sized quote. */
export function passageForChat(text: string, max = MAX_PASSAGE_CHARS): string {
	const folded = text.replace(/\s+/g, " ").trim();
	if (folded.length <= max) return folded;
	return `${folded.slice(0, max - 1).trimEnd()}…`;
}

export function reportPassageMessage(
	t: ExplainTranslate,
	report: { title: string; passage: string }
): string {
	return t("report_passage", {
		title: report.title.trim(),
		passage: passageForChat(report.passage),
	});
}
