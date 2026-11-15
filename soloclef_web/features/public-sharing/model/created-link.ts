import { z } from "zod";

/** What the server answers when an output's public link is created or renewed. */
export const createdLinkSchema = z.object({
	snapshot_id: z.number(),
	share_token: z.string(),
	public_url: z.string(),
	is_new: z.boolean(),
	expires_at: z.string().nullable().optional(),
});
export type CreatedLink = z.infer<typeof createdLinkSchema>;
