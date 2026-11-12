import { z } from "zod";

export const user = z.object({
	id: z.uuid(),
	email: z.email(),
	is_active: z.boolean(),
	is_superuser: z.boolean(),
	is_verified: z.boolean(),
	display_name: z.string().nullish(),
	avatar_url: z.string().nullish(),
});

/**
 * Get current user
 */
export const getMeResponse = user;

/**
 * Update current user request
 */
export const updateUserRequest = z.object({
	display_name: z.string().nullish(),
	avatar_url: z.string().nullish(),
});

/**
 * Update current user response
 */
export const updateUserResponse = user;

/**
 * Account settings (GET and PATCH /api/v1/users/me/settings): one of each
 * per account.
 */
export const userSettings = z.object({
	chat_instructions: z.string(),
	citations_enabled: z.boolean(),
	api_access_enabled: z.boolean(),
});

/**
 * Any subset of the three settings. Strict because the server answers 422 to
 * a key it does not know, so a typo is caught here first. A null
 * chat_instructions clears the instructions; 4000 characters is the server's
 * limit.
 */
export const updateUserSettingsRequest = z
	.object({
		chat_instructions: z.string().max(4000).nullable(),
		citations_enabled: z.boolean(),
		api_access_enabled: z.boolean(),
	})
	.partial()
	.strict();

export type User = z.infer<typeof user>;
export type UserSettings = z.infer<typeof userSettings>;
export type UpdateUserSettingsRequest = z.infer<typeof updateUserSettingsRequest>;
export type GetMeResponse = z.infer<typeof getMeResponse>;
export type UpdateUserRequest = z.infer<typeof updateUserRequest>;
export type UpdateUserResponse = z.infer<typeof updateUserResponse>;
