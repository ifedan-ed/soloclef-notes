import { defineQuery } from "@rocicorp/zero";
import { z } from "zod";
import { zql } from "../schema/index";
import { constrainThroughParent } from "./ownership";

export const messageQueries = {
	byThread: defineQuery(z.object({ threadId: z.number() }), ({ args: { threadId }, ctx }) =>
		constrainThroughParent(zql.new_chat_messages.where("threadId", threadId), "thread", ctx).orderBy(
			"createdAt",
			"asc"
		)
	),
};

export const chatSessionQueries = {
	byThread: defineQuery(z.object({ threadId: z.number() }), ({ args: { threadId }, ctx }) =>
		constrainThroughParent(zql.chat_session_state.where("threadId", threadId), "thread", ctx).one()
	),
};
