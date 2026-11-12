import { z } from "zod";
import { modelOptionSchema, optionLabel } from "@/features/chat-model/model-option";

/**
 * The model that does one of the account's jobs other than chat, as the
 * server answers it (soloclef_api/internal/modelchoices):
 *
 *   GET, PUT /api/v1/users/me/quick-model    small helper calls
 *   GET, PUT /api/v1/users/me/studio-model   Studio outputs
 *   GET, PUT /api/v1/users/me/model-roles    {vision, image}
 *
 * It is the chat model's shape (features/chat-model) with two differences:
 * `model` and `default` can be null, because a server may name no model for
 * looking at pictures or drawing them. A model is known by its id (the
 * proxy's name, or "own:<n>" for a model of the account's own connection),
 * which is stored and sent; its label is what is shown.
 */
export const modelChoiceSchema = z.object({
	/** The account's own choice; null when it has made none. */
	selected: z.string().nullable(),
	/** What does the job now: the choice, else the default; null when there is neither. */
	model: z.string().nullable(),
	/** Whether the proxy still offers `model`. */
	available: z.boolean(),
	/** What does the job for an account that has chosen none. */
	default: z.string().nullable(),
	/** Every model that may be chosen for the job, the proxy's and the account's own. */
	models: z.array(modelOptionSchema),
});
export type ModelChoice = z.infer<typeof modelChoiceSchema>;

export const modelRolesSchema = z.object({
	vision: modelChoiceSchema,
	image: modelChoiceSchema,
});
export type ModelRoles = z.infer<typeof modelRolesSchema>;

/** The two jobs of /model-roles; also the field each is chosen by. */
export type ModelRole = keyof ModelRoles;

/** The jobs that have a route of their own, and the route. */
export const MODEL_CHOICE_PATHS = {
	quick: "/api/v1/users/me/quick-model",
	studio: "/api/v1/users/me/studio-model",
} as const;
export type ModelJob = keyof typeof MODEL_CHOICE_PATHS;

export const MODEL_ROLES_PATH = "/api/v1/users/me/model-roles";

/**
 * What a picker shows the moment a choice is made, before the server
 * answers: the server's own arithmetic (choice, else default, held to the
 * list). `name` null goes back to the default.
 */
export function withChoice(current: ModelChoice, name: string | null): ModelChoice {
	const model = name ?? current.default;
	return {
		...current,
		selected: name,
		model,
		available: model !== null && current.models.some((option) => option.id === model),
	};
}

/** Whose model the default of a job is, which is how its "no choice" option reads. */
export type DefaultSource = "server" | "chat";

/**
 * The label of the "no choice" option: what the default is today, by name.
 * Quick tasks and Studio default to the model answering the account's chat;
 * vision and image generation to the model the server names, if it names one.
 */
export function defaultLabel(choice: ModelChoice, source: DefaultSource = "server"): string {
	// The chat model a job follows may be one of the account's own, whose id
	// is not a name, so the default is named by its label.
	const named = choice.default === null ? null : optionLabel(choice.models, choice.default);
	if (source === "chat") return `Same as chat model (${named ?? "none"})`;
	return named === null ? "Server default (none set)" : `Server default (${named})`;
}

/**
 * Why the job cannot be done right now, or null when it can. A job with no
 * model at all (nothing chosen, and the server names none) is not a fault:
 * the server simply does not do it until a model is chosen.
 */
export function unavailableSentence(choice: ModelChoice): string | null {
	if (choice.available || choice.model === null) return null;
	return choice.selected !== null
		? `The model you chose, ${optionLabel(choice.models, choice.model)}, is no longer offered. Choose another model.`
		: `The default model, ${optionLabel(choice.models, choice.model)}, is not offered. Choose a model.`;
}
