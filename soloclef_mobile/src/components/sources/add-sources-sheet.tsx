import * as DocumentPicker from "expo-document-picker";
import { useState } from "react";
import { View } from "react-native";
import { errorMessage } from "../../lib/api/errors";
import type { DocumentParser } from "../../lib/api/schemas";
import { useApp } from "../../lib/app-context";
import { type BillingAction, billingActionFor } from "../../lib/billing";
import { uploadDocument } from "../../lib/files";
import { useEtlCapabilities, useSourceMutations } from "../../lib/queries";
import { acceptedExtensions, fileExtension, normalizeLink, resolveParserChoice } from "../../lib/util/uploads";
import { space } from "../../theme/colors";
import { useBillingNoticeAction } from "../billing-link";
import { Sheet, useToast } from "../ui/layout";
import { AppText, Button, DelayedSpinner, Icon, Notice, PressableRow, Segmented, TextField } from "../ui/primitives";

export type UploadState =
	| { status: "idle" }
	| { status: "uploading"; name: string }
	| { status: "failed"; message: string; billing?: BillingAction | null };

type Mode = "choose" | "link" | "upload";

/**
 * Add a web link or upload files from the phone. Uploads offer the same
 * Local/Cloud parser choice as the web's upload tab, refuse file types the
 * chosen parser can't read, then link the new documents to the notebook.
 */
