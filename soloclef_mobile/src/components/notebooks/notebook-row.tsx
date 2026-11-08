import { StyleSheet, View } from "react-native";
import type { Notebook } from "../../lib/api/schemas";
import { useApp } from "../../lib/app-context";
import { relativeTime } from "../../lib/util/notebooks";
import { radius } from "../../theme/colors";
import { AppText, IconButton, PressableRow } from "../ui/primitives";

export function NotebookRow({
	notebook,
	onPress,
	onMore,
}: {
	notebook: Notebook;
	onPress: () => void;
	onMore: () => void;
}) {
	const { t, locale, colors } = useApp();
	const meta = [
		t("notebooks.source_count", { count: notebook.source_count }),
		relativeTime(notebook.updated_at, locale),
	].join(" · ");
	return (
		<View style={styles.wrap}>
			<PressableRow
				onPress={onPress}
				onLongPress={onMore}
				accessibilityRole="button"
				accessibilityLabel={`${notebook.title}, ${meta}`}
				style={{ flex: 1, paddingRight: 0 }}
			>
				<View style={[styles.icon, { backgroundColor: colors.muted }]}>
					<AppText style={{ fontSize: 22 }}>{notebook.icon || "📓"}</AppText>
				</View>
				<View style={{ flex: 1, minWidth: 0 }}>
					<AppText variant="subheading" numberOfLines={2}>
						{notebook.title}
					</AppText>
					<AppText variant="caption" tone="muted" numberOfLines={1}>
						{meta}
					</AppText>
				</View>
			</PressableRow>
			<IconButton
				name="ellipsis-vertical"
				label={t("notebooks.notebook_menu")}
				onPress={onMore}
				color={colors.mutedForeground}
				style={{ marginRight: 8 }}
			/>
		</View>
	);
}

const styles = StyleSheet.create({
	wrap: { flexDirection: "row", alignItems: "center" },
	icon: { width: 44, height: 44, borderRadius: radius.md, alignItems: "center", justifyContent: "center" },
});
