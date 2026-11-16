"use client";

import { useSetAtom } from "jotai";
import { useCallback } from "react";
import { openArtifactPanelAtom } from "@/features/artifacts/state/artifact-panel.atom";
import { useNotebookChat } from "@/features/notebooks/state/notebook-chat-context";

type OpenArtifactRequest = { artifactId: number; selectedCardToolCallId?: string | null };

/**
 * Opens an artifact the chat made. In a notebook, what the chat made is one
 * of the notebook's Studio outputs, so it opens in Studio's output view: the
 * same viewer and the same actions (Share publicly, Expand, the prompt) as
 * when it is opened from Studio, for every kind. Elsewhere, and for an
 * artifact that is no Studio output (a picture), it opens in the chat's
 * artifact panel.
 */
export function useOpenArtifact(): (request: OpenArtifactRequest) => void {
	const openArtifactPanel = useSetAtom(openArtifactPanelAtom);
	const openArtifactOutput = useNotebookChat()?.openArtifactOutput;
	return useCallback(
		(request: OpenArtifactRequest) => {
			if (openArtifactOutput?.(request.artifactId)) return;
			openArtifactPanel(request);
		},
		[openArtifactOutput, openArtifactPanel]
	);
}
