import { defineQuery } from "@rocicorp/zero";
import { z } from "zod";
import { zql } from "../schema/index";
import { canReadSpace, denySpace } from "./authz";
import { constrainToOutputsOfReadableThreads } from "./thread-access";

export const deliverableJobQueries = {
	bySpace: defineQuery(z.object({ workspaceId: z.number() }), ({ args: { workspaceId }, ctx }) => {
		const query = zql.deliverable_jobs.where("workspaceId", workspaceId);
		if (!canReadSpace(ctx, workspaceId)) return denySpace(query).orderBy("createdAt", "desc");
		return constrainToOutputsOfReadableThreads(query, ctx).orderBy("createdAt", "desc");
	}),
	byId: defineQuery(z.object({ jobId: z.number() }), ({ args: { jobId }, ctx }) =>
		constrainToOutputsOfReadableThreads(zql.deliverable_jobs.where("id", jobId), ctx).one()
	),
};
