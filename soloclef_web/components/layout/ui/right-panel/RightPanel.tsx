"use client";

import { useAtomValue, useSetAtom } from "jotai";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import dynamic from "next/dynamic";
import { useEffect } from "react";
import { citationPanelAtom, closeCitationPanelAtom } from "@/atoms/citation/citation-panel.atom";
import {
	closeDocumentViewerAtom,
	documentViewerAtom,
} from "@/atoms/documents/document-viewer.atom";
import { closeEditorPanelAtom, editorPanelAtom } from "@/atoms/editor/editor-panel.atom";
import {
	type RightPanelTab,
	rightPanelCollapsedAtom,
	rightPanelTabAtom,
} from "@/atoms/layout/right-panel.atom";
import {
	artifactPanelAtom,
	closeArtifactPanelAtom,
} from "@/features/artifacts/state/artifact-panel.atom";
import { artifactsPanelOpenAtom, closeArtifactsPanelAtom } from "@/features/chat-artifacts";
import { closeHitlEditPanelAtom, hitlEditPanelAtom } from "@/features/chat-messages/hitl";
import { useMediaQuery } from "@/hooks/use-media-query";

const EditorPanelContent = dynamic(
	() =>
		import("@/components/editor-panel/editor-panel").then((m) => ({
			default: m.EditorPanelContent,
		})),
	{ ssr: false, loading: () => null }
);

const DocumentViewerContent = dynamic(
	() =>
		import("@/features/documents/viewer/document-viewer-panel").then((module) => ({
			default: module.DocumentViewerContent,
		})),
	{ ssr: false, loading: () => null }
);

const CitationPanelContent = dynamic(
	() =>
		import("@/components/citation-panel/citation-panel").then((m) => ({
			default: m.CitationPanelContent,
		})),
	{ ssr: false, loading: () => null }
);

const RunCitationPanelContent = dynamic(
	() =>
		import("@/components/citations/run-citation-panel").then((m) => ({
			default: m.RunCitationPanelContent,
		})),
	{ ssr: false, loading: () => null }
);

const HitlEditPanelContent = dynamic(
	() =>
		import("@/features/chat-messages/hitl").then((m) => ({
			default: m.HitlEditPanelContent,
		})),
	{ ssr: false, loading: () => null }
);

const ArtifactViewerContent = dynamic(
	() =>
		import("@/features/artifacts/ui/artifact-panel").then((m) => ({
			default: m.ArtifactViewerContent,
		})),
	{ ssr: false, loading: () => null }
);

const ArtifactsPanelContent = dynamic(
	() =>
		import("@/features/chat-artifacts").then((m) => ({
			default: m.ArtifactsPanelContent,
		})),
	{ ssr: false, loading: () => null }
);

interface RightPanelProps {
	layout: RightPanelLayout;
}

export interface RightPanelLayout {
	hasContent: boolean;
	isVisible: boolean;
	effectiveTab: RightPanelTab;
	documentOpen: boolean;
	artifactOpen: boolean;
	editorOpen: boolean;
	hitlEditOpen: boolean;
	citationOpen: boolean;
	artifactsOpen: boolean;
}

export function useRightPanelLayout(): RightPanelLayout {
	const activeTab = useAtomValue(rightPanelTabAtom);
	const collapsed = useAtomValue(rightPanelCollapsedAtom);
	const artifactState = useAtomValue(artifactPanelAtom);
	const documentState = useAtomValue(documentViewerAtom);
	const editorState = useAtomValue(editorPanelAtom);
	const hitlEditState = useAtomValue(hitlEditPanelAtom);
	const citationState = useAtomValue(citationPanelAtom);
	const artifactsPanelOpen = useAtomValue(artifactsPanelOpenAtom);
	const supportsArtifactPanel = useMediaQuery("(min-width: 1024px)");
	const artifactsOpen = supportsArtifactPanel && artifactsPanelOpen;
	const artifactOpen = supportsArtifactPanel && artifactState.isOpen && !!artifactState.artifactId;
	const documentOpen =
		supportsArtifactPanel &&
		documentState.isOpen &&
		!!documentState.documentId &&
		!!documentState.workspaceId;
	const editorOpen =
		editorState.isOpen &&
		(editorState.kind === "memory" ? !!editorState.memoryScope : !!editorState.localFilePath);
	const hitlEditOpen = hitlEditState.isOpen && !!hitlEditState.onSave;
	const citationOpen = citationState.isOpen && citationState.target != null;
	const openByTab: Record<RightPanelTab, boolean> = {
		none: false,
		document: documentOpen,
		artifact: artifactOpen,
		editor: editorOpen,
		"hitl-edit": hitlEditOpen,
		citation: citationOpen,
		artifacts: artifactsOpen,
	};
	const hasContent = Object.values(openByTab).some(Boolean);

	return {
		hasContent,
		isVisible: hasContent && !collapsed,
		effectiveTab: resolveEffectiveTab(activeTab, openByTab),
		documentOpen,
		artifactOpen,
		editorOpen,
		hitlEditOpen,
		citationOpen,
		artifactsOpen,
	};
}

const PANEL_CONTENT_TRANSITION = { duration: 0.15, ease: [0.4, 0, 0.2, 1] } as const;

/**
 * Priority order used to fall back to another open surface when the active
 * tab's content closes.
 */
const TAB_FALLBACK_ORDER: RightPanelTab[] = [
	"hitl-edit",
	"citation",
	"editor",
	"document",
	"artifact",
	"artifacts",
];

