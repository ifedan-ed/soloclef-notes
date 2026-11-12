import { defineQuery } from "@rocicorp/zero";
import { zql } from "../schema/index";
import { constrainToOwner } from "./ownership";

// The caller's notifications: no argument, the account is the context's.
export const notificationQueries = {
	mine: defineQuery(({ ctx }) =>
		constrainToOwner(zql.notifications, ctx).orderBy("createdAt", "desc")
	),
};
