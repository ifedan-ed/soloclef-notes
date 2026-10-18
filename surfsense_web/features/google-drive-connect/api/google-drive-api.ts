import { z } from "zod";
import { baseApiService } from "@/lib/apis/base-api.service";

export const googleDriveConnectionSchema = z.object({
	configured: z.boolean(),
	missing: z.array(z.string()),
	connector_id: z.number().nullable(),
	account_email: z.string().nullable(),
	needs_reconnect: z.boolean(),
});
export type GoogleDriveConnection = z.infer<typeof googleDriveConnectionSchema>;

const connectStartedSchema = z.object({ auth_url: z.string().url() });

const pickerSessionSchema = z.object({
	access_token: z.string(),
	api_key: z.string(),
	app_id: z.string(),
});
export type PickerSession = z.infer<typeof pickerSessionSchema>;

export type PickedDriveFile = { id: string; name: string; mime_type: string };

const importResultSchema = z.object({
	document_ids: z.array(z.number()),
	queued: z.number(),
	unsupported: z.array(z.string()),
});
export type DriveImportResult = z.infer<typeof importResultSchema>;

const disconnectedSchema = z.object({ revoked: z.boolean() });

const workspaceBase = (workspaceId: number) => `/api/v1/workspaces/${workspaceId}/google-drive`;
const connectorBase = (connectorId: number) => `/api/v1/connectors/${connectorId}/google-drive`;

/**
 * Connecting Google Drive (Google's consent page, then back here) and
 * importing files picked in the Google Picker. The refresh token never
 * leaves the server; the Picker gets a short-lived access token.
 */
export const googleDriveApi = {
	connection: (workspaceId: number) =>
		baseApiService.get(`${workspaceBase(workspaceId)}/connection`, googleDriveConnectionSchema),
	connect: (workspaceId: number, returnPath: string | null) =>
		baseApiService.post(`${workspaceBase(workspaceId)}/connect`, connectStartedSchema, {
			body: { return_path: returnPath },
		}),
	pickerSession: (connectorId: number) =>
		baseApiService.get(`${connectorBase(connectorId)}/picker-session`, pickerSessionSchema),
	importPicked: (connectorId: number, files: PickedDriveFile[], notebookId: number | null) =>
		baseApiService.post(`${connectorBase(connectorId)}/import`, importResultSchema, {
			body: { files, notebook_id: notebookId },
		}),
	disconnect: (connectorId: number) =>
		baseApiService.delete(`${connectorBase(connectorId)}/connection`, disconnectedSchema),
};
