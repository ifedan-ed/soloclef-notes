import { z } from "zod";
import { baseApiService } from "@/lib/apis/base-api.service";

export const nextcloudConnectionSchema = z.object({
	configured: z.boolean(),
	server: z.string().nullable(),
	connector_id: z.number().nullable(),
	account: z.object({ user_id: z.string(), display_name: z.string() }).nullable(),
	needs_reconnect: z.boolean(),
});
export type NextcloudConnection = z.infer<typeof nextcloudConnectionSchema>;

const loginStartedSchema = z.object({
	flow_id: z.string(),
	login_url: z.string().url(),
	expires_in: z.number(),
});
export type LoginStarted = z.infer<typeof loginStartedSchema>;

const loginProgressSchema = z.object({
	status: z.enum(["pending", "connected"]),
	connection: nextcloudConnectionSchema.nullable(),
});
export type LoginProgress = z.infer<typeof loginProgressSchema>;

const disconnectedSchema = z.object({ revoked: z.boolean() });

// The connection is the signed-in account's: one per account, so the
// routes name nobody.
const base = "/api/v1/nextcloud";

/**
 * Connecting Nextcloud with its Login Flow. The backend holds the poll token
 * and receives the app password; the browser only sees a flow id and the
 * login page to open.
 */
export const nextcloudConnectApi = {
	connection: () => baseApiService.get(`${base}/connection`, nextcloudConnectionSchema),
	start: () => baseApiService.post(`${base}/login`, loginStartedSchema),
	poll: (flowId: string) =>
		baseApiService.post(`${base}/login/${encodeURIComponent(flowId)}/poll`, loginProgressSchema),
	cancel: (flowId: string) => baseApiService.delete(`${base}/login/${encodeURIComponent(flowId)}`),
	disconnect: (connectorId: number) =>
		baseApiService.delete(
			`/api/v1/connectors/${connectorId}/nextcloud/connection`,
			disconnectedSchema
		),
};
