import { Image } from "expo-image";
import { useRef, useState } from "react";
import { KeyboardAvoidingView, Platform, Pressable, ScrollView, StyleSheet, type TextInput, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { AppText, Button, Icon, Notice, TextField } from "../components/ui/primitives";
import { isPlausibleServerUrl, normalizeBaseUrl } from "../lib/api/client";
import { useApp } from "../lib/app-context";
import { space } from "../theme/colors";

export default function LoginScreen() {
	const { t, prefs, signIn, notice, colors } = useApp();
	const insets = useSafeAreaInsets();
	const [server, setServer] = useState(prefs.serverUrl);
	const [editingServer, setEditingServer] = useState(!prefs.serverUrl);
	const [email, setEmail] = useState("");
	const [password, setPassword] = useState("");
	const [showPassword, setShowPassword] = useState(false);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const passwordRef = useRef<TextInput>(null);

	const serverHost = (() => {
		try {
			return new URL(normalizeBaseUrl(server)).host;
		} catch {
			return server;
		}
	})();

	const submit = async () => {
		setError(null);
		if (!isPlausibleServerUrl(server)) {
			setError(t("mobile.server_invalid"));
			setEditingServer(true);
			return;
		}
		if (!email.trim()) return setError(t("auth.email_required"));
		if (!password) return setError(t("auth.password_required"));
		setBusy(true);
		try {
			await signIn(server, email, password);
		} catch (e) {
			setError(e instanceof Error ? e.message : t("mobile.sign_in_failed"));
			setBusy(false);
		}
	};

	return (
		<KeyboardAvoidingView
			behavior={Platform.OS === "ios" ? "padding" : "height"}
			style={{ flex: 1, backgroundColor: colors.background }}
		>
			<ScrollView
				keyboardShouldPersistTaps="handled"
				contentContainerStyle={[
					styles.container,
					{ paddingTop: insets.top + space.xxl, paddingBottom: insets.bottom + space.xl },
				]}
			>
				<View style={styles.brand}>
					<Image
						source={require("../../assets/images/icon.png")}
						style={styles.logo}
						contentFit="contain"
						accessibilityIgnoresInvertColors
					/>
					<AppText variant="title">{t("auth.welcome_back")}</AppText>
					<AppText tone="muted" style={{ textAlign: "center" }}>
						{t("auth.login_subtitle")}
					</AppText>
				</View>

				{notice ? <Notice message={notice} /> : null}

				<View style={styles.field}>
					<AppText variant="label">{t("mobile.server")}</AppText>
					{editingServer ? (
						<TextField
							value={server}
							onChangeText={setServer}
							autoCapitalize="none"
							autoCorrect={false}
							keyboardType="url"
							textContentType="URL"
							placeholder="https://notes.example.com"
							accessibilityLabel={t("mobile.server")}
							accessibilityHint={t("mobile.server_hint")}
						/>
					) : (
						<Pressable
							onPress={() => setEditingServer(true)}
							accessibilityRole="button"
							accessibilityLabel={`${t("mobile.server")}: ${serverHost}. ${t("mobile.server_change")}`}
							style={[styles.serverRow, { borderColor: colors.border }]}
						>
							<Icon name="server-outline" size={18} color={colors.mutedForeground} />
							<AppText style={{ flex: 1 }} numberOfLines={1}>
								{serverHost}
							</AppText>
							<AppText variant="small" tone="muted">
								{t("mobile.server_change")}
							</AppText>
						</Pressable>
					)}
				</View>

				<View style={styles.field}>
					<AppText variant="label">{t("auth.email")}</AppText>
					<TextField
						value={email}
						onChangeText={setEmail}
						autoCapitalize="none"
						autoCorrect={false}
						autoComplete="email"
						keyboardType="email-address"
						textContentType="username"
						returnKeyType="next"
						onSubmitEditing={() => passwordRef.current?.focus()}
						accessibilityLabel={t("auth.email")}
					/>
				</View>

				<View style={styles.field}>
					<AppText variant="label">{t("auth.password")}</AppText>
					<View>
						<TextField
							ref={passwordRef}
							value={password}
							onChangeText={setPassword}
							secureTextEntry={!showPassword}
							autoCapitalize="none"
							autoCorrect={false}
							autoComplete="current-password"
							textContentType="password"
							returnKeyType="go"
							onSubmitEditing={submit}
							accessibilityLabel={t("auth.password")}
							style={{ paddingRight: 48 }}
						/>
						<Pressable
							onPress={() => setShowPassword((v) => !v)}
							accessibilityRole="button"
							accessibilityLabel={showPassword ? t("auth.hide_password") : t("auth.show_password")}
							style={styles.eye}
							hitSlop={8}
						>
							<Icon name={showPassword ? "eye-off-outline" : "eye-outline"} color={colors.mutedForeground} />
						</Pressable>
					</View>
				</View>

				{error ? <Notice tone="error" message={error} /> : null}

				<Button title={busy ? t("mobile.signing_in") : t("auth.sign_in")} onPress={submit} busy={busy} />
			</ScrollView>
		</KeyboardAvoidingView>
	);
}

const styles = StyleSheet.create({
	container: { paddingHorizontal: space.xl, gap: space.lg, maxWidth: 480, width: "100%", alignSelf: "center" },
	brand: { alignItems: "center", gap: space.sm, marginBottom: space.md },
	logo: { width: 72, height: 72, borderRadius: 16 },
	field: { gap: space.xs },
	serverRow: {
		flexDirection: "row",
		alignItems: "center",
		gap: space.sm,
		borderWidth: 1,
		borderRadius: 10,
		paddingHorizontal: space.md,
		minHeight: 44,
	},
	eye: { position: "absolute", right: 12, top: 0, bottom: 0, justifyContent: "center" },
});
