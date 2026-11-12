import type { Context } from "@/types/zero";

type OwnedQuery = {
	where: (...args: unknown[]) => OwnedQuery;
};

type Conditions = {
	exists: (relationship: string, cb?: (query: never) => unknown) => unknown;
};

/** A user id no row has: what a query is narrowed to when there is no caller. */
const NOBODY = "00000000-0000-0000-0000-000000000000";

/**
 * Rows of ctx's account and of no other. Every published table that has an
 * owner carries user_id, so this one comparison is the whole rule: a row is
 * yours or it is not synced. With no caller, nothing is.
 *
 * Runs on the server (the query route builds every client's query from these
 * definitions), so it holds for every client, whatever arguments it sends.
 */
export function constrainToOwner<T extends OwnedQuery>(query: T, ctx: Context): T {
	return query.where("userId", ctx?.userId ?? NOBODY) as T;
}

/**
 * A thread's children (its messages, its session state, an automation's
 * runs through their automation): they carry no owner column of their own,
 * so the rule follows the named relationship to the parent that does.
 */
export function constrainThroughParent<T extends OwnedQuery>(
	query: T,
	relationship: string,
	ctx: Context
): T {
	return query.where(({ exists }: Conditions) =>
		exists(relationship, (parent: never) => constrainToOwner(parent as OwnedQuery, ctx))
	) as T;
}
