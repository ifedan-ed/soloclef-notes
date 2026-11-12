import { atomWithQuery } from "jotai-tanstack-query";
import { chatThreadsApiService } from "@/lib/apis/chat-threads-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";

/** Every public chat and output link of the signed-in account. */
export const publicChatSnapshotsAtom = atomWithQuery(() => ({
	queryKey: cacheKeys.publicChatSnapshots.list,
	staleTime: 5 * 60 * 1000,
	queryFn: () => chatThreadsApiService.listAccountPublicChatSnapshots(),
}));
