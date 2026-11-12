"use client";

import { AuiIf, ThreadPrimitive, useAuiState } from "@assistant-ui/react";
import { ArrowDownIcon } from "lucide-react";
import type { FC, ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

const ChatScrollToBottom: FC = () => (
	<ThreadPrimitive.ScrollToBottom asChild>
		<Button
			type="button"
			variant="ghost"
			size="icon"
			aria-label="Scroll to bottom"
			className="aui-thread-scroll-to-bottom -top-12 absolute z-10 size-10 self-center rounded-full border border-input bg-muted p-0 text-foreground shadow-sm shadow-black/5 hover:bg-accent hover:text-accent-foreground disabled:invisible dark:shadow-black/10"
		>
			<ArrowDownIcon />
		</Button>
	</ThreadPrimitive.ScrollToBottom>
);

export interface ChatViewportProps {
	children: ReactNode;
	footer?: ReactNode;
	/**
	 * Keep the footer (composer) shown even when the thread has no messages —
	 * needed while an existing thread's messages are still loading, so the
	 * bottom composer stays visible above the loading skeleton.
	 */
	footerAlwaysVisible?: boolean;
	/**
	 * While the thread is empty, the scrolling area is only as tall as what it
	 * holds, so the footer's composer sits right under it (a notebook's
	 * welcome) rather than at the bottom of the panel. Content taller than
	 * the panel still scrolls, with the composer kept in view. Only a layout:
	 * the composer stays the same mounted element whichever way it is laid
	 * out, so nothing typed into it can be lost to a swap.
	 */
	footerUnderEmptyContent?: boolean;
}

/**
 * The chat's scrolling area with the composer beneath it.
 *
 * The composer sits below the scrolling area, not pinned inside it. A browser
 * scrolls an area while text is drag-selected only once the pointer leaves
 * that area; with the composer inside, dragging a selection down onto it
 * scrolled nothing, so nothing below it could be selected (2026-11-06,
 * tests/app/chat-select-scroll.spec.ts). The public chat (public-thread.tsx)
 * is laid out the same way. A fade at the top of the composer still shows
 * that text continues beneath it, and lets the pointer through to the
 * messages.
 */
export const ChatViewport: FC<ChatViewportProps> = ({
	children,
	footer,
	footerAlwaysVisible = false,
	footerUnderEmptyContent = false,
}) => {
	const isEmpty = useAuiState(({ thread }) => thread.isEmpty);
	const hugContent = footerUnderEmptyContent && isEmpty;
	return (
		<div className="aui-chat-viewport-column relative flex min-h-0 min-w-0 w-full flex-1 flex-col">
			<ThreadPrimitive.Viewport
				turnAnchor="top"
				autoScroll
				scrollToBottomOnRunStart
				scrollToBottomOnInitialize
				scrollToBottomOnThreadSwitch
				className={cn(
					"aui-thread-viewport relative flex min-h-0 min-w-0 w-full flex-col overflow-y-auto px-4 scroll-smooth",
					hugContent ? "flex-initial" : "flex-1"
				)}
				style={{ scrollbarGutter: "stable" }}
			>
				<div
					aria-hidden
					className="aui-chat-viewport-top-fade pointer-events-none sticky top-0 z-10 -mx-4 h-2 shrink-0 bg-gradient-to-b from-main-panel from-20% to-transparent"
				/>
				{children}
			</ThreadPrimitive.Viewport>
			{footer ? (
				<AuiIf condition={({ thread }) => footerAlwaysVisible || !thread.isEmpty}>
					<div
						className="aui-chat-composer-footer relative z-20 flex min-w-0 shrink-0 flex-col items-stretch bg-main-panel px-4 pt-2"
						style={{ paddingBottom: "max(0.5rem, env(safe-area-inset-bottom))" }}
					>
						<div
							aria-hidden
							className="aui-chat-composer-fade pointer-events-none absolute inset-x-0 -top-6 h-6 bg-gradient-to-t from-main-panel to-transparent"
						/>
						<div className="aui-chat-composer-area relative mx-auto flex min-w-0 w-full max-w-(--thread-max-width) flex-col gap-3 overflow-visible">
							<ChatScrollToBottom />
							{footer}
						</div>
					</div>
				</AuiIf>
			) : null}
		</div>
	);
};