function resolveEffectiveTab(
	activeTab: RightPanelTab,
	openByTab: Record<RightPanelTab, boolean>
): RightPanelTab {
	if (openByTab[activeTab]) return activeTab;
	return TAB_FALLBACK_ORDER.find((tab) => openByTab[tab]) ?? "none";
}

export function RightPanel({ layout }: RightPanelProps) {
	const artifactState = useAtomValue(artifactPanelAtom);
	const closeArtifact = useSetAtom(closeArtifactPanelAtom);
	const documentState = useAtomValue(documentViewerAtom);
	const closeDocument = useSetAtom(closeDocumentViewerAtom);
	const editorState = useAtomValue(editorPanelAtom);
	const closeEditor = useSetAtom(closeEditorPanelAtom);
	const hitlEditState = useAtomValue(hitlEditPanelAtom);
	const closeHitlEdit = useSetAtom(closeHitlEditPanelAtom);
	const citationState = useAtomValue(citationPanelAtom);
	const closeCitation = useSetAtom(closeCitationPanelAtom);
	const closeArtifacts = useSetAtom(closeArtifactsPanelAtom);
	const reduceMotion = useReducedMotion();
	const {
		isVisible,
		effectiveTab,
		artifactOpen,
		documentOpen,
		editorOpen,
		hitlEditOpen,
		citationOpen,
		artifactsOpen,
	} = layout;

	useEffect(() => {
		if (
			!artifactOpen &&
			!documentOpen &&
			!editorOpen &&
			!hitlEditOpen &&
			!citationOpen &&
			!artifactsOpen
		)
			return;
		const handleKeyDown = (e: KeyboardEvent) => {
			if (e.key === "Escape") {
				if (documentOpen) closeDocument();
				else if (artifactOpen) closeArtifact();
				else if (hitlEditOpen) closeHitlEdit();
				else if (citationOpen) closeCitation();
				else if (editorOpen) closeEditor();
				else if (artifactsOpen) closeArtifacts();
			}
		};
		document.addEventListener("keydown", handleKeyDown);
		return () => document.removeEventListener("keydown", handleKeyDown);
	}, [
		artifactOpen,
		documentOpen,
		editorOpen,
		hitlEditOpen,
		citationOpen,
		artifactsOpen,
		closeArtifact,
		closeDocument,
		closeEditor,
		closeHitlEdit,
		closeCitation,
		closeArtifacts,
	]);

	return (
		<AnimatePresence initial={false}>
			{isVisible ? (
				<motion.aside
					key="right-panel"
					initial={reduceMotion ? false : { opacity: 0 }}
					animate={{ opacity: 1 }}
					exit={{ opacity: 0 }}
					transition={reduceMotion ? { duration: 0 } : PANEL_CONTENT_TRANSITION}
					style={reduceMotion ? undefined : { willChange: "opacity" }}
					className="flex h-full min-h-0 min-w-0 w-full flex-col overflow-hidden border-l bg-panel text-sidebar-foreground"
				>
					<div className="flex h-full min-h-0 min-w-0 w-full flex-col">
						<div className="relative min-h-0 min-w-0 flex-1 overflow-hidden">
							{effectiveTab === "artifact" && artifactOpen && (
								<div className="flex h-full min-w-0 flex-col">
									<ArtifactViewerContent
										artifactId={artifactState.artifactId as number}
										onClose={closeArtifact}
									/>
								</div>
							)}
							{effectiveTab === "document" &&
								documentOpen &&
								documentState.documentId &&
								documentState.workspaceId && (
									<div className="flex h-full min-w-0 flex-col">
										<DocumentViewerContent
											documentId={documentState.documentId}
											workspaceId={documentState.workspaceId}
											title={documentState.title}
											initialPage={documentState.initialPage}
											highlightText={documentState.highlightText}
											onClose={closeDocument}
										/>
									</div>
								)}
							{effectiveTab === "editor" && editorOpen && (
								<div className="flex h-full min-w-0 flex-col">
									<EditorPanelContent
										kind={editorState.kind}
										localFilePath={editorState.localFilePath ?? undefined}
										memoryScope={editorState.memoryScope ?? undefined}
										workspaceId={editorState.workspaceId ?? undefined}
										title={editorState.title}
										onClose={closeEditor}
									/>
								</div>
							)}
							{effectiveTab === "hitl-edit" && hitlEditOpen && hitlEditState.onSave && (
								<div className="flex h-full min-w-0 flex-col">
									<HitlEditPanelContent
										title={hitlEditState.title}
										content={hitlEditState.content}
										toolName={hitlEditState.toolName}
										contentFormat={hitlEditState.contentFormat}
										extraFields={hitlEditState.extraFields}
										onSave={hitlEditState.onSave}
										onClose={closeHitlEdit}
									/>
								</div>
							)}
							{effectiveTab === "citation" && citationOpen && citationState.target && (
								<div className="flex h-full min-w-0 flex-col">
									{citationState.target.kind === "run" ? (
										<RunCitationPanelContent
											runId={citationState.target.runId}
											onClose={closeCitation}
										/>
									) : (
										<CitationPanelContent
											chunkId={citationState.target.chunkId}
											onClose={closeCitation}
										/>
									)}
								</div>
							)}
							{effectiveTab === "artifacts" && artifactsOpen && (
								<div className="flex h-full min-w-0 flex-col">
									<ArtifactsPanelContent onClose={closeArtifacts} />
								</div>
							)}
						</div>
					</div>
				</motion.aside>
			) : null}
		</AnimatePresence>
	);
}
