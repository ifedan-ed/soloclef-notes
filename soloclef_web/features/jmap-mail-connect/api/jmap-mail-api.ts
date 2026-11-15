import { z } from "zod";
import { baseApiService } from "@/lib/apis/base-api.service";

/**
 * A mailbox on a Stalwart server, as the server shows it. These fields are
 * the whole shape: zod drops anything else, so even a server that regressed
 * and sent a token or an app password back would not put it in page state.
 */
export const jmapAccountSchema = z.object({
	id: z.string(),
	label: z.string(),
	server_url: z.string(),
	username: z.string(),
	/** "oauth": signed in on Stalwart's own page. "app_password": typed here. */
	auth: z.enum(["oauth", "app_password"]),
	/** The account may send, and then only after the person's confirmation. */
	can_send: z.boolean(),
	/** Stalwart no longer accepts the stored sign-in: offer Reconnect. */
	needs_reconnect: z.boolean(),
	created_at: z.string().nullable(),
});
export type JmapAccount = z.infer<typeof jmapAccountSchema>;

export const jmapTestResultSchema = z.object({
	status: z.enum(["ok", "error"]),
	mailbox_count: z.number().nullable(),
	can_send: z.boolean().nullable(),
	message: z.string().nullable(),
});
export type JmapTestResult = z.infer<typeof jmapTestResultSchema>;

/**
 * What a removal did at the server. `revoked` is false for an app password
 * (this app cannot delete it) and for a sign-in Stalwart could not be told to
 * end; the person is then told where to end it themselves.
 */
export const jmapRemovalSchema = z.object({ revoked: z.boolean() });
export type JmapRemoval = z.infer<typeof jmapRemovalSchema>;

const oauthAvailabilitySchema = z.object({ configured: z.boolean() });
const oauthStartedSchema = z.object({ auth_url: z.string().url() });

export interface NewJmapAccount {
	/** Empty: the server names it after the username. */
	label?: string;
	server_url: string;
	username: string;
	app_password: string;
}

const base = "/api/v1/connectors/jmap";

/**
 * Stalwart (JMAP) mailboxes. The account is the session's; every secret is
 * sealed on the server the moment it arrives and none is ever sent back.
 */
export const jmapMailApi = {
	list: () => baseApiService.get(`${base}/accounts`, z.array(jmapAccountSchema)),
	add: (account: NewJmapAccount) =>
		baseApiService.post(`${base}/accounts`, jmapAccountSchema, { body: account }),
	remove: (accountId: string) =>
		baseApiService.delete(`${base}/accounts/${encodeURIComponent(accountId)}`, jmapRemovalSchema),
	test: (accountId: string) =>
		baseApiService.post(
			`${base}/accounts/${encodeURIComponent(accountId)}/test`,
			jmapTestResultSchema
		),
	oauthAvailability: () => baseApiService.get(`${base}/oauth`, oauthAvailabilitySchema),
	/** Stalwart's own sign-in page for this server; the browser goes there next. */
	oauthConnect: (serverUrl: string, returnPath: string) =>
		baseApiService.post(`${base}/oauth/connect`, oauthStartedSchema, {
			body: { server_url: serverUrl, return_path: returnPath },
		}),
};
