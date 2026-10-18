"use client";

import { useEffect, useState } from "react";
import { Spinner } from "@/components/ui/spinner";
import { authenticatedFetch } from "@/lib/auth-fetch";
import { buildBackendUrl } from "@/lib/env-config";
import { cannotPreviewMessage } from "./file-format";
import { buildHtmlArtifactDocument } from "./html-artifact-document";
import {
	applyMediaLinks,
	findMediaRefs,
	mediaOrigins,
	workspaceIdFromContentUrl,
} from "./html-media-links";
import { fetchMediaLinks } from "./media-links-api";
import type { FileViewerProps } from "./model";
import { UnviewableFile } from "./unviewable-file";

export const HTML_MAX_VIEWER_BYTES = 15 * 1024 * 1024;
export const HTML_IFRAME_SANDBOX = "allow-scripts";

export type LoadState =
	| { status: "loading" }
	| { status: "ready"; source: string; hasMedia: boolean }
	| { status: "error"; message: string };

class InvalidHtmlEncodingError extends Error {}

/**
 * Fetch an HTML artifact and build the sandboxed document it is shown in.
 * ``bridgeScript`` is appended after the fragment (see buildHtmlArtifactDocument).
 */
export function useHtmlArtifactDocument(
	primary: FileViewerProps["primary"],
	bridgeScript?: string
): { state: LoadState; showSpinner: boolean } {
	const [state, setState] = useState<LoadState>({ status: "loading" });
	const [showSpinner, setShowSpinner] = useState(false);

	// A spinner only once loading has lasted long enough to notice; quick
	// loads paint the report directly instead of flashing a spinner first.
	useEffect(() => {
		if (state.status !== "loading") {
			setShowSpinner(false);
			return;
		}
		const timer = window.setTimeout(() => setShowSpinner(true), 250);
		return () => window.clearTimeout(timer);
	}, [state.status]);

	useEffect(() => {
		const controller = new AbortController();
		setState({ status: "loading" });

		if (primary.size_bytes > HTML_MAX_VIEWER_BYTES) {
			setState({
				status: "error",
				message: `${cannotPreviewMessage(primary.filename)} (file is too large to preview)`,
			});
			return () => controller.abort();
		}

		void (async () => {
			try {
				const response = await authenticatedFetch(buildBackendUrl(primary.content_url), {
					cache: "no-store",
					signal: controller.signal,
					skipAuthRedirect: true,
				});
				if (!response.ok) {
					throw new Error(`Could not load HTML artifact (${response.status})`);
				}
				const bytes = await response.arrayBuffer();
				if (controller.signal.aborted) return;
				if (bytes.byteLength > HTML_MAX_VIEWER_BYTES) {
					setState({
						status: "error",
						message: `${cannotPreviewMessage(primary.filename)} (file is too large to preview)`,
					});
					return;
				}
				let fragment: string;
				try {
					fragment = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
				} catch {
					throw new InvalidHtmlEncodingError();
				}
				const refs = findMediaRefs(fragment);
				const workspaceId = workspaceIdFromContentUrl(primary.content_url);
				let links = new Map<string, string>();
				if (refs.length && workspaceId !== null) {
					links = await fetchMediaLinks(workspaceId, refs, controller.signal);
					if (controller.signal.aborted) return;
				}
				setState({
					status: "ready",
					source: buildHtmlArtifactDocument(applyMediaLinks(fragment, links), {
						mediaSources: mediaOrigins(links),
						bridgeScript,
					}),
					hasMedia: links.size > 0,
				});
			} catch (error) {
				if (controller.signal.aborted) return;
				const detail =
					error instanceof InvalidHtmlEncodingError
						? "file is not valid UTF-8"
						: "file could not be opened";
				setState({
					status: "error",
					message: `${cannotPreviewMessage(primary.filename)} (${detail})`,
				});
			}
		})();

		return () => controller.abort();
	}, [primary.content_url, primary.filename, primary.size_bytes, bridgeScript]);

	return { state, showSpinner };
}

export default function HtmlFileViewer({ primary }: FileViewerProps) {
	const { state, showSpinner } = useHtmlArtifactDocument(primary);

	if (state.status === "loading") {
		return (
			<div aria-busy="true" className="flex h-full items-center justify-center">
				{showSpinner ? <Spinner size="lg" /> : null}
			</div>
		);
	}

	if (state.status === "error") {
		return <UnviewableFile message={state.message} />;
	}

	return (
		<iframe
			className="h-full w-full border-0 bg-white"
			sandbox={HTML_IFRAME_SANDBOX}
			allow={state.hasMedia ? "fullscreen" : undefined}
			srcDoc={state.source}
			title={primary.filename}
		/>
	);
}
