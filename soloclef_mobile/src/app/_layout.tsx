import { focusManager, QueryClientProvider } from "@tanstack/react-query";
import { DarkTheme, DefaultTheme, SplashScreen, Stack, ThemeProvider } from "expo-router";
import { StatusBar } from "expo-status-bar";
import * as SystemUI from "expo-system-ui";
import { useEffect } from "react";
import { AppState, Platform } from "react-native";
import { SafeAreaProvider } from "react-native-safe-area-context";
import { ToastProvider } from "../components/ui/layout";
import { AppProvider, useApp } from "../lib/app-context";

SplashScreen.preventAutoHideAsync().catch(() => {});

// Refetch stale queries when the app comes back to the foreground.
focusManager.setEventListener((handleFocus) => {
	const subscription = AppState.addEventListener("change", (state) => {
		if (Platform.OS !== "web") handleFocus(state === "active");
	});
	return () => subscription.remove();
});

export default function RootLayout() {
	return (
		<SafeAreaProvider>
			<AppProvider>
				<Root />
			</AppProvider>
		</SafeAreaProvider>
	);
}

function Root() {
	const { auth, queryClient, colors, scheme } = useApp();

	useEffect(() => {
		if (auth !== "loading") SplashScreen.hideAsync().catch(() => {});
	}, [auth]);

	useEffect(() => {
		SystemUI.setBackgroundColorAsync(colors.background).catch(() => {});
	}, [colors.background]);

	// Keep the splash up until the stored session is read: no sign-in flash.
	if (auth === "loading") return null;

	const base = scheme === "dark" ? DarkTheme : DefaultTheme;
	const navTheme = {
		...base,
		colors: {
			...base.colors,
			background: colors.background,
			card: colors.background,
			text: colors.foreground,
			border: colors.border,
			primary: colors.primary,
		},
	};

	return (
		<QueryClientProvider client={queryClient}>
			<ThemeProvider value={navTheme}>
				<ToastProvider>
					<StatusBar style={scheme === "dark" ? "light" : "dark"} />
					<Stack
						screenOptions={{
							headerShown: false,
							contentStyle: { backgroundColor: colors.background },
							animation: "slide_from_right",
						}}
					>
						<Stack.Protected guard={auth === "signedIn"}>
							<Stack.Screen name="index" />
							<Stack.Screen name="settings" />
							<Stack.Screen name="notebook/[id]/index" />
							<Stack.Screen name="notebook/[id]/source/[sourceId]" />
							<Stack.Screen name="notebook/[id]/output/[outputId]" />
						</Stack.Protected>
						<Stack.Protected guard={auth !== "signedIn"}>
							<Stack.Screen name="login" options={{ animation: "fade" }} />
						</Stack.Protected>
					</Stack>
				</ToastProvider>
			</ThemeProvider>
		</QueryClientProvider>
	);
}
