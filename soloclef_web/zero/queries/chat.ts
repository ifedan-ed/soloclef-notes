import { defineQuery } from "@rocicorp/zero";
import { z } from "zod";
import { zql } from "../schema/index";
import { constrainToReadableThreads } from "./thread-access";

export const messageQueries = {
	byThread: defineQuery(z.object({ threadId: z.number() }), ({ args: { threadId }, ctx }) =>
		zql.new_chat_messages
			.where("threadId", threadId)
			.whereExists("thread", (q) => constrainToReadableThreads(q, ctx))
			.orderBy("createdAt", "asc")
	),
};

export const commentQueries = {
	byThread: defineQuery(z.object({ threadId: z.number() }), ({ args: { threadId }, ctx }) =>
		zql.chat_comments
			.where("threadId", threadId)
			.whereExists("thread", (q) => constrainToReadableThreads(q, ctx))
			.orderBy("createdAt", "asc")
	),
};

export const chatSessionQueries = {
	byThread: defineQuery(z.object({ threadId: z.number() }), ({ args: { threadId }, ctx }) =>
		zql.chat_session_state
			.where("threadId", threadId)
			.whereExists("thread", (q) => constrainToReadableThreads(q, ctx))
			.one()
	),
};
