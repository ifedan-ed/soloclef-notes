import { z } from "zod";
import { baseApiService } from "@/lib/apis/base-api.service";

const availabilitySchema = z.object({ configured: z.boolean() });
const connectStartedSchema = z.object({ auth_url: z.string().url() });

const base = "/api/v1/connectors/email-imap/microsoft";

/**
 * Connecting a Microsoft mailbox: Microsoft's sign-in page, then back to the
 * page it started from. Tokens never reach the browser.
 */
export const microsoftMailApi = {
	availability: () => baseApiService.get(base, availabilitySchema),
	connect: (returnPath: string, accountId?: string) =>
		baseApiService.post(`${base}/connect`, connectStartedSchema, {
			body: { return_path: returnPath, account_id: accountId ?? null },
		}),
};
