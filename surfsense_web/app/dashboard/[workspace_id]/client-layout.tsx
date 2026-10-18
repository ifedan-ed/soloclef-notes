"use client";

import { useAtomValue, useSetAtom } from "jotai";
import { useParams, useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type React from "react";
import { useEffect } from "react";
import { pendingUserImageDataUrlsAtom } from "@/atoms/chat/pending-user-images.atom";
import { llmSetupStatusAtomFamily } from "@/atoms/model-connections/model-connections-query.atoms";
import { activeWorkspaceIdAtom } from "@/atoms/workspaces/workspace-query.atoms";
import { DocumentUploadDialogProvider } from "@/components/assistant-ui/document-upload-popup";
import { RetrievalScopeProvider } from "@/components/assistant-ui/retrieval-scope-provider";
import { LayoutDataProvider } from "@/components/layout";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import type { RetrievalScope } from "@/contracts/types/retrieval-scope.types";
import { useFolderSync } from "@/hooks/use-folder-sync";
import { useGlobalLoadingEffect } from "@/hooks/use-global-loading";
import { useElectronAPI } from "@/hooks/use-platform";
import { AppError } from "@/lib/error";

export function DashboardClientLayout({
	children,
	workspaceId,
	initialRetrievalScope,
	initialRightPanelCollapsed,
}: {
	children: React.ReactNode;
	workspaceId: string;
	initialRetrievalScope: RetrievalScope;
	initialRightPanelCollapsed: boolean;
}) {
	const t = useTranslations("dashboard");
	const router = useRouter();
	const { workspace_id } = useParams();
	const activeWorkspaceId = useAtomValue(activeWorkspaceIdAtom);
	const setActiveWorkspaceIdState = useSetAtom(activeWorkspaceIdAtom);
	const setPendingUserImageUrls = useSetAtom(pendingUserImageDataUrlsAtom);

	// Whether this workspace can chat. Keyed by the route workspaceId so the
	// verdict can never lag behind a workspace switch.
	const { isLoading: setupLoading, error: setupError } = useAtomValue(
		llmSetupStatusAtomFamily(Number(workspaceId))
	);

	// A workspace you cannot open (deleted, left, or a stale link) is not a
	// configuration problem: go to /dashboard, which opens one of yours.
	const noAccess =
		setupError instanceof AppError && (setupError.status === 403 || setupError.status === 404);
	useEffect(() => {
		if (noAccess) router.replace("/dashboard");
	}, [noAccess, router]);

	const isWorkspaceReady = activeWorkspaceId === workspaceId;

	// No onboarding: every workspace chats on the shared models from the start.
	// A workspace that cannot chat (the shared list is gone) shows the
	// composer's inline notice instead of sending anyone to set up providers.

	const electronAPI = useElectronAPI();

	useEffect(() => {
		const htmlBackground = document.documentElement.style.backgroundColor;
		const bodyBackground = document.body.style.backgroundColor;

		document.documentElement.style.backgroundColor = "var(--panel)";
		document.body.style.backgroundColor = "var(--panel)";

		return () => {
			document.documentElement.style.backgroundColor = htmlBackground;
			document.body.style.backgroundColor = bodyBackground;
		};
	}, []);

	useEffect(() => {
		if (!electronAPI?.onChatScreenCapture) return;
		return electronAPI.onChatScreenCapture((dataUrl: string) => {
			if (typeof dataUrl !== "string" || !dataUrl.startsWith("data:image/")) return;
			setPendingUserImageUrls((prev) => [...prev, dataUrl]);
		});
	}, [electronAPI, setPendingUserImageUrls]);

	useEffect(() => {
		const activeSeacrhSpaceId =
			typeof workspace_id === "string"
				? workspace_id
				: Array.isArray(workspace_id) && workspace_id.length > 0
					? workspace_id[0]
					: "";
		if (!activeSeacrhSpaceId) return;
		setActiveWorkspaceIdState(activeSeacrhSpaceId);

		// Sync to Electron store if stored value is null (first navigation)
		if (electronAPI?.getActiveWorkspace && electronAPI.setActiveWorkspace) {
			const setActiveWorkspace = electronAPI.setActiveWorkspace;
			electronAPI
				.getActiveWorkspace()
				.then((stored: string | null) => {
					if (!stored) {
						setActiveWorkspace(activeSeacrhSpaceId);
					}
				})
				.catch(() => {});
		}
	}, [workspace_id, setActiveWorkspaceIdState, electronAPI]);

	// Block children until the workspace is synced and the initial verdict is
	// in; afterwards the composer renders its own not-ready state in place, so
	// recovery never triggers a full-screen loader or a redirect.
	const shouldShowLoading = noAccess || (!setupError && (!isWorkspaceReady || setupLoading));

	useGlobalLoadingEffect(shouldShowLoading);

	// Wire desktop app file watcher -> single-file re-index API
	useFolderSync();

	if (shouldShowLoading) {
		return null;
	}

	if (setupError) {
		return (
			<div className="flex flex-col items-center justify-center min-h-screen space-y-4">
				<Card className="w-full max-w-[400px] bg-background/60 backdrop-blur-sm border-destructive/20">
					<CardHeader className="pb-2">
						<CardTitle className="text-xl font-medium text-destructive">
							{t("config_error")}
						</CardTitle>
						<CardDescription>{t("failed_load_llm_config")}</CardDescription>
					</CardHeader>
					<CardContent>
						<p className="text-sm text-muted-foreground">
							{setupError instanceof Error ? setupError.message : String(setupError)}
						</p>
					</CardContent>
				</Card>
			</div>
		);
	}

	return (
		<RetrievalScopeProvider
			key={workspaceId}
			workspaceId={Number(workspaceId)}
			initialScope={initialRetrievalScope}
		>
			<DocumentUploadDialogProvider>
				<LayoutDataProvider
					workspaceId={workspaceId}
					initialRightPanelCollapsed={initialRightPanelCollapsed}
				>
					{children}
				</LayoutDataProvider>
			</DocumentUploadDialogProvider>
		</RetrievalScopeProvider>
	);
}
