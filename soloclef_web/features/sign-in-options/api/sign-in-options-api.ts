import { baseApiService } from "@/lib/apis/base-api.service";
import { type SignInOptions, signInOptionsSchema } from "../model/sign-in-options";

/** The route's path; public, so it is listed among the API client's no-auth endpoints. */
export const SIGN_IN_OPTIONS_PATH = "/api/v1/sign-in-options";

/** Read how this server lets people sign in. Needs no session. */
export function fetchSignInOptions(): Promise<SignInOptions> {
	return baseApiService.get(SIGN_IN_OPTIONS_PATH, signInOptionsSchema) as Promise<SignInOptions>;
}
