import { useState } from "react";
import { View } from "react-native";
import type { StudioOutput } from "../../lib/api/schemas";
import { useApp } from "../../lib/app-context";
import { space } from "../../theme/colors";
import { Sheet } from "../ui/layout";
import { AppText, Button, Icon, Notice, PressableRow, TextField } from "../ui/primitives";

type Mode = "menu" | "rename" | "delete";

/** Open / try again / rename / delete for a Studio output, confirmations inside the sheet. */
export function OutputActionsSheet({
	output,
	onClose,
	onOpen,
	onRetry,
	onRename,
	onDelete,
}: {
	output: StudioOutput | null;
	onClose: () => void;
	onOpen: () => void;
	onRetry: () => void;
	onRename: (title: string) => Promise<void>;
	onDelete: () => Promise<void>;
}) {
	const { t, colors } = useApp();
	const [mode, setMode] = useState<Mode>("menu");
	const [draft, setDraft] = useState("");
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);

	const [shown, setShown] = useState<StudioOutput | null>(null);
	if (output !== shown) {
		setShown(output);
		if (output) {
			setMode("menu");
			setDraft(output.title);
			setError(null);
			setBusy(false);
		}
	}

	const run = async (action: () => Promise<void>, failure: string) => {
		setBusy(true);
		setError(null);
		try {
			await action();
			onClose();
		} catch {
			setError(failure);
			setBusy(false);
		}
	};

	const title =
		mode === "delete" && output
			? t("notebooks.delete_output_confirm", { title: output.title })
			: mode === "rename"
				? t("notebooks.rename")
				: output?.title;

	return (
		<Sheet visible={!!output} onClose={onClose} title={title}>
			{mode === "menu" && output ? (
				<View style={{ marginHorizontal: -space.lg }}>
					{output.status === "ready" ? (
						<PressableRow
							onPress={() => {
								onClose();
								onOpen();
							}}
							accessibilityRole="button"
						>
							<Icon name="open-outline" />
							<AppText>{t("notebooks.open")}</AppText>
						</PressableRow>
					) : null}
					{output.status === "failed" || output.status === "cancelled" ? (
						<PressableRow
							onPress={() => {
								onClose();
								onRetry();
							}}
							accessibilityRole="button"
						>
							<Icon name="refresh-outline" />
							<AppText>{t("notebooks.try_again")}</AppText>
						</PressableRow>
					) : null}
					<PressableRow onPress={() => setMode("rename")} accessibilityRole="button">
						<Icon name="pencil-outline" />
						<AppText>{t("notebooks.rename")}</AppText>
					</PressableRow>
					<PressableRow onPress={() => setMode("delete")} accessibilityRole="button">
						<Icon name="trash-outline" color={colors.destructive} />
						<AppText tone="destructive">{t("notebooks.delete")}</AppText>
					</PressableRow>
					{output.generated_by?.model ? (
						<AppText variant="caption" tone="muted" style={{ paddingHorizontal: space.lg, paddingTop: space.sm }}>
							{t(output.generated_by.fallback ? "notebooks.made_with_fallback" : "notebooks.made_with", {
								model: output.generated_by.model,
							})}
						</AppText>
					) : null}
				</View>
			) : null}
			{mode === "rename" ? (
				<>
					<TextField
						value={draft}
						onChangeText={setDraft}
						autoFocus
						maxLength={200}
						accessibilityLabel={t("notebooks.rename")}
					/>
					{error ? <Notice tone="error" message={error} /> : null}
					<View style={{ flexDirection: "row", gap: space.sm, justifyContent: "flex-end" }}>
						<Button kind="ghost" title={t("notebooks.cancel")} onPress={onClose} />
						<Button
							title={t("notebooks.save")}
							busy={busy}
							disabled={!draft.trim()}
							onPress={() => run(() => onRename(draft.trim()), t("notebooks.rename_failed"))}
						/>
					</View>
				</>
			) : null}
			{mode === "delete" ? (
				<>
					{error ? <Notice tone="error" message={error} /> : null}
					<View style={{ flexDirection: "row", gap: space.sm, justifyContent: "flex-end" }}>
						<Button kind="ghost" title={t("notebooks.cancel")} onPress={onClose} />
						<Button
							kind="destructive"
							title={t("notebooks.delete")}
							busy={busy}
							onPress={() => run(onDelete, t("notebooks.delete_failed"))}
						/>
					</View>
				</>
			) : null}
		</Sheet>
	);
}
