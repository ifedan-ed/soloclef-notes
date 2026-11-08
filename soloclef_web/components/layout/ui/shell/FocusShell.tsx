"use client";

import { useSetAtom } from "jotai";
import { useTranslations } from "next-intl";
import type React from "react";
import { closeCitationPanelAtom } from "@/atoms/citation/citation-panel.atom";
import { closeDocumentViewerAtom } from "@/atoms/documents/document-viewer.atom";
import { closeEditorPanelAtom } from "@/atoms/editor/editor-panel.atom";
import { closeArtifactPanelAtom } from "@/features/artifacts/state/artifact-panel.atom";
import { closeArtifactsPanelAtom } from "@/features/chat-artifacts";
import { closeHitlEditPanelAtom } from "@/features/chat-messages/hitl";
import { cn } from "@/lib/utils";
import { RightPanel, useRightPanelLayout } from "../right-panel/RightPanel";

/**
 * A full-screen page with no app sidebars (the notebook workspace). What
 * normally opens in the right-hand column — an artifact, a document, a
 * citation — opens as a wide drawer over the page instead, so the page's own
 * columns keep their width.
 */
export function FocusShell({
	children,
	className,
	footer,
}: {
	children: React.ReactNode;
	className?: string;
	footer?: React.ReactNode;
}) {
	const t = useTranslations("notebooks");
	const layout = useRightPanelLayout();
	const closeArtifact = useSetAtom(closeArtifactPanelAtom);
	const closeDocument = useSetAtom(closeDocumentViewerAtom);
	const closeEditor = useSetAtom(closeEditorPanelAtom);
	const closeHitlEdit = useSetAtom(closeHitlEditPanelAtom);
	const closeCitation = useSetAtom(closeCitationPanelAtom);
	const closeArtifacts = useSetAtom(closeArtifactsPanelAtom);
	const closeAll = () => {
		closeArtifact();
		closeDocument();
		closeEditor();
		closeHitlEdit();
		closeCitation();
		closeArtifacts();
	};

	return (
		<div className={cn("relative flex h-dvh w-full flex-col overflow-hidden bg-panel", className)}>
			<main className="flex min-h-0 flex-1 flex-col overflow-hidden">{children}</main>
			{layout.isVisible ? (
				<>
					<button
						type="button"
						aria-label={t("close_viewer")}
						onClick={closeAll}
						className="absolute inset-0 z-30 hidden cursor-default bg-black/20 lg:block"
					/>
					<div className="absolute inset-y-0 right-0 z-40 hidden w-[min(60rem,calc(100vw-4rem))] shadow-2xl lg:flex">
						<RightPanel layout={layout} />
					</div>
				</>
			) : null}
			{footer}
		</div>
	);
}
