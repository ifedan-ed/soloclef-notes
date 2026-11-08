"use client";

import { createContext, useContext } from "react";

export type AuthType = "LOCAL" | "GOOGLE" | string;
export type DeploymentMode = "self-hosted" | "cloud" | string;

export interface RuntimeConfigValue {
	authType: AuthType;
	etlService: string;
	deploymentMode: DeploymentMode;
	maxFileSizeMB: number;
	/** Cloudflare Turnstile site key; null leaves the human check off. */
	turnstileSiteKey: string | null;
	/** Sign-in through Authentik (OIDC) offered beside the password form. */
	oidcEnabled: boolean;
	/** The words on that button (OIDC_BUTTON_LABEL). */
	oidcButtonLabel: string;
}

const RuntimeConfigContext = createContext<RuntimeConfigValue | null>(null);

export function RuntimeConfigProvider({
	value,
	children,
}: {
	value: RuntimeConfigValue;
	children: React.ReactNode;
}) {
	return <RuntimeConfigContext.Provider value={value}>{children}</RuntimeConfigContext.Provider>;
}

export function useRuntimeConfig() {
	const context = useContext(RuntimeConfigContext);
	if (!context) {
		throw new Error("useRuntimeConfig must be used within RuntimeConfigProvider");
	}
	return context;
}

/** For components that may render outside RuntimeConfigProvider (e.g. anonymous /free pages). */
export function useOptionalRuntimeConfig(): RuntimeConfigValue | null {
	return useContext(RuntimeConfigContext);
}

export function useIsLocalAuth() {
	return useRuntimeConfig().authType === "LOCAL";
}

export function useIsGoogleAuth() {
	return useRuntimeConfig().authType === "GOOGLE";
}

export function useIsSelfHosted() {
	return useRuntimeConfig().deploymentMode === "self-hosted";
}

export function useIsCloud() {
	return useRuntimeConfig().deploymentMode === "cloud";
}
