import { z } from "zod";
import { baseApiService } from "@/lib/apis/base-api.service";

const BASE = "/api/v1/admin/announcements";

export const sentAnnouncementSchema = z.object({
	id: z.number(),
	title: z.string(),
	body: z.string(),
	link: z.string().nullable(),
	audience: z.enum(["all", "workspace"]),
	workspace_id: z.number().nullable(),
	workspace_name: z.string().nullable(),
	created_at: z.string(),
	expires_at: z.string().nullable(),
	withdrawn_at: z.string().nullable(),
	recipient_count: z.number(),
	read_count: z.number(),
	status: z.enum(["live", "expired", "withdrawn"]),
});

export const announcementsOverviewSchema = z.object({
	announcements: z.array(sentAnnouncementSchema),
	workspaces: z.array(z.object({ id: z.number(), name: z.string(), member_count: z.number() })),
});

export type SentAnnouncement = z.infer<typeof sentAnnouncementSchema>;
export type AnnouncementsOverview = z.infer<typeof announcementsOverviewSchema>;

export interface AnnouncementBody {
	title: string;
	body: string;
	link: string | null;
	expires_at: string | null;
	audience: "all" | "workspace";
	workspace_id: number | null;
}

/** Site announcements. Every route requires a site administrator. */
export const announcementsAdminApi = {
	overview: (signal?: AbortSignal) =>
		baseApiService.get(BASE, announcementsOverviewSchema, { signal }),
	send: (body: AnnouncementBody) => baseApiService.post(BASE, sentAnnouncementSchema, { body }),
	withdraw: (id: number) => baseApiService.post(`${BASE}/${id}/withdraw`, sentAnnouncementSchema),
};
