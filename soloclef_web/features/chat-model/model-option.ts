import { z } from "zod";

/**
 * One entry of a model menu, as every model route answers it
 * (/users/me/chat-model, /quick-model, /studio-model, /model-roles).
 *
 * Two kinds of model share a menu:
 *
 *  - the server's own, which it reaches through its model proxy. Their id is
 *    the proxy's name, and the label is that same name.
 *  - the account's own, a model of an OpenAI-compatible connection the
 *    person added in Model settings, which the server calls directly. Their
 *    id is "own:<n>" (a number the person never needs to read), so the label
 *    is what is shown, with `connection` naming the endpoint's host.
 *
 * The id is still the one string that is stored and sent back; the label is
 * only ever shown.
 */
export const modelOptionSchema = z.object({
	id: z.string(),
	label: z.string(),
	own: z.boolean(),
	// Only an own model has an endpoint to name. Optional so an own entry
	// whose host the server could not read still parses and is still marked.
	connection: z.string().optional(),
});
export type ModelOption = z.infer<typeof modelOptionSchema>;

/** The id shape the server gives a model of the account's own connection. */
const OWN_ID = /^own:\d+$/;

/** Whether a stored or chosen id is one of the account's own connections' models. */
export function isOwnModelId(id: string): boolean {
	return OWN_ID.test(id);
}

/**
 * What a menu shows for an id. A listed id shows its label. An id that is
 * not in the list (a model withdrawn since it was chosen) shows the id
 * itself, which for a proxy model is its name; an own model's id is a bare
 * number, so it is described instead of being shown as "own:42".
 */
export function optionLabel(models: readonly ModelOption[], id: string): string {
	const listed = models.find((option) => option.id === id);
	if (listed) return listed.label;
	return isOwnModelId(id) ? "a model from one of your connections" : id;
}

/** The second line that marks an own model as the account's: whose and where. */
export function ownMark(option: ModelOption): string | null {
	if (!option.own) return null;
	return option.connection ? `Your connection · ${option.connection}` : "Your connection";
}

/**
 * The options matching what was typed, in the server's order; all of them
 * for an empty search. The label and the endpoint are searched as well as
 * the id, because an own model's id says nothing a person would type.
 */
export function matchingOptions(models: readonly ModelOption[], search: string): ModelOption[] {
	const wanted = search.trim().toLowerCase();
	if (!wanted) return [...models];
	return models.filter((option) =>
		[option.id, option.label, option.connection ?? ""].some((text) =>
			text.toLowerCase().includes(wanted)
		)
	);
}
