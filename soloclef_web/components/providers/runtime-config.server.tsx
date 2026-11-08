import { connection } from "next/server";
import { RuntimeConfigProvider } from "@/components/providers/runtime-config";
import {
	BUILD_TIME_AUTH_TYPE,
	BUILD_TIME_DEPLOYMENT_MODE,
	BUILD_TIME_ETL_SERVICE,
} from "@/lib/env-config";
import { fallbackEtlService } from "@/lib/etl/fallback-service";
import { DEFAULT_SSO_BUTTON_LABEL } from "@/lib/sso-settings";

// Per-file upload cap. Falls back to 500 on unset, empty, or malformed
// MAX_FILE_SIZE_MB so an operator typo can't zero out uploads (empty string)
// or silently disable the pre-upload check (NaN).
function resolveMaxFileSizeMB(defaultValue = 500): number {
	const raw = process.env.MAX_FILE_SIZE_MB?.trim() ?? "";
	if (!raw) return defaultValue;

	const parsed = Number.parseInt(raw, 10);
	if (!Number.isFinite(parsed) || parsed <= 0) {
		console.warn(`Invalid MAX_FILE_SIZE_MB="${raw}", falling back to ${defaultValue} MB`);
		return defaultValue;
	}
	return parsed;
}

export async function RuntimeConfig({ children }: { children: React.ReactNode }) {
	await connection();

	const value = {
		authType: process.env.AUTH_TYPE ?? BUILD_TIME_AUTH_TYPE,
		// The upload dialog's file list until /etl/capabilities answers.
		etlService: fallbackEtlService(process.env) ?? BUILD_TIME_ETL_SERVICE,
		deploymentMode: process.env.DEPLOYMENT_MODE ?? BUILD_TIME_DEPLOYMENT_MODE,
		maxFileSizeMB: resolveMaxFileSizeMB(),
		turnstileSiteKey: process.env.TURNSTILE_SITE_KEY?.trim() || null,
		// Must match the backend's OIDC_ENABLED, or the button leads nowhere.
		oidcEnabled: process.env.OIDC_ENABLED?.trim().toUpperCase() === "TRUE",
		oidcButtonLabel: process.env.OIDC_BUTTON_LABEL?.trim() || DEFAULT_SSO_BUTTON_LABEL,
	};

	return <RuntimeConfigProvider value={value}>{children}</RuntimeConfigProvider>;
}
