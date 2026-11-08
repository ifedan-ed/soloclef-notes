"use client";

import { useAtomValue } from "jotai";
import { myAccessAtom } from "@/atoms/members/members-query.atoms";
import { canCreatePublicLinks } from "../lib/public-link-access";

/** Owners, by default: the share actions only render for them. */
export function useCanCreatePublicLinks(): boolean {
	const { data: access } = useAtomValue(myAccessAtom);
	return canCreatePublicLinks(access);
}
