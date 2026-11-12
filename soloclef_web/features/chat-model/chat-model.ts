import { z } from "zod";
import { type ModelOption, matchingOptions, modelOptionSchema, optionLabel } from "./model-option";

/**
 * The account's chat model as the server answers it
 * (GET and PUT /api/v1/users/me/chat-model).
 *
 * A model is known by one string, its id: the name the model proxy lists it
 * under, or "own:<n>" for a model of the account's own connection. The id is
 * what is stored and sent; the picker shows the entry's label
 * (./model-option).
 */
export const chatModelSchema = z.object({
	/** The account's own choice; null when it has made none. */
	selected: z.string().nullable(),
	/** The model that answers now: the choice, else the server's default. */
	model: z.string(),
	/** Whether the proxy still offers `model`. False means a message would be refused. */
	available: z.boolean(),
	/** The model answering an account that has chosen none. */
	default: z.string(),
	/** Every model that may be chosen, the proxy's and the account's own, sorted by the server. */
	models: z.array(modelOptionSchema),
});
export type ChatModel = z.infer<typeof chatModelSchema>;

/**
 * What the picker shows the moment a choice is made, before the server
 * answers: the same arithmetic the server does (choice, else default, held
 * to the list). `name` null goes back to the server's default.
 */
export function withChoice(current: ChatModel, name: string | null): ChatModel {
	const model = name ?? current.default;
	return {
		...current,
		selected: name,
		model,
		available: current.models.some((option) => option.id === model),
	};
}

/**
 * Why the next message would be refused, or null when it would be answered.
 * The two cases are worded apart because they are fixed by different
 * people: the account's own choice here in the menu, the server's default
 * by whoever runs the server (or by choosing a model here instead).
 */
export function unavailableSentence(chatModel: ChatModel): string | null {
	if (chatModel.available) return null;
	return chatModel.selected !== null
		? `The model you chose, ${optionLabel(chatModel.models, chatModel.model)}, is no longer offered. Choose another model.`
		: `You have not chosen a model, and the server's default model, ${chatModel.default}, is not offered. Choose a model.`;
}

/** Whether going back to the server's default would leave the chat answerable. */
export function defaultIsOffered(chatModel: ChatModel): boolean {
	return chatModel.models.some((option) => option.id === chatModel.default);
}

/** The offered models matching what was typed; all of them for an empty search. */
export function matchingModels(chatModel: ChatModel, search: string): ModelOption[] {
	return matchingOptions(chatModel.models, search);
}

const CHOOSE_FAILED = "The chat model could not be changed. Try again in a moment.";

/**
 * The sentence for a choice the server refused.
 *
 * A model removed from the proxy between the list being read and the click
 * comes back as a validation failure on `model`, whose own message names the
 * model ("The model x is not offered. Choose one from the list."). That
 * message is shown as it is; the envelope's summary of the same failure
 * ("Validation failed: body.model: ...") is not a sentence for a person.
 * Every other refusal (no permission, the model service unreachable) is
 * already one sentence in the error's message.
 */
export function chooseFailureSentence(error: unknown): string {
	if (typeof error !== "object" || error === null) return CHOOSE_FAILED;
	const { fields, message } = error as {
		fields?: Array<{ loc?: unknown; msg?: unknown }>;
		message?: unknown;
	};
	if (Array.isArray(fields)) {
		const onModel = fields.find(
			(field) => Array.isArray(field.loc) && field.loc.at(-1) === "model"
		);
		if (typeof onModel?.msg === "string" && onModel.msg) return onModel.msg;
	}
	return typeof message === "string" && message ? message : CHOOSE_FAILED;
}
