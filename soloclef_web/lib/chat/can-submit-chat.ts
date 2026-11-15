export interface CanSubmitChatInput {
	isLoadingMessages: boolean;
	isThreadRunning: boolean;
	isComposerEmpty: boolean;
	/** The account has a usable chat model; without one a send would only be refused. */
	isChatModelReady: boolean;
}

export function canSubmitChat({
	isLoadingMessages,
	isThreadRunning,
	isComposerEmpty,
	isChatModelReady,
}: CanSubmitChatInput): boolean {
	return !isLoadingMessages && !isThreadRunning && !isComposerEmpty && isChatModelReady;
}
