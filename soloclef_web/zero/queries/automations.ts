import { defineQuery } from "@rocicorp/zero";
import { z } from "zod";
import { zql } from "../schema/index";
import { constrainThroughParent } from "./ownership";

// A run carries no owner of its own: it is readable when its automation is
// the caller's (the automation row is published with just its id and owner).
export const automationRunQueries = {
	byAutomation: defineQuery(
		z.object({ automationId: z.number() }),
		({ args: { automationId }, ctx }) =>
			constrainThroughParent(
				zql.automation_runs.where("automationId", automationId),
				"automation",
				ctx
			).orderBy("createdAt", "desc")
	),
};
