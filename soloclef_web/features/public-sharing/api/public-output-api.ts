import { baseApiService } from "@/lib/apis/base-api.service";
import { type PublicOutputPage, publicOutputPage } from "../model/public-output";

/** The read a public link's page makes. No session is sent: the token is the key. */
export const publicOutputApi = {
	page(shareToken: string): Promise<PublicOutputPage> {
		return baseApiService.get(`/api/v1/public/${encodeURIComponent(shareToken)}`, publicOutputPage);
	},
};