export function AddSourcesSheet({
	visible,
	onClose,
	workspaceId,
	notebookId,
	onUploadState,
}: {
	visible: boolean;
	onClose: () => void;
	workspaceId: number;
	notebookId: number;
	onUploadState: (state: UploadState) => void;
}) {
	const { t, api } = useApp();
	const toast = useToast();
	const mutations = useSourceMutations(workspaceId, notebookId);
	const capabilities = useEtlCapabilities(visible);
	const [mode, setMode] = useState<Mode>("choose");
	const [link, setLink] = useState("");
	const [error, setErrorState] = useState<{ message: string; billing: BillingAction | null } | null>(null);
	const setError = (message: string | null, cause?: unknown) =>
		setErrorState(message === null ? null : { message, billing: billingActionFor(cause) });
	const billingNotice = useBillingNoticeAction();
	const [parser, setParser] = useState<DocumentParser | undefined>(undefined);

	const [wasVisible, setWasVisible] = useState(visible);
	if (visible !== wasVisible) {
		setWasVisible(visible);
		if (visible) {
			setMode("choose");
			setLink("");
			setError(null);
		}
	}

	const choice = resolveParserChoice(capabilities.data, parser);

	const addLink = async () => {
		const url = normalizeLink(link);
		if (!url) {
			setError(t("mobile.link_invalid"));
			return;
		}
		setError(null);
		try {
			await mutations.importLink.mutateAsync(url);
			onClose();
		} catch (e) {
			setError(errorMessage(e, t("mobile.link_add_failed")), e);
		}
	};

	const pickAndUpload = async () => {
		const picked = await DocumentPicker.getDocumentAsync({ multiple: true, copyToCacheDirectory: true });
		if (picked.canceled || picked.assets.length === 0) return;
		const accepted = capabilities.data ? acceptedExtensions(capabilities.data, choice.effectiveParser) : null;
		const parserLabel = t(`upload_documents.parser_${choice.effectiveParser ?? "local"}`);
		const refused = accepted ? picked.assets.filter((a) => !accepted.has(fileExtension(a.name))) : [];
		const files = picked.assets.filter((a) => !refused.includes(a));
		if (refused.length > 0) {
			const message = refused
				.map((a) => t("mobile.unsupported_file", { parser: parserLabel, name: a.name }))
				.join("\n");
			if (files.length === 0) {
				setError(message);
				return;
			}
			toast(message, "error");
		}
		onClose();
		const ids: number[] = [];
		for (const asset of files) {
			onUploadState({ status: "uploading", name: asset.name });
			try {
				const result = await uploadDocument(api, asset, { workspaceId, parser: choice.effectiveParser });
				ids.push(...result.document_ids, ...(result.duplicate_document_ids ?? []));
			} catch (e) {
				onUploadState({
					status: "failed",
					message: `${t("mobile.upload_failed", { name: asset.name })} ${errorMessage(e, "")}`.trim(),
					billing: billingActionFor(e),
				});
				return;
			}
		}
		try {
			if (ids.length > 0) await mutations.add.mutateAsync(ids.map((id) => ({ kind: "document" as const, id })));
			onUploadState({ status: "idle" });
		} catch (e) {
			onUploadState({ status: "failed", message: t("notebooks.add_sources_failed"), billing: billingActionFor(e) });
		}
	};

	const title =
		mode === "link" ? t("mobile.add_link") : mode === "upload" ? t("mobile.add_file") : t("notebooks.add_sources");

	return (
		<Sheet visible={visible} onClose={onClose} title={title}>
			{mode === "choose" ? (
				<View style={{ marginHorizontal: -space.lg }}>
					<PressableRow onPress={() => setMode("link")} accessibilityRole="button">
						<Icon name="link-outline" />
						<View style={{ flex: 1 }}>
							<AppText variant="subheading">{t("mobile.add_link")}</AppText>
							<AppText variant="small" tone="muted">
								{t("mobile.add_link_hint")}
							</AppText>
						</View>
					</PressableRow>
					<PressableRow onPress={() => setMode("upload")} accessibilityRole="button">
						<Icon name="cloud-upload-outline" />
						<View style={{ flex: 1 }}>
							<AppText variant="subheading">{t("mobile.add_file")}</AppText>
							<AppText variant="small" tone="muted">
								{t("mobile.add_file_hint")}
							</AppText>
						</View>
					</PressableRow>
				</View>
			) : null}

			{mode === "link" ? (
				<>
					<TextField
						value={link}
						onChangeText={setLink}
						autoFocus
						autoCapitalize="none"
						autoCorrect={false}
						keyboardType="url"
						placeholder={t("mobile.link_placeholder")}
						accessibilityLabel={t("mobile.add_link")}
						returnKeyType="done"
						onSubmitEditing={addLink}
					/>
					{error ? <Notice tone="error" message={error.message} {...billingNotice(error.billing)} /> : null}
					<View style={{ flexDirection: "row", gap: space.sm, justifyContent: "flex-end" }}>
						<Button kind="ghost" title={t("notebooks.back")} onPress={() => setMode("choose")} />
						<Button
							title={t("common.add")}
							onPress={addLink}
							busy={mutations.importLink.isPending}
							disabled={!link.trim()}
						/>
					</View>
				</>
			) : null}

			{mode === "upload" ? (
				<>
					<View style={{ gap: space.sm }}>
						<AppText variant="label">{t("upload_documents.parser_label")}</AppText>
						{capabilities.isLoading ? <DelayedSpinner active /> : null}
						{choice.options.length > 0 ? (
							<Segmented
								accessibilityLabel={t("upload_documents.parser_label")}
								value={choice.effectiveParser ?? choice.options[0].parser}
								onChange={(value) => setParser(value)}
								options={choice.options.map((o) => ({
									value: o.parser,
									label: t(`upload_documents.parser_${o.parser}`),
								}))}
							/>
						) : null}
						{choice.options.map((option) =>
							option.parser === choice.effectiveParser || !option.available ? (
								<AppText key={option.parser} variant="small" tone={option.available ? "muted" : "destructive"}>
									{option.available
										? t(`upload_documents.parser_${option.parser}_desc`)
										: `${t(`upload_documents.parser_${option.parser}`)}: ${t(`upload_documents.parser_unavailable_${option.unavailableReason ?? "unknown_service"}`)}`}
									{option.available && option.costMultiplier
										? ` ${t("upload_documents.parser_cost_note", { multiplier: option.costMultiplier })}`
										: ""}
								</AppText>
							) : null
						)}
					</View>
					{error ? <Notice tone="error" message={error.message} {...billingNotice(error.billing)} /> : null}
					<View style={{ flexDirection: "row", gap: space.sm, justifyContent: "flex-end" }}>
						<Button kind="ghost" title={t("notebooks.back")} onPress={() => setMode("choose")} />
						<Button title={t("mobile.choose_file")} icon="document-attach-outline" onPress={pickAndUpload} />
					</View>
				</>
			) : null}
		</Sheet>
	);
}
