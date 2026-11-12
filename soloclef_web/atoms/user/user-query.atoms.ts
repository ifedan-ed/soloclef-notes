import { atomWithQuery } from "jotai-tanstack-query";
import { userApiService } from "@/lib/apis/user-api.service";
import { isAuthenticated } from "@/lib/auth-utils";

export const USER_QUERY_KEY = ["user", "me"] as const;
const userQueryFn = () => userApiService.getMe();

export const currentUserAtom = atomWithQuery(() => {
	return {
		queryKey: USER_QUERY_KEY,
		// The profile's fields change only when the person edits them here,
		// so /users/me only needs to fire once per session.
		staleTime: Infinity,
		enabled: isAuthenticated(),
		// No `retry: false` here: paired with `staleTime: Infinity`, one dropped
		// connection would leave the profile blank for the whole session. The
		// shared predicate already refuses to retry a 401.
		queryFn: userQueryFn,
	};
});
