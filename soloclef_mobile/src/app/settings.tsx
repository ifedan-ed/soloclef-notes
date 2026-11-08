import Constants from "expo-constants";
import * as WebBrowser from "expo-web-browser";
import { useRouter } from "expo-router";
import { useState } from "react";
import { Pressable, ScrollView, StyleSheet, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { Header, Screen, Sheet } from "../components/ui/layout";
import { AppText, Button, Divider, Icon, Segmented } from "../components/ui/primitives";
import { useApp } from "../lib/app-context";
import { billingUrl, formatCredits } from "../lib/billing";
import { LOCALE_NAMES, SUPPORTED_LOCALES } from "../lib/i18n/translate";
import { useBilling, useMe, useWorkspace } from "../lib/queries";
import type { ThemePreference } from "../lib/storage";
import { radius, space } from "../theme/colors";

export default function SettingsScreen() {
	const { t, prefs, updatePrefs, signOut, api, queryClient, locale } = useApp();
	const router = useRouter();
	const insets = useSafeAreaInsets();
	const me = useMe();
	const billing = useBilling();
	const { workspaces, workspaceId } = useWorkspace();
	const [confirm, setConfirm] = useState<"signout" | "server" | null>(null);
	const [busy, setBusy] = useState(false);

	const host = (() => {
		try {
			return new URL(api.serverUrl).host;
		} catch {
			return api.serverUrl;
		}
	})();

	return (
		<Screen>
			<Header onBack={() => router.back()} backLabel={t("notebooks.back")} title={t("notebooks.settings")} />
			<ScrollView
				contentContainerStyle={{ padding: space.lg, gap: space.xl, paddingBottom: insets.bottom + space.xxl }}
			>
				<Section title={t("mobile.account")}>
					<AppText>{me.data ? t("mobile.signed_in_as", { email: me.data.email }) : " "}</AppText>
					<Row icon="server-outline" label={t("mobile.server")} value={host} onPress={() => setConfirm("server")} />
				</Section>

				{billing.data ? (
					<Section title={t("mobile.plan_and_credits")}>
						<Row icon="ribbon-outline" label={t("mobile.plan")} value={billing.data.plan?.name ?? ""} />
						{billing.data.exempt ? (
							<Row icon="infinite-outline" label={t("mobile.credits")} value={t("mobile.unlimited")} />
						) : (
							<>
								<Row
									icon="flash-outline"
									label={t("mobile.credits")}
									value={t("mobile.credits_left", {
										left: formatCredits(billing.data.balances.plan_credits, locale),
										total: formatCredits(billing.data.balances.plan_allowance, locale),
									})}
								/>
								<Row
									icon="wallet-outline"
									label={t("mobile.topup_credits")}
									value={formatCredits(billing.data.balances.topup_credits, locale)}
								/>
							</>
						)}
						{billing.data.mode === "shadow" || billing.data.mode === "off" ? (
							<AppText variant="caption" tone="muted">
								{t("mobile.billing_not_charged")}
							</AppText>
						) : null}
						<Row
							icon="open-outline"
							label={t("mobile.manage_billing")}
							value=""
							onPress={() => {
								// Plans and credits are bought on the web, never in the app (store rules).
								void WebBrowser.openBrowserAsync(billingUrl(api.serverUrl)).then(() => billing.refetch());
							}}
						/>
					</Section>
				) : null}

				{workspaces.length > 1 ? (
					<Section title={t("mobile.workspace")}>
						{workspaces.map((w) => (
							<Choice
								key={w.id}
								label={w.name}
								selected={w.id === workspaceId}
								onPress={() => {
									updatePrefs({ workspaceId: w.id });
									void queryClient.invalidateQueries({ queryKey: ["notebooks"] });
								}}
							/>
						))}
					</Section>
				) : null}

				<Section title={t("mobile.appearance")}>
					<Segmented<ThemePreference>
						accessibilityLabel={t("mobile.appearance")}
						value={prefs.theme}
						onChange={(theme) => updatePrefs({ theme })}
						options={[
							{ value: "system", label: t("mobile.theme_system") },
							{ value: "light", label: t("mobile.theme_light") },
							{ value: "dark", label: t("mobile.theme_dark") },
						]}
					/>
				</Section>

				<Section title={t("mobile.language")}>
					<Choice
						label={t("mobile.language_system")}
						selected={!prefs.language}
						onPress={() => updatePrefs({ language: "" })}
					/>
					{SUPPORTED_LOCALES.map((code) => (
						<Choice
							key={code}
							label={LOCALE_NAMES[code]}
							selected={prefs.language === code}
							onPress={() => updatePrefs({ language: code })}
						/>
					))}
				</Section>

				<Divider />
				<Button
					kind="outline"
					icon="log-out-outline"
					title={t("mobile.sign_out")}
					onPress={() => setConfirm("signout")}
				/>
				<AppText variant="caption" tone="muted" style={{ textAlign: "center" }}>
					{t("mobile.version", { version: Constants.expoConfig?.version ?? "1.0.0" })}
				</AppText>
			</ScrollView>

			<Sheet
				visible={confirm !== null}
				onClose={() => setConfirm(null)}
				title={confirm === "server" ? t("mobile.server_change") : t("mobile.sign_out_confirm")}
			>
				{confirm === "server" ? <AppText tone="muted">{t("mobile.server_change_confirm")}</AppText> : null}
				<View style={{ flexDirection: "row", gap: space.sm, justifyContent: "flex-end" }}>
					<Button kind="ghost" title={t("notebooks.cancel")} onPress={() => setConfirm(null)} />
					<Button
						kind={confirm === "signout" ? "destructive" : "primary"}
						title={confirm === "server" ? t("mobile.server_change") : t("mobile.sign_out")}
						busy={busy}
						onPress={async () => {
							setBusy(true);
							await signOut();
							setBusy(false);
							setConfirm(null);
						}}
					/>
				</View>
			</Sheet>
		</Screen>
	);
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
	return (
		<View style={{ gap: space.sm }}>
			<AppText variant="label" tone="muted">
				{title}
			</AppText>
			{children}
		</View>
	);
}

function Row({
	icon,
	label,
	value,
	onPress,
}: {
	icon: React.ComponentProps<typeof Icon>["name"];
	label: string;
	value: string;
	/** Without one the row is read-only. */
	onPress?: () => void;
}) {
	const { colors } = useApp();
	const content = (
		<>
			<Icon name={icon} size={18} color={colors.mutedForeground} />
			<AppText style={{ flex: 1 }}>{label}</AppText>
			<AppText tone="muted" numberOfLines={1} style={{ maxWidth: "55%" }}>
				{value}
			</AppText>
		</>
	);
	if (!onPress) return <View style={[styles.row, { borderColor: colors.border }]}>{content}</View>;
	return (
		<Pressable onPress={onPress} accessibilityRole="button" style={[styles.row, { borderColor: colors.border }]}>
			{content}
		</Pressable>
	);
}

function Choice({ label, selected, onPress }: { label: string; selected: boolean; onPress: () => void }) {
	const { colors } = useApp();
	return (
		<Pressable
			onPress={onPress}
			accessibilityRole="radio"
			accessibilityState={{ selected }}
			style={[styles.row, { borderColor: selected ? colors.foreground : colors.border }]}
		>
			<AppText style={{ flex: 1 }}>{label}</AppText>
			{selected ? <Icon name="checkmark" /> : null}
		</Pressable>
	);
}

const styles = StyleSheet.create({
	row: {
		flexDirection: "row",
		alignItems: "center",
		gap: space.md,
		borderWidth: 1,
		borderRadius: radius.md,
		minHeight: 48,
		paddingHorizontal: space.md,
	},
});
