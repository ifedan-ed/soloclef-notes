/**
 * Web fallback for development only (`expo start --web`): the platform
 * keystore does not exist in a browser, so the session and preferences live
 * in localStorage. The shipped apps use storage.ts (expo-secure-store).
 */
import type { TokenPair, TokenStore } from "./api/client";

const configuredServerUrl = process.env.EXPO_PUBLIC_DEFAULT_SERVER_URL?.trim();

export const DEFAULT_SERVER_URL = configuredServerUrl || "";

export type ThemePreference = "system" | "light" | "dark";

export interface Preferences {
	serverUrl: string;
	theme: ThemePreference;
	language: string;
	workspaceId: number | null;
}

export const DEFAULT_PREFERENCES: Preferences = {
	serverUrl: DEFAULT_SERVER_URL,
	theme: "system",
	language: "",
	workspaceId: null,
};

const SESSION_KEY = "surfsense.session";
const PREFS_KEY = "surfsense.prefs";

function read(key: string): unknown {
	try {
		const raw = globalThis.localStorage?.getItem(key);
		return raw ? JSON.parse(raw) : null;
	} catch {
		return null;
	}
}

function write(key: string, value: unknown): void {
	try {
		if (value === null) globalThis.localStorage?.removeItem(key);
		else globalThis.localStorage?.setItem(key, JSON.stringify(value));
	} catch {
		// storage blocked: the session lasts for this page only
	}
}

export const secureTokenStore: TokenStore = {
	async load() {
		const value = read(SESSION_KEY) as TokenPair | null;
		return value && typeof value.accessToken === "string" ? value : null;
	},
	async save(tokens) {
		write(SESSION_KEY, tokens);
	},
	async clear() {
		write(SESSION_KEY, null);
	},
};

export async function loadPreferences(): Promise<Preferences> {
	return { ...DEFAULT_PREFERENCES, ...((read(PREFS_KEY) as Partial<Preferences> | null) ?? {}) };
}

export async function savePreferences(prefs: Preferences): Promise<void> {
	write(PREFS_KEY, prefs);
}
