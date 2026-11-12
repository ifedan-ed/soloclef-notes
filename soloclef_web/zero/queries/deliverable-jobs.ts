import { defineQuery } from "@rocicorp/zero";
import { z } from "zod";
import { zql } from "../schema/index";
import { constrainToOwner } from "./ownership";

export const deliverableJobQueries = {
	byId: defineQuery(z.object({ jobId: z.number() }), ({ args: { jobId }, ctx }) =>
		constrainToOwner(zql.deliverable_jobs.where("id", jobId), ctx).one()
	),
};
