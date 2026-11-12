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

/**
 * The import request, field for field what the server's DriveImportRequest
 * reads. The picks land in the signed-in account's library (the server takes
 * the account from the session), and in the notebook too when one is named.
 */
export type DriveImportRequest = {
	files: PickedDriveFile[];
	notebook_id: number | null;
};

const importResultSchema = z.object({
	document_ids: z.array(z.number()),
	queued: z.number(),
	unsupported: z.array(z.string()),
});
export type DriveImportResult = z.infer<typeof importResultSchema>;

const disconnectedSchema = z.object({ revoked: z.boolean() });

// The account's own Drive connection: one per account, so no id in the path.
const accountBase = "/api/v1/google-drive";
const connectorBase = (connectorId: number) => `/api/v1/connectors/${connectorId}/google-drive`;

/**
 * Connecting Google Drive (Google's consent page, then back here) and
 * importing files picked in the Google Picker. The refresh token never
 * leaves the server; the Picker gets a short-lived access token.
 */
export const googleDriveApi = {
	connection: () => baseApiService.get(`${accountBase}/connection`, googleDriveConnectionSchema),
	connect: (returnPath: string | null) =>
		baseApiService.post(`${accountBase}/connect`, connectStartedSchema, {
			body: { return_path: returnPath },
		}),
	pickerSession: (connectorId: number) =>
		baseApiService.get(`${connectorBase(connectorId)}/picker-session`, pickerSessionSchema),
	importPicked: (connectorId: number, body: DriveImportRequest) =>
		baseApiService.post(`${connectorBase(connectorId)}/import`, importResultSchema, { body }),
	disconnect: (connectorId: number) =>
		baseApiService.delete(`${connectorBase(connectorId)}/connection`, disconnectedSchema),
};
