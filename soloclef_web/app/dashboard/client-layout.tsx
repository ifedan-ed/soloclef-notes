"use client";

import { useAtomValue } from "jotai";
import { useTranslations } from "next-intl";
import type React from "react";
import { useEffect } from "react";
import { llmSetupStatusAtom } from "@/atoms/model-connections/model-connections-query.atoms";
import { DocumentUploadDialogProvider } from "@/components/assistant-ui/document-upload-popup";
import { RetrievalScopeProvider } from "@/components/assistant-ui/retrieval-scope-provider";
import { LayoutDataProvider } from "@/components/layout";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { usePresenceHeartbeat } from "@/features/presence/hooks/use-presence-heartbeat";
import { useGlobalLoadingEffect } from "@/hooks/use-global-loading";

/**
 * The signed-in app around every dashboard page: the account's model setup
 * verdict first (whether it can chat), then the providers every page shares.
 */
export function DashboardClientLayout({ children }: { children: React.ReactNode }) {
	const t = useTranslations("dashboard");

	// Whether the signed-in account can chat. No onboarding: every account
	// chats on the shared models from the start, and one that cannot (the
	// shared list is gone) sees the composer's inline notice instead.
	const { isLoading: setupLoading, error: setupError } = useAtomValue(llmSetupStatusAtom);

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

	// Block children until the first verdict is in; afterwards the composer
	// renders its own not-ready state in place, so recovery never triggers a
	// full-screen loader.
	const shouldShowLoading = !setupError && setupLoading;

	useGlobalLoadingEffect(shouldShowLoading);

	// No phone push while this tab is on screen: the person sees it here.
	usePresenceHeartbeat();

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
		<RetrievalScopeProvider>
			<DocumentUploadDialogProvider>
				<LayoutDataProvider>{children}</LayoutDataProvider>
			</DocumentUploadDialogProvider>
		</RetrievalScopeProvider>
	);
}
