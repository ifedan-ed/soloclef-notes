import { z } from "zod";

/**
 * A notebook's live link, as its owner reads it. A notebook has one owner, so
 * the link carries no "made by" and no "may revoke": the owner made it and may.
 */
export const liveLinkSchema = z.object({
	id: z.number(),
	notebook_id: z.number(),
	notebook_title: z.string(),
	notebook_icon: z.string().nullable().optional(),
	share_token: z.string(),
	public_url: z.string(),
	created_at: z.string(),
	expires_at: z.string().nullable(),
	is_expired: z.boolean(),
});
export type LiveLink = z.infer<typeof liveLinkSchema>;

export const liveLinkStateSchema = z.object({ link: liveLinkSchema.nullable() });
export const liveLinkListSchema = z.object({ links: z.array(liveLinkSchema) });
