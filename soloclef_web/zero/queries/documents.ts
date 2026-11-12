import { defineQuery } from "@rocicorp/zero";
import { zql } from "../schema/index";
import { constrainToOwner } from "./ownership";

// The caller's own rows: no argument, because the account is the context's,
// never something a client names.
export const documentQueries = {
	mine: defineQuery(({ ctx }) =>
		constrainToOwner(zql.documents, ctx).orderBy("createdAt", "desc")
	),
};

export const connectorQueries = {
	mine: defineQuery(({ ctx }) =>
		constrainToOwner(zql.search_source_connectors, ctx).orderBy("createdAt", "desc")
	),
};
