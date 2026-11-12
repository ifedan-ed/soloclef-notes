import type { ChatModel } from "@/features/chat-model/chat-model";
import { isOwnModelId, optionLabel } from "@/features/chat-model/model-option";

/**
 * Words for an automation's model fields. A model is the id
 * GET /api/v1/users/me/chat-model lists it under (a proxy name, or "own:<n>"
 * for a model of the account's own connection), named by its label;
 * "none chosen" is null and means a wider default decides at run time.
 */

/**
 * What the automation-wide "none chosen" option stands for: the account's own
 * chat model, named, so the person sees which model that is today. It is read
 * again when each run starts, so the name here is "currently", not a promise.
 */
export function accountModelLine(chatModel: ChatModel | undefined): string {
	if (!chatModel) return "Whichever model answers your chat when the run starts";
	const label = optionLabel(chatModel.models, chatModel.model);
	return chatModel.available
		? `Currently ${label}`
		: `Currently ${label}, which is no longer offered`;
}

/**
 * Why a chosen name would fail a run, or null when it would not. A name the
 * proxy has stopped listing is kept in the form and said, never swapped for
 * another model: the server refuses it on save with the same meaning.
 */
export function withdrawnSentence(
	chatModel: ChatModel | undefined,
	name: string | null
): string | null {
	if (!chatModel || name === null) return null;
	if (chatModel.models.some((option) => option.id === name)) return null;
	// A gone own model has no label left to name it by, only its number.
	return isOwnModelId(name)
		? "The model from your own connection is no longer offered. Choose another model."
		: `${name} is no longer offered. Choose another model.`;
}
