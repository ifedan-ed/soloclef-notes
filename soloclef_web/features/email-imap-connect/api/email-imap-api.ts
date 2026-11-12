import { z } from "zod";
import { baseApiService } from "@/lib/apis/base-api.service";

export const emailImapAccountSchema = z.object({
	id: z.string(),
	label: z.string(),
	host: z.string(),
	port: z.number(),
	use_tls: z.boolean(),
	username: z.string(),
	created_at: z.string().nullable(),
	/** "microsoft": connected through Microsoft's sign-in, not a password. */
	auth: z.enum(["password", "microsoft"]).catch("password").default("password"),
	/** Microsoft stopped accepting this mailbox's sign-in: offer Reconnect. */
	needs_reconnect: z.boolean().default(false),
});
export type EmailImapAccount = z.infer<typeof emailImapAccountSchema>;

export const testConnectionResultSchema = z.object({
	status: z.enum(["ok", "error"]),
	folder_count: z.number().nullable().optional(),
	message: z.string().nullable().optional(),
});
export type TestConnectionResult = z.infer<typeof testConnectionResultSchema>;

export interface NewEmailImapAccount {
	label: string;
	host: string;
	port: number;
	use_tls: boolean;
	username: string;
	app_password: string;
}

// The account's own mailboxes: the server takes the account from the session.
const base = "/api/v1/connectors/email-imap/accounts";

/**
 * Email (IMAP) account management. Credentials are encrypted server-side the
 * moment they arrive — the browser never gets a password back, including
 * from `add` (the create response omits it).
 */
export const emailImapApi = {
	list: () => baseApiService.get(base, z.array(emailImapAccountSchema)),
	add: (account: NewEmailImapAccount) =>
		baseApiService.post(base, emailImapAccountSchema, { body: account }),
	remove: (accountId: string) => baseApiService.delete(`${base}/${encodeURIComponent(accountId)}`),
	test: (accountId: string) =>
		baseApiService.post(
			`${base}/${encodeURIComponent(accountId)}/test`,
			testConnectionResultSchema
		),
	/** Test connection settings before the account exists yet (add-form). */
	testUnsaved: (account: NewEmailImapAccount) =>
		baseApiService.post(`/api/v1/connectors/email-imap/test`, testConnectionResultSchema, {
			body: account,
		}),
};
