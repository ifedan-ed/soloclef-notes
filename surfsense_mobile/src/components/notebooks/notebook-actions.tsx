import { useState } from "react";
import { View } from "react-native";
import type { Notebook } from "../../lib/api/schemas";
import { useApp } from "../../lib/app-context";
import { space } from "../../theme/colors";
import { Sheet } from "../ui/layout";
import { AppText, Button, Icon, Notice, PressableRow, TextField } from "../ui/primitives";

type Mode = "menu" | "rename" | "delete";

/**
 * Rename / delete for one notebook, as an in-app sheet: the delete
 * confirmation is a step inside it, never a system dialog.
 */
export function NotebookActionsSheet({
	notebook,
	onClose,
	onRename,
	onDelete,
	initialMode = "menu",
}: {
	notebook: Notebook | null;
	onClose: () => void;
	onRename: (title: string) => Promise<void>;
	onDelete: () => Promise<void>;
	initialMode?: Mode;
}) {
	const { t, colors } = useApp();
	const [mode, setMode] = useState<Mode>(initialMode);
	const [draft, setDraft] = useState("");
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);

	// Each opening starts fresh (state adjusted while rendering, not in an effect).
	const [shown, setShown] = useState<Notebook | null>(null);
	if (notebook !== shown) {
		setShown(notebook);
		if (notebook) {
			setMode(initialMode);
			setDraft(notebook.title);
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
		} finally {
			setBusy(false);
		}
	};

	const title =
		mode === "delete" && notebook
			? t("notebooks.delete_confirm_title", { title: notebook.title })
			: mode === "rename"
				? t("notebooks.rename")
				: notebook?.title;

	return (
		<Sheet visible={!!notebook} onClose={onClose} title={title}>
			{mode === "menu" ? (
				<View style={{ marginHorizontal: -space.lg }}>
					<PressableRow onPress={() => setMode("rename")} accessibilityRole="button">
						<Icon name="pencil-outline" />
						<AppText>{t("notebooks.rename")}</AppText>
					</PressableRow>
					<PressableRow onPress={() => setMode("delete")} accessibilityRole="button">
						<Icon name="trash-outline" color={colors.destructive} />
						<AppText tone="destructive">{t("notebooks.delete")}</AppText>
					</PressableRow>
				</View>
			) : null}

			{mode === "rename" ? (
				<>
					<TextField
						value={draft}
						onChangeText={setDraft}
						autoFocus
						maxLength={200}
						selectTextOnFocus
						accessibilityLabel={t("notebooks.notebook_title")}
						returnKeyType="done"
						onSubmitEditing={() => draft.trim() && run(() => onRename(draft.trim()), t("notebooks.rename_failed"))}
					/>
					{error ? <Notice tone="error" message={error} /> : null}
					<View style={{ flexDirection: "row", gap: space.sm, justifyContent: "flex-end" }}>
						<Button kind="ghost" title={t("notebooks.cancel")} onPress={onClose} />
						<Button
							title={busy ? t("notebooks.saving") : t("notebooks.save")}
							busy={busy}
							disabled={!draft.trim()}
							onPress={() => run(() => onRename(draft.trim()), t("notebooks.rename_failed"))}
						/>
					</View>
				</>
			) : null}

			{mode === "delete" ? (
				<>
					<AppText tone="muted">{t("notebooks.delete_confirm_body")}</AppText>
					{error ? <Notice tone="error" message={error} /> : null}
					<View style={{ flexDirection: "row", gap: space.sm, justifyContent: "flex-end" }}>
						<Button kind="ghost" title={t("notebooks.cancel")} onPress={onClose} />
						<Button
							kind="destructive"
							title={busy ? t("notebooks.deleting") : t("notebooks.delete")}
							busy={busy}
							onPress={() => run(onDelete, t("notebooks.delete_failed"))}
						/>
					</View>
				</>
			) : null}
		</Sheet>
	);
}
