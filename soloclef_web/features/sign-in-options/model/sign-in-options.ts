import { z } from "zod";

/**
 * How people can sign in to this server, as GET /api/v1/sign-in-options
 * answers it (soloclef_api/internal/signinoptions). Every field is read from
 * the same setting the matching route obeys, so a page that follows it never
 * offers what the server would then refuse.
 *
 * The website reads it at run time, not at build time: the static pages are
 * built once, and whether this server mails reset links is a setting the
 * operator may change without rebuilding them.
 */
export const signInOptionsSchema = z.object({
	service: z.string(),
	password: z.boolean(),
	/** Whether a reset link can be mailed: false on a server that sends no mail. */
	forgot_password: z.boolean(),
	sign_up: z.boolean(),
	sso: z
		.object({
			label: z.string(),
			/** The provider as a sentence names it ("Example SSO"). */
			name: z.string(),
			app: z.boolean(),
		})
		.nullable(),
});

export type SignInOptions = z.infer<typeof signInOptionsSchema>;

/** The words "confirm it's you with …" uses: the provider's own name, or a plain description. */
export function ssoProviderName(options: SignInOptions | undefined, fallback: string): string {
	const name = options?.sso?.name.trim();
	return name ? name : fallback;
}
