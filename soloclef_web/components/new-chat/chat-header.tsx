"use client";

import { ChatModelPicker } from "@/features/chat-model/ui/chat-model-picker";
import { useIsMobile } from "@/hooks/use-mobile";
import { cn } from "@/lib/utils";
import { ImageModelSelector } from "./image-model-selector";

interface ChatHeaderProps {
	className?: string;
	onChatModelSelected?: () => void;
}

export function ChatHeader({ className, onChatModelSelected }: ChatHeaderProps) {
	const isMobile = useIsMobile();
	const selectorClassName = cn(
		className,
		"min-w-0",
		isMobile ? "flex-none basis-auto" : "max-w-[180px] flex-initial shrink"
	);

	return (
		<div
			className={cn(
				"flex min-w-0 items-center justify-end gap-0",
				isMobile ? "w-auto flex-none" : "max-w-[360px] flex-1"
			)}
		>
			<ChatModelPicker className={selectorClassName} onChatModelSelected={onChatModelSelected} />
			<ImageModelSelector className={selectorClassName} mobileIconOnly />
		</div>
	);
}
