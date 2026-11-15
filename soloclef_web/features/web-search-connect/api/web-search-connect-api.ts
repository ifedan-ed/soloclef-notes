import { z } from "zod";
import { baseApiService } from "@/lib/apis/base-api.service";
import { WEB_SEARCH_PROVIDERS, type WebSearchProvider } from "../model/web-search-providers";

/** One provider's connection: whether a key is saved, and that key's mask, never the key. */
export const webSearchConnection = z.object({
	provider: z.enum(WEB_SEARCH_PROVIDERS),
	connected: z.boolean(),
	key_mask: z.string().nullable(),
	connected_at: z.string().nullable(),
});
export type WebSearchConnection = z.infer<typeof webSearchConnection>;

const webSearchConnections = z.object({ providers: z.array(webSearchConnection) });

const PATH = "/api/v1/connectors/web-search";

export const webSearchConnectApi = {
	list: async (): Promise<WebSearchConnection[]> =>
		(await baseApiService.get(PATH, webSearchConnections)).providers,

	/**
	 * Saves a key. The server tries it against the provider first, so a
	 * refusal arrives as the provider's verdict in a sentence (thrown).
	 */
	connect: (provider: WebSearchProvider, apiKey: string) =>
		baseApiService.put(`${PATH}/${provider}`, webSearchConnection, {
			body: { api_key: apiKey },
		}),

	disconnect: async (provider: WebSearchProvider): Promise<void> => {
		await baseApiService.delete(`${PATH}/${provider}`);
	},
};

/** A provider's row from the list, or "not connected" while the list is not in. */
export function connectionOf(
	list: WebSearchConnection[] | undefined,
	provider: WebSearchProvider
): WebSearchConnection {
	return (
		list?.find((row) => row.provider === provider) ?? {
			provider,
			connected: false,
			key_mask: null,
			connected_at: null,
		}
	);
}
