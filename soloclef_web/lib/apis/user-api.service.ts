import {
	getMeResponse,
	type UpdateUserRequest,
	type UpdateUserSettingsRequest,
	updateUserResponse,
	updateUserSettingsRequest,
	userSettings,
} from "@/contracts/types/user.types";
import { ValidationError } from "../error";
import { baseApiService } from "./base-api.service";

class UserApiService {
	/**
	 * Get current authenticated user
	 */
	getMe = async () => {
		return baseApiService.get(`/users/me`, getMeResponse);
	};

	/**
	 * Update current authenticated user
	 */
	updateMe = async (request: UpdateUserRequest) => {
		return baseApiService.patch(`/users/me`, updateUserResponse, {
			body: request,
		});
	};

	/**
	 * The account's settings: chat instructions, citations, API access.
	 */
	getSettings = async () => {
		return baseApiService.get(`/api/v1/users/me/settings`, userSettings);
	};

	/**
	 * Change any of the account's settings; the answer is all three as they
	 * now stand. Checked here first because the server refuses an unknown key
	 * or over-long instructions with a 422 that names nothing useful to show.
	 */
	updateSettings = async (request: UpdateUserSettingsRequest) => {
		const parsed = updateUserSettingsRequest.safeParse(request);
		if (!parsed.success) {
			const errorMessage = parsed.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}
		return baseApiService.patch(`/api/v1/users/me/settings`, userSettings, {
			body: parsed.data,
		});
	};

	/**
	 * Delete the current account. Locks it out immediately; the erase follows.
	 */
	deleteMe = async () => {
		return baseApiService.delete(`/users/me`);
	};
}

export const userApiService = new UserApiService();
