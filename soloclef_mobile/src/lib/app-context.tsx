import { QueryClient } from "@tanstack/react-query";
import { fetch as expoFetch } from "expo/fetch";
import { getLocales } from "expo-localization";
import { createContext, type ReactNode, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { useColorScheme } from "react-native";
import { dark, light, type Palette } from "../theme/colors";
import { ApiClient, type FetchLike, normalizeBaseUrl } from "./api/client";
import { createEndpoints, type Endpoints } from "./api/endpoints";
import { ApiError, NetworkError } from "./api/errors";
import { createTranslator, type Locale, resolveLocale, type Translate } from "./i18n/translate";
import { DEFAULT_PREFERENCES, loadPreferences, type Preferences, savePreferences, secureTokenStore } from "./storage";

/** expo/fetch streams response bodies on device, which the chat needs. */
export const streamingFetch = expoFetch as unknown as FetchLike;

export type AuthState = "loading" | "signedOut" | "signedIn";

interface AppContextValue {
	auth: AuthState;
	api: ApiClient;
	ep: Endpoints;
	queryClient: QueryClient;
	prefs: Preferences;
	updatePrefs: (changes: Partial<Preferences>) => void;
	signIn: (serverUrl: string, email: string, password: string) => Promise<void>;
	signOut: () => Promise<void>;
	/** Why the user landed on the sign-in screen (session ended), if they did not choose to. */
	notice: string | null;
	locale: Locale;
	t: Translate;
	colors: Palette;
	scheme: "light" | "dark";
}

const AppContext = createContext<AppContextValue | null>(null);

export function createQueryClient(): QueryClient {
	return new QueryClient({
		defaultOptions: {
			queries: {
				staleTime: 30_000,
				retry: (count, error) => {
					if (error instanceof ApiError && error.status >= 400 && error.status < 500) return false;
					return count < 2;
				},
			},
		},
	});
}

export function AppProvider({ children }: { children: ReactNode }) {
	const systemScheme = useColorScheme();
	const [prefs, setPrefs] = useState<Preferences>(DEFAULT_PREFERENCES);
	const [auth, setAuth] = useState<AuthState>("loading");
	const [notice, setNotice] = useState<string | null>(null);
	const [queryClient] = useState(createQueryClient);
	const [api] = useState(
		() =>
			new ApiClient({
				baseUrl: DEFAULT_PREFERENCES.serverUrl,
				store: secureTokenStore,
				fetch: streamingFetch,
			})
	);
	const ep = useMemo(() => createEndpoints(api), [api]);

	const locale = resolveLocale(
		prefs.language,
		getLocales().map((l) => l.languageTag)
	);
	const t = useMemo(() => createTranslator(locale), [locale]);
	const scheme: "light" | "dark" =
		prefs.theme === "system" ? (systemScheme === "dark" ? "dark" : "light") : prefs.theme;

	// Session ended on the server (refresh refused): back to sign-in, saying why.
	useEffect(() => {
		api.setSessionExpiredHandler(() => {
			queryClient.clear();
			setNotice(t("mobile.session_expired"));
			setAuth("signedOut");
		});
	}, [api, queryClient, t]);

	useEffect(() => {
		let cancelled = false;
		(async () => {
			const loaded = await loadPreferences();
			api.setBaseUrl(loaded.serverUrl);
			const hasSession = await api.hasSession();
			if (cancelled) return;
			setPrefs(loaded);
			setAuth(hasSession ? "signedIn" : "signedOut");
		})();
		return () => {
			cancelled = true;
		};
	}, [api]);

	const updatePrefs = useCallback((changes: Partial<Preferences>) => {
		setPrefs((current) => {
			const next = { ...current, ...changes };
			void savePreferences(next).catch(() => {});
			return next;
		});
	}, []);

	const signIn = useCallback(
		async (serverUrl: string, email: string, password: string) => {
			const url = normalizeBaseUrl(serverUrl);
			api.setBaseUrl(url);
			try {
				await api.login(email, password);
			} catch (error) {
				if (error instanceof ApiError && error.message === "LOGIN_BAD_CREDENTIALS") {
					throw new Error(t("mobile.bad_credentials"));
				}
				if (error instanceof NetworkError) throw new Error(t("mobile.offline"));
				if (error instanceof ApiError && error.message) throw new Error(error.message);
				throw new Error(t("mobile.sign_in_failed"));
			}
			queryClient.clear();
			if (url !== prefs.serverUrl) updatePrefs({ serverUrl: url, workspaceId: null });
			setNotice(null);
			setAuth("signedIn");
		},
		[api, queryClient, prefs.serverUrl, t, updatePrefs]
	);

	const signOut = useCallback(async () => {
		await api.logout();
		queryClient.clear();
		setNotice(null);
		setAuth("signedOut");
	}, [api, queryClient]);

	const value = useMemo<AppContextValue>(
		() => ({
			auth,
			api,
			ep,
			queryClient,
			prefs,
			updatePrefs,
			signIn,
			signOut,
			notice,
			locale,
			t,
			colors: scheme === "dark" ? dark : light,
			scheme,
		}),
		[auth, api, ep, queryClient, prefs, updatePrefs, signIn, signOut, notice, locale, t, scheme]
	);

	return <AppContext.Provider value={value}>{children}</AppContext.Provider>;
}

export function useApp(): AppContextValue {
	const value = useContext(AppContext);
	if (!value) throw new Error("useApp outside AppProvider");
	return value;
}
