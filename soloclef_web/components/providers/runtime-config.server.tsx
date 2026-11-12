import { connection } from "next/server";
import { RuntimeConfigProvider } from "@/components/providers/runtime-config";
import { BUILD_TIME_DEPLOYMENT_MODE } from "@/lib/env-config";
import { DEFAULT_SSO_BUTTON_LABEL } from "@/lib/sso-settings";

export async function RuntimeConfig({ children }: { children: React.ReactNode }) {
	await connection();

	const value = {
		deploymentMode: process.env.DEPLOYMENT_MODE ?? BUILD_TIME_DEPLOYMENT_MODE,
		// Must match the backend's OIDC_ENABLED, or the button leads nowhere.
		oidcEnabled: process.env.OIDC_ENABLED?.trim().toUpperCase() === "TRUE",
		oidcButtonLabel: process.env.OIDC_BUTTON_LABEL?.trim() || DEFAULT_SSO_BUTTON_LABEL,
	};

	return <RuntimeConfigProvider value={value}>{children}</RuntimeConfigProvider>;
}
