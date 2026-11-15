import {
	type PushPreferences,
	type PushPreferencesChange,
	pushPreferences,
	pushPreferencesChange,
} from "@/contracts/types/push-preferences.types";
import { ValidationError } from "../error";
import { baseApiService } from "./base-api.service";

const PATH = "/api/v1/push/preferences";

class PushPreferencesApiService {
	/** Every kind with its choice, and quiet hours (null while off). */
	get = async (): Promise<PushPreferences> => {
		return baseApiService.get(PATH, pushPreferences) as Promise<PushPreferences>;
	};

	/**
	 * Change some of the choices and/or quiet hours; the answer is all of them
	 * as they now stand. An empty change is refused here rather than sent, as
	 * the server would only answer it with a 422.
	 */
	update = async (change: PushPreferencesChange): Promise<PushPreferences> => {
		const parsed = pushPreferencesChange.safeParse(change);
		if (!parsed.success) {
			throw new ValidationError(parsed.error.issues.map((issue) => issue.message).join(", "));
		}
		return baseApiService.put(PATH, pushPreferences, {
			body: parsed.data,
		}) as Promise<PushPreferences>;
	};
}

export const pushPreferencesApiService = new PushPreferencesApiService();
