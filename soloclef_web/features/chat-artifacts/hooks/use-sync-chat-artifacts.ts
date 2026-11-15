import type { ThreadMessageLike } from "@assistant-ui/react";
import { useQuery } from "@tanstack/react-query";
import { useSetAtom } from "jotai";
import { useEffect, useMemo } from "react";
import { artifactListQueryOptions } from "@/features/artifacts/api/artifact-queries";
import {
	collectArtifacts,
	resolveArtifactRowsWithLegacyCompatibility,
} from "../lib/collect-artifacts";
import type { ChatArtifact } from "../model/artifact";
import { chatArtifactsAtom } from "../state/artifacts-panel.atom";

/**
 * Keep `chatArtifactsAtom` in sync with the active thread's messages so the
 * right-panel sidebar (rendered in the layout shell, outside the chat runtime)
 * can read the deliverable list. Clears on unmount and on thread switch (a new
 * `messages` array recomputes to the new thread's artifacts).
 */
export function useSyncChatArtifacts(
	messages: readonly ThreadMessageLike[],
	threadId: number | null
): {
	artifacts: ChatArtifact[];
	isReady: boolean;
} {
	const setArtifacts = useSetAtom(chatArtifactsAtom);
	const messageArtifacts = useMemo(() => collectArtifacts(messages), [messages]);
	const canLoadPersisted = threadId != null;
	const {
		data: persisted = [],
		isError,
		isPending,
	} = useQuery({
		...artifactListQueryOptions(threadId),
		enabled: canLoadPersisted,
		// A media job saves its Artifact after the chat turn has returned; the
		// server reports that save on the change stream (`outputs`), which
		// reads this list again. It used to poll for up to ten minutes.
	});
	const artifacts = useMemo(
		() => resolveArtifactRowsWithLegacyCompatibility(messageArtifacts, persisted),
		[messageArtifacts, persisted]
	);

	useEffect(() => {
		setArtifacts(artifacts);
	}, [artifacts, setArtifacts]);

	useEffect(() => () => setArtifacts([]), [setArtifacts]);

	// Compatibility layer: a failed persisted-metadata read must not make deep
	// links discard historical artifacts. React Query can retry on focus.
	return {
		artifacts,
		isReady: !canLoadPersisted || (!isPending && !isError),
	};
}
