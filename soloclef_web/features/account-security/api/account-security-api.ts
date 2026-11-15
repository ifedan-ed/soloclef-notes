import { baseApiService } from "@/lib/apis/base-api.service";
import { type Device, deviceListSchema } from "../model/device";
import {
	type PasswordChanged,
	type PasswordState,
	passwordChangedSchema,
	passwordStateSchema,
} from "../model/password-change";

/**
 * The account's own security routes (soloclef_api/internal/signin), the same
 * ones the phone app calls. Each takes a signed-in session only, never an
 * API key: a program acting for the account must not be able to take it over
 * or sign its owner out.
 */
export const accountSecurityApi = {
	passwordState(): Promise<PasswordState> {
		return baseApiService.get("/auth/password", passwordStateSchema) as Promise<PasswordState>;
	},

	/**
	 * Set a new password. `current` is left out for an account that has none
	 * (made through single sign-on); the server then wants a sign-in its
	 * provider proved in the last ten minutes.
	 */
	changePassword(current: string | null, next: string): Promise<PasswordChanged> {
		const body =
			current === null ? { new_password: next } : { current_password: current, new_password: next };
		return baseApiService.post("/auth/change-password", passwordChangedSchema, {
			body,
		}) as Promise<PasswordChanged>;
	},

	devices(): Promise<Device[]> {
		return baseApiService.get("/auth/devices", deviceListSchema) as Promise<Device[]>;
	},

	signOutDevice(sessionId: string): Promise<unknown> {
		return baseApiService.delete(`/auth/devices/${encodeURIComponent(sessionId)}`);
	},

	/** Ends every sign-in, this browser's too; the server clears its cookies. */
	signOutEverywhere(): Promise<unknown> {
		return baseApiService.post("/auth/jwt/logout-all");
	},
};
