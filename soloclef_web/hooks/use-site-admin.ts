"use client";

import { useAtomValue } from "jotai";
import { currentUserAtom } from "@/atoms/user/user-query.atoms";

export interface SiteAdminStanding {
	/** Undefined until the profile has loaded, so a caller can wait instead of guessing. */
	isAdmin: boolean | undefined;
	isLoading: boolean;
}

/**
 * Reads the administrator standing out of the profile query's state. A
 * profile that failed to load reads as "not an administrator": the admin
 * pages are only a convenience, and the server refuses their writes on its own.
 */
export function siteAdminStanding(query: {
	data?: { is_superuser: boolean };
	isError: boolean;
	isLoading: boolean;
}): SiteAdminStanding {
	if (query.isError) return { isAdmin: false, isLoading: false };
	return { isAdmin: query.data?.is_superuser, isLoading: query.isLoading };
}

/**
 * Whether the signed-in person administers this site: the account's own
 * `is_superuser` flag, from the profile (`/users/me`) the app loads once per
 * session anyway.
 */
export function useSiteAdmin(): SiteAdminStanding {
	return siteAdminStanding(useAtomValue(currentUserAtom));
}
