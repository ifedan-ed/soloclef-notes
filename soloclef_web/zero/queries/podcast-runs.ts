import { defineQuery } from "@rocicorp/zero";
import { z } from "zod";
import { zql } from "../schema/index";
import { constrainToOwner } from "./ownership";

export const podcastRunQueries = {
	byId: defineQuery(z.object({ podcastId: z.number() }), ({ args: { podcastId }, ctx }) =>
		constrainToOwner(zql.podcast_runs.where("id", podcastId), ctx).one()
	),
};
