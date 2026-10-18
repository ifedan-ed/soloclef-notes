/**
 * Device storage: the session in the platform keystore (expo-secure-store:
 * Keychain on iOS, Keystore-backed on Android) and the app's preferences
 * alongside it. Nothing here ever leaves the device.
 */
import * as SecureStore from "expo-secure-store";
import type { TokenPair, TokenStore } from "./api/client";

// Builds may set EXPO_PUBLIC_DEFAULT_SERVER_URL (see README.md, "Your own
// server"). Without it the sign-in screen asks for the server first.
const configuredServerUrl = process.env.EXPO_PUBLIC_DEFAULT_SERVER_URL?.trim();

export const DEFAULT_SERVER_URL = configuredServerUrl || "";

export type ThemePreference = "system" | "light" | "dark";

export interface Preferences {
	serverUrl: string;
	theme: ThemePreference;
	/** "" follows the device language. */
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

const options: SecureStore.SecureStoreOptions = {
	keychainAccessible: SecureStore.AFTER_FIRST_UNLOCK,
};

function isTokenPair(value: unknown): value is TokenPair {
	const v = value as TokenPair;
	return (
		typeof v === "object" &&
		v !== null &&
		typeof v.accessToken === "string" &&
		typeof v.refreshToken === "string" &&
		typeof v.accessExpiresAt === "number"
	);
}

export const secureTokenStore: TokenStore = {
	async load() {
		try {
			const raw = await SecureStore.getItemAsync(SESSION_KEY, options);
			if (!raw) return null;
			const parsed = JSON.parse(raw);
			return isTokenPair(parsed) ? parsed : null;
		} catch {
			return null;
		}
	},
	async save(tokens) {
		await SecureStore.setItemAsync(SESSION_KEY, JSON.stringify(tokens), options);
	},
	async clear() {
		await SecureStore.deleteItemAsync(SESSION_KEY, options);
	},
};

export async function loadPreferences(): Promise<Preferences> {
	try {
		const raw = await SecureStore.getItemAsync(PREFS_KEY, options);
		if (!raw) return DEFAULT_PREFERENCES;
		return { ...DEFAULT_PREFERENCES, ...(JSON.parse(raw) as Partial<Preferences>) };
	} catch {
		return DEFAULT_PREFERENCES;
	}
}

export async function savePreferences(prefs: Preferences): Promise<void> {
	await SecureStore.setItemAsync(PREFS_KEY, JSON.stringify(prefs), options);
}
