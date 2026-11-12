import { baseApiService } from "@/lib/apis/base-api.service";
import { type CreatedLink, createdLinkSchema } from "../model/created-link";
import {
	type LiveLink,
	liveLinkListSchema,
	liveLinkSchema,
	liveLinkStateSchema,
} from "../model/live-link";
import { type ExpiryChoice, expiryPayload } from "../model/share-expiry";

export const publicLinksApi = {
	shareChat(threadId: number, expiry: ExpiryChoice): Promise<CreatedLink> {
		return baseApiService.post(`/api/v1/threads/${threadId}/snapshots`, createdLinkSchema, {
			body: expiryPayload(expiry),
		});
	},

	shareOutput(notebookId: number, outputId: number, expiry: ExpiryChoice): Promise<CreatedLink> {
		return baseApiService.post(
			`/api/v1/notebooks/${notebookId}/studio/outputs/${outputId}/public-link`,
			createdLinkSchema,
			{ body: expiryPayload(expiry) }
		);
	},

	/** The notebook's live link, or null. */
	async notebookLink(notebookId: number): Promise<LiveLink | null> {
		const state = await baseApiService.get(
			`/api/v1/notebooks/${notebookId}/public-link`,
			liveLinkStateSchema
		);
		return state.link;
	},

	/** Make the notebook's live link, or keep it with a new expiry; `regenerate` gives it a new address. */
	shareNotebook(notebookId: number, expiry: ExpiryChoice, regenerate = false): Promise<LiveLink> {
		return baseApiService.post(`/api/v1/notebooks/${notebookId}/public-link`, liveLinkSchema, {
			body: { ...expiryPayload(expiry), regenerate },
		});
	},

	stopSharingNotebook(notebookId: number): Promise<unknown> {
		return baseApiService.delete(`/api/v1/notebooks/${notebookId}/public-link`);
	},

	async notebookLinks(): Promise<LiveLink[]> {
		const list = await baseApiService.get(`/api/v1/notebook-links`, liveLinkListSchema);
		return list.links;
	},

	revokeNotebookLink(linkId: number): Promise<unknown> {
		return baseApiService.delete(`/api/v1/notebook-links/${linkId}`);
	},

	revoke(snapshotId: number): Promise<unknown> {
		return baseApiService.delete(`/api/v1/snapshots/${snapshotId}`);
	},
};
