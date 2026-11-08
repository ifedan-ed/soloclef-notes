/**
 * This server's single sign-on as its public pages describe it: whether there
 * is one, the button's name, where it lives and whom it makes accounts for.
 *
 * The docs are prerendered, so these are the build's values: docker-compose.yml
 * passes OIDC_ENABLED, OIDC_BUTTON_LABEL, OIDC_DISCOVERY_URL,
 * OIDC_AUTOCREATE_DOMAINS and OIDC_ALLOW_SIGNUP from docker/.env as the
 * NEXT_PUBLIC_OIDC_* build args below, the same lines the backend reads at
 * runtime. Change one, rebuild the frontend. The login page's own button
 * reads OIDC_ENABLED and OIDC_BUTTON_LABEL at runtime (runtime-config).
 */

/** The button's name when OIDC_BUTTON_LABEL is unset. */
export const DEFAULT_SSO_BUTTON_LABEL = "Continue with single sign-on";

/** Who a first single sign-on makes an account for (app/auth_oidc/account_creation_rule.py). */
export type SsoAccounts = "anyone" | "domains" | "nobody";

export interface SsoSettings {
	enabled: boolean;
	buttonLabel: string;
	/** The provider's host, from its discovery URL; null when unknown. */
	providerHost: string | null;
	accounts: SsoAccounts;
	/** The exact domains OIDC_AUTOCREATE_DOMAINS names, lowercased, without "@". */
	domains: string[];
}

export interface SsoEnv {
	enabled?: string;
	buttonLabel?: string;
	discoveryUrl?: string;
	autocreateDomains?: string;
	allowSignup?: string;
}

function flag(value: string | undefined): boolean {
	return ["TRUE", "1", "YES", "ON"].includes((value ?? "").trim().toUpperCase());
}

/** The same parsing as the backend's parse_domains: commas or spaces, "@" and a final "." ignored. */
export function parseDomains(value: string | undefined): string[] {
	const entries = (value ?? "")
		.split(/[\s,]+/)
		.map((part) => part.trim().toLowerCase().replace(/^@+/, "").replace(/\.+$/, ""))
		.filter(Boolean);
	return [...new Set(entries)].sort();
}

function hostOf(url: string | undefined): string | null {
	try {
		return url?.trim() ? new URL(url.trim()).host || null : null;
	} catch {
		return null;
	}
}

export function readSsoSettings(env: SsoEnv): SsoSettings {
	const domains = parseDomains(env.autocreateDomains);
	// OIDC_ALLOW_SIGNUP wins over the domain list, as in may_create_account.
	const accounts: SsoAccounts = flag(env.allowSignup)
		? "anyone"
		: domains.length
			? "domains"
			: "nobody";
	return {
		enabled: flag(env.enabled),
		buttonLabel: env.buttonLabel?.trim() || DEFAULT_SSO_BUTTON_LABEL,
		providerHost: hostOf(env.discoveryUrl),
		accounts,
		domains,
	};
}

/** "@a.com", "@a.com or @b.org", "@a.com, @b.org or @c.net". */
export function formatDomains(domains: string[]): string {
	const at = domains.map((domain) => `@${domain}`);
	if (at.length <= 1) return at[0] ?? "";
	return `${at.slice(0, -1).join(", ")} or ${at[at.length - 1]}`;
}

// Each NEXT_PUBLIC_* is spelled out so `next build` inlines it.
export const SSO_SETTINGS: SsoSettings = readSsoSettings({
	enabled: process.env.NEXT_PUBLIC_OIDC_ENABLED,
	buttonLabel: process.env.NEXT_PUBLIC_OIDC_BUTTON_LABEL,
	discoveryUrl: process.env.NEXT_PUBLIC_OIDC_DISCOVERY_URL,
	autocreateDomains: process.env.NEXT_PUBLIC_OIDC_AUTOCREATE_DOMAINS,
	allowSignup: process.env.NEXT_PUBLIC_OIDC_ALLOW_SIGNUP,
});
