import { z } from "zod";

/**
 * One of the account's public links, as Settings › Public links lists it.
 * Every link is to a Studio output. The wire names predate that: the list
 * key is `snapshots` and the output's title arrives as `thread_title`. Only
 * the fields the list shows are read.
 */
export const publicLinkSchema = z.object({
	id: z.number(),
	share_token: z.string(),
	created_at: z.string(),
	expires_at: z.string().nullable(),
	is_expired: z.boolean(),
	thread_title: z.string(),
});
export type PublicLink = z.infer<typeof publicLinkSchema>;

export const publicLinkListSchema = z.object({ snapshots: z.array(publicLinkSchema) });
