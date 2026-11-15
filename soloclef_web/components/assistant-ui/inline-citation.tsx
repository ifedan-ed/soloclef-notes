"use client";

import { useSetAtom } from "jotai";
import { FileText } from "lucide-react";
import type { FC } from "react";
import { useId, useState } from "react";
import { openCitationPanelAtom } from "@/atoms/citation/citation-panel.atom";
import { useCitationMetadata } from "@/components/assistant-ui/citation-metadata-context";
import { PublicCitation, usePublicCitations } from "@/components/assistant-ui/public-citations";
import { CitationPanelContent } from "@/components/citation-panel/citation-panel";
import { useCitationNumber } from "@/components/citations/citation-numbers-context";
import { Citation } from "@/components/tool-ui/citation";
import { Button } from "@/components/ui/button";
import {
	Drawer,
	DrawerContent,
	DrawerHandle,
	DrawerHeader,
	DrawerTitle,
} from "@/components/ui/drawer";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { openArtifactPanelAtom } from "@/features/artifacts/state/artifact-panel.atom";
import { useMediaQuery } from "@/hooks/use-media-query";
import { documentsApiService } from "@/lib/apis/documents-api.service";
import { tryGetHostname } from "@/lib/url";

interface InlineCitationProps {
	chunkId: number;
	isDocsChunk?: boolean;
}

/** The pill every citation chip is drawn as. */
export const PILL =
	"ml-0.5 inline-flex h-5 min-w-5 items-center justify-center gap-0.5 rounded-md bg-popover px-1.5 text-[11px] font-medium text-popover-foreground/80 align-baseline";

/**
 * What a chip shows: its number in this answer (1, 2, 3 in order of first
 * appearance; lib/citations/citation-numbering.ts), never the passage id,
 * which means nothing to a reader. Without a number (a chip outside any
 * numbering) it shows a source icon.
 */
const ChipFace: FC<{ number: number | null }> = ({ number }) =>
	number === null ? <FileText className="size-3" aria-hidden /> : number;

/**
 * Inline citation badge for knowledge-base chunks (numeric chunk IDs).
 *
 * The badge shows the citation's number in its answer. A click resolves the
 * chunk through the document chunk API: artifact documents open the artifact
 * panel; all others open the citation panel, which shows the cited chunk
 * surrounded by adjacent chunks.
 *
 * Negative chunk IDs and legacy Soloclef-docs chunks (`isDocsChunk`) render
 * as a static, non-interactive "doc" pill. The Soloclef product-docs feature
 * was removed, so those markers are inert (no fetch, no preview) — they only
 * survive in old persisted messages.
 */
export const InlineCitation: FC<InlineCitationProps> = ({ chunkId, isDocsChunk = false }) => {
	const resolvePublic = usePublicCitations();
	const number = useCitationNumber({ kind: "chunk", chunkId, isDocsChunk });
	if (resolvePublic && !isDocsChunk) {
		// On a public page the marker holds the page's own source number
		// (it is what resolves the source); the chip still shows the
		// citation's number within this answer.
		return <PublicCitation number={number} target={resolvePublic(chunkId)} />;
	}
	if (chunkId < 0 || isDocsChunk) {
		return (
			<Tooltip>
				<TooltipTrigger asChild>
					<span className={PILL} role="note">
						<FileText className="size-3" />
						doc
					</span>
				</TooltipTrigger>
				<TooltipContent>
					{isDocsChunk ? "Documentation reference" : "Uploaded document"}
				</TooltipContent>
			</Tooltip>
		);
	}

	return <NumericChunkCitation chunkId={chunkId} number={number} />;
};

/**
 * A citation the model has written in an answer that is still streaming:
 * "[3]", which is saved as a marker naming its source only when the answer
 * ends. It shows the number it will keep then, and opens nothing yet,
 * because which passage it names is known only to the server until then.
 */
export const LabelCitation: FC<{ label: number }> = ({ label }) => {
	const number = useCitationNumber({ kind: "label", label });
	return (
		<span className={PILL} role="note" aria-label="Citation; opens when the answer is finished">
			<ChipFace number={number} />
		</span>
	);
};

const NumericChunkCitation: FC<{ chunkId: number; number: number | null }> = ({
	chunkId,
	number,
}) => {
	const isTouchLike = useMediaQuery("(hover: none), (pointer: coarse)");
	const openCitationPanel = useSetAtom(openCitationPanelAtom);
	const openArtifactPanel = useSetAtom(openArtifactPanelAtom);
	const [mobilePreviewOpen, setMobilePreviewOpen] = useState(false);
	const [loading, setLoading] = useState(false);

	const handleClick = async () => {
		if (loading) return;
		setLoading(true);
		try {
			const document = await documentsApiService.getDocumentByChunk({
				chunk_id: chunkId,
				chunk_window: 0,
			});
			const artifactId = document.document_metadata.artifact_id;
			if (
				document.document_type === "ARTIFACT" &&
				typeof artifactId === "number" &&
				Number.isInteger(artifactId) &&
				artifactId > 0
			) {
				openArtifactPanel({ artifactId });
				return;
			}
		} catch {
			// Let the citation panel surface the existing fetch error.
		} finally {
			setLoading(false);
		}

		if (isTouchLike) setMobilePreviewOpen(true);
		else openCitationPanel({ chunkId });
	};

	return (
		<>
			<Button
				type="button"
				variant="ghost"
				onClick={() => void handleClick()}
				disabled={loading}
				className={PILL}
				title={number === null ? "View source" : `View source ${number}`}
				aria-label={`View cited chunk ${chunkId}`}
				data-citation-number={number ?? undefined}
			>
				<ChipFace number={number} />
			</Button>
			<Drawer
				open={mobilePreviewOpen}
				onOpenChange={setMobilePreviewOpen}
				shouldScaleBackground={false}
			>
				<DrawerContent
					className="h-[85vh] max-h-[85vh] z-80 overflow-hidden"
					overlayClassName="z-80"
				>
					<DrawerHandle />
					<DrawerHeader className="pb-0">
						<DrawerTitle>Citation</DrawerTitle>
					</DrawerHeader>
					<div className="min-h-0 flex-1 flex flex-col overflow-hidden">
						<CitationPanelContent chunkId={chunkId} />
					</div>
				</DrawerContent>
			</Drawer>
		</>
	);
};

interface UrlCitationProps {
	url: string;
}

/**
 * Inline citation for URL-based chunk IDs (e.g. scraped/linked web pages).
 * The chip shows the citation's number, from the same sequence as the
 * passages', so a reader sees one list of sources; the hover popover names
 * the page (favicon, domain, title and snippet when known).
 */
export const UrlCitation: FC<UrlCitationProps> = ({ url }) => {
	const reactId = useId();
	const citationInstanceId = `url-cite-${reactId.replace(/:/g, "")}`;
	const domain = tryGetHostname(url) ?? url;
	const meta = useCitationMetadata(url);
	const number = useCitationNumber({ kind: "url", url });

	return (
		<Citation
			id={citationInstanceId}
			href={url}
			title={meta?.title || domain}
			snippet={meta?.snippet}
			domain={domain}
			favicon={`https://www.google.com/s2/favicons?domain=${domain}&sz=32`}
			variant="inline"
			type="webpage"
			inlineLabel={number === null ? undefined : String(number)}
		/>
	);
};
