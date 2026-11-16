/**
 * The words for what an automation's task may read and where its result goes,
 * shared by the automation form (task-reach-fields.tsx) and the chat's draft
 * card (automation-draft-form.tsx). They are two editors of the same three
 * fields, so they label them the same way; until they shared this module the
 * card said "Answer only from" and "Keep each result as a note in" where the
 * form said "Notebook" and "Save results to notebook".
 *
 * Worded for a run nobody watches: it can search the web, but not read a page
 * in full (reading one asks first, and nobody is there to ask), so no hint
 * promises it.
 */

export type ReachValue = "documents" | "all" | "web";

export const CAN_USE_LABEL = "Can use";

export const REACH_OPTIONS: { value: ReachValue; label: string; hint: string }[] = [
	{ value: "documents", label: "Your documents", hint: "Searches your documents and notes." },
	{
		value: "all",
		label: "Your documents and the web",
		hint: "Also web search (Exa or Tavily, if connected), PubMed, arXiv and preprints.",
	},
	{ value: "web", label: "The web only", hint: "Web and research search, not your documents." },
];

/** The option for a value, or the first (documents) for one not listed. */
export function reachOption(value: string) {
	return REACH_OPTIONS.find((option) => option.value === value) ?? REACH_OPTIONS[0];
}

/** The notebook a task answers from: any, or one whose sources alone it reads. */
export const ANSWER_FROM = {
	label: "Answer from",
	any: "Any notebook",
	hint: (picked: boolean) =>
		picked ? "Only that notebook's switched-on sources." : "Any of your notebooks.",
} as const;

/** The notebook each run's result is kept in as a note, or none. */
export const SAVE_RESULT_TO = {
	label: "Save each result as a note in",
	none: "Don't save",
	hint: (picked: boolean) =>
		picked ? "Each run's result is added there as a note." : "Results stay on the run's page.",
} as const;

/** A notebook still picked after it was deleted, until another is chosen. */
export const DELETED_NOTEBOOK = "A deleted notebook";

/** The switch that emails each run's result to the account's address. */
export const EMAIL_RESULT_LABEL = "Email me each result";
