import type { ReactNode } from "react";
import {
	formatDomains,
	SSO_SETTINGS,
	type SsoAccounts,
	type SsoSettings,
} from "@/lib/sso-settings";

/**
 * Docs prose about this server's single sign-on, filled from its own
 * settings (lib/sso-settings) rather than any one server's names, so every
 * deployment's docs describe its own button, provider and account rule.
 * `settings` is for tests; the docs use the build's values.
 */

interface WithSettings {
	settings?: SsoSettings;
}

/** The sign-in button's name, e.g. "Continue with single sign-on". */
export function SsoButtonLabel({ settings = SSO_SETTINGS }: WithSettings) {
	return <>{settings.buttonLabel}</>;
}

/** " (sso.example.com)" after the provider's description; nothing when unknown. */
export function SsoProviderHost({ settings = SSO_SETTINGS }: WithSettings) {
	return settings.providerHost ? ` (${settings.providerHost})` : null;
}

/** The domains whose addresses get an account, e.g. "@example.com". */
export function SsoDomains({ settings = SSO_SETTINGS }: WithSettings) {
	return <>{formatDomains(settings.domains)}</>;
}

/** Its children only when the server offers single sign-on. */
export function SsoOnly({
	settings = SSO_SETTINGS,
	children,
}: WithSettings & { children?: ReactNode }) {
	return settings.enabled ? children : null;
}

/**
 * Its children only when a first sign-in makes accounts for `accounts`:
 * "anyone" (OIDC_ALLOW_SIGNUP), "domains" (OIDC_AUTOCREATE_DOMAINS) or
 * "nobody" (neither, or no single sign-on at all), so each sentence about
 * new accounts is true on this server.
 */
export function SsoAccountsFor({
	accounts,
	settings = SSO_SETTINGS,
	children,
}: WithSettings & { accounts: SsoAccounts; children?: ReactNode }) {
	const here: SsoAccounts = settings.enabled ? settings.accounts : "nobody";
	return here === accounts ? children : null;
}
