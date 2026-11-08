"use client";

import { forwardRef, useEffect, useImperativeHandle, useRef } from "react";
import { Spinner } from "@/components/ui/spinner";
import {
	HTML_IFRAME_SANDBOX,
	useHtmlArtifactDocument,
} from "@/features/file-viewers/html-file-viewer";
import type { ViewableFile } from "@/features/file-viewers/model";
import { UnviewableFile } from "@/features/file-viewers/unviewable-file";
import {
	BRIDGE_TO_REPORT,
	parseBridgeMessage,
	REPORT_BRIDGE_SCRIPT,
} from "../../lib/report-bridge";

export interface ReportFrameHandle {
	goto: (index: number) => void;
}

/**
 * An interactive report in its sandboxed frame, with the contents bridge:
 * the frame reports its headings and the one being read; ``goto`` scrolls it.
 */
export const ReportHtmlFrame = forwardRef<
	ReportFrameHandle,
	{
		primary: ViewableFile;
		onToc: (items: string[]) => void;
		onActive: (index: number) => void;
	}
>(function ReportHtmlFrame({ primary, onToc, onActive }, ref) {
	const { state, showSpinner } = useHtmlArtifactDocument(primary, REPORT_BRIDGE_SCRIPT);
	const frame = useRef<HTMLIFrameElement>(null);
	const handlers = useRef({ onToc, onActive });
	handlers.current = { onToc, onActive };

	useImperativeHandle(ref, () => ({
		goto: (index: number) =>
			frame.current?.contentWindow?.postMessage(
				{ source: BRIDGE_TO_REPORT, type: "goto", index },
				"*"
			),
	}));

	useEffect(() => {
		const listen = (event: MessageEvent) => {
			// Only the report's own frame; its origin is opaque ("null").
			if (!frame.current || event.source !== frame.current.contentWindow) return;
			const message = parseBridgeMessage(event.data);
			if (message?.type === "toc") handlers.current.onToc(message.items);
			else if (message?.type === "active") handlers.current.onActive(message.index);
		};
		window.addEventListener("message", listen);
		return () => window.removeEventListener("message", listen);
	}, []);

	if (state.status === "loading") {
		return (
			<div aria-busy="true" className="flex h-full items-center justify-center">
				{showSpinner ? <Spinner size="lg" /> : null}
			</div>
		);
	}
	if (state.status === "error") return <UnviewableFile message={state.message} />;
	return (
		<iframe
			ref={frame}
			className="h-full w-full border-0 bg-white"
			sandbox={HTML_IFRAME_SANDBOX}
			allow={state.hasMedia ? "fullscreen" : undefined}
			srcDoc={state.source}
			title={primary.filename}
		/>
	);
});
