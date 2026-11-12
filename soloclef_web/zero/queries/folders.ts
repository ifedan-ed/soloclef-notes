import { defineQuery } from "@rocicorp/zero";
import { zql } from "../schema/index";
import { constrainToOwner } from "./ownership";

// The caller's folder tree: no argument, the account is the context's.
export const folderQueries = {
	mine: defineQuery(({ ctx }) =>
		constrainToOwner(zql.folders, ctx).orderBy("position", "asc")
	),
};
