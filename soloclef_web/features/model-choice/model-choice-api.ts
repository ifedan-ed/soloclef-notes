import { baseApiService } from "@/lib/apis/base-api.service";
import {
	MODEL_CHOICE_PATHS,
	MODEL_ROLES_PATH,
	type ModelChoice,
	type ModelJob,
	type ModelRole,
	type ModelRoles,
	modelChoiceSchema,
	modelRolesSchema,
} from "./model-choice";

// Every setting here is the account's: the server takes the account from
// the session, so no path names an owner.
export const modelChoiceApi = {
	read: (job: ModelJob): Promise<ModelChoice> =>
		baseApiService.get(MODEL_CHOICE_PATHS[job], modelChoiceSchema),

	/** `model` is always sent, as a name or as null for the default. */
	choose: (job: ModelJob, model: string | null): Promise<ModelChoice> =>
		baseApiService.put(MODEL_CHOICE_PATHS[job], modelChoiceSchema, { body: { model } }),

	readRoles: (): Promise<ModelRoles> => baseApiService.get(MODEL_ROLES_PATH, modelRolesSchema),

	/** Only the role named is sent: the server leaves the other alone. */
	chooseRole: (role: ModelRole, model: string | null): Promise<ModelRoles> =>
		baseApiService.put(MODEL_ROLES_PATH, modelRolesSchema, { body: { [role]: model } }),
};
