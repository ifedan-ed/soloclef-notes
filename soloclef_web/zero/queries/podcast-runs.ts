import { defineQuery } from "@rocicorp/zero";
import { z } from "zod";
import { zql } from "../schema/index";
import { canReadSpace, denySpace } from "./authz";
import { constrainToOutputsOfReadableThreads } from "./thread-access";

export const podcastRunQueries = {
	bySpace: defineQuery(z.object({ workspaceId: z.number() }), ({ args: { workspaceId }, ctx }) => {
		const query = zql.podcast_runs.where("workspaceId", workspaceId);
		if (!canReadSpace(ctx, workspaceId)) return denySpace(query).orderBy("createdAt", "desc");
		return constrainToOutputsOfReadableThreads(query, ctx).orderBy("createdAt", "desc");
	}),
	byId: defineQuery(z.object({ podcastId: z.number() }), ({ args: { podcastId }, ctx }) =>
		constrainToOutputsOfReadableThreads(zql.podcast_runs.where("id", podcastId), ctx).one()
	),
};
