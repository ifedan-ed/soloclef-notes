import { z } from "zod";

/** What the server answers when a public link is created (chat or output). */
export const createdLinkSchema = z.object({
	snapshot_id: z.number(),
	share_token: z.string(),
	public_url: z.string(),
	is_new: z.boolean(),
	expires_at: z.string().nullable().optional(),
});
export type CreatedLink = z.infer<typeof createdLinkSchema>;
