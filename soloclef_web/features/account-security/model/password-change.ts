import { z } from "zod";

/** GET /auth/password: whether to ask for the current password. */
export const passwordStateSchema = z.object({ has_password: z.boolean() });

export type PasswordState = z.infer<typeof passwordStateSchema>;

/** POST /auth/change-password's answer: how many other sign-ins it ended. */
export const passwordChangedSchema = z.object({
	detail: z.string(),
	signed_out_sessions: z.number().int().nonnegative(),
});

export type PasswordChanged = z.infer<typeof passwordChangedSchema>;
