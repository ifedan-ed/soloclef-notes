import { baseApiService } from "@/lib/apis/base-api.service";
import { type CreatedLink, createdLinkSchema } from "../model/created-link";
import { type PublicLink, publicLinkListSchema } from "../model/public-link";
import { type ExpiryChoice, expiryPayload } from "../model/share-expiry";

/**
 * The owner's side of public links. Only a Studio output can be shared, so
 * this is the whole of it: make an output's link, list the account's links,
 * take one down.
 */
export const publicLinksApi = {
	shareOutput(notebookId: number, outputId: number, expiry: ExpiryChoice): Promise<CreatedLink> {
		return baseApiService.post(
			`/api/v1/notebooks/${notebookId}/studio/outputs/${outputId}/public-link`,
			createdLinkSchema,
			{ body: expiryPayload(expiry) }
		);
	},

	/** Every link the signed-in account has made, newest first (the server's order). */
	async list(): Promise<PublicLink[]> {
		const body = await baseApiService.get("/api/v1/snapshots", publicLinkListSchema);
		return body.snapshots;
	},

	revoke(linkId: number): Promise<unknown> {
		return baseApiService.delete(`/api/v1/snapshots/${linkId}`);
	},
};
