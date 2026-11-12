import { useAtomValue } from "jotai";
import { Check, Eye, EyeOff, Settings } from "lucide-react";
import { useId, useMemo, useState } from "react";
import {
	bulkUpdateModelsMutationAtom,
	discoverConnectionModelsMutationAtom,
	testConnectionUpdateMutationAtom,
	updateModelConnectionMutationAtom,
	updateModelMutationAtom,
} from "@/atoms/model-connections/model-connections-mutation.atoms";
import { modelProvidersAtom } from "@/atoms/model-connections/model-connections-query.atoms";
import { Button } from "@/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
	DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";
import { Spinner } from "@/components/ui/spinner";
import type { ConnectionRead } from "@/contracts/types/model-connections.types";
import { HeadersField } from "./connect-fields";
import { type HeaderRow, headersFromRows, savedHeadersIntent } from "./connection-headers";
import { hasModelCatalogue } from "./model-listing";
import { capability, type SelectableModel } from "./model-utils";
import { ModelsSelectionPanel } from "./models-selection-panel";
import { providerIcon } from "./provider-metadata";
import { connectionUpdateFor, hasConnectionChanges, savedKeyIntent } from "./saved-key-draft";

interface ConnectionSettingsDialogProps {
	connection: ConnectionRead;
	providerLabel: string;
}

function enabledModelIds(models: SelectableModel[]) {
	return new Set(
		models
			.filter((model) => typeof model.id === "number" && model.enabled)
			.map((model) => Number(model.id))
	);
}

/**
 * Settings for a saved connection.
 *
 * The saved API key is never sent to the browser, so this dialog never has it.
 * The key field therefore always opens empty, and what it shows about the
 * saved key is words beside the field ("A key is saved"), never a mask of
 * dots inside it: a mask is text in an input, and text in an input is what
 * gets sent back as the key. What the person wants done with the key is one of
 * three explicit things (./saved-key-draft): keep it, replace it with what
 * they typed, or remove it.
 *
 * A change is tested before it is saved, by the server, which holds the key:
 * the test request leaves `api_key` out unless a new one was typed.
 */
export function ConnectionSettingsDialog({
	connection,
	providerLabel,
}: ConnectionSettingsDialogProps) {
	const discoverModels = useAtomValue(discoverConnectionModelsMutationAtom);
	const { data: providers = [] } = useAtomValue(modelProvidersAtom);
	// Only a provider with a list (the model service's catalogue, or an
	// OpenAI-compatible endpoint's own GET /models) has anything to refresh;
	// for a server the model service reaches by name the button would only
	// ever fail.
	const canRefreshModels = hasModelCatalogue(
		providers.find((item) => item.provider === connection.provider)
	);
	const testConnectionUpdate = useAtomValue(testConnectionUpdateMutationAtom);
	const updateConnection = useAtomValue(updateModelConnectionMutationAtom);
	const updateModel = useAtomValue(updateModelMutationAtom);
	const bulkUpdateModels = useAtomValue(bulkUpdateModelsMutationAtom);

	const [isOpen, setIsOpen] = useState(false);
	const [baseUrlDraft, setBaseUrlDraft] = useState(connection.base_url ?? "");
	// Only ever what was typed here. It is never filled from the connection.
	const [apiKeyDraft, setApiKeyDraft] = useState("");
	const [removeSavedKey, setRemoveSavedKey] = useState(false);
	// Only an OpenAI-compatible endpoint takes extra headers. Their saved
	// values are never in the browser: the rows are only ever new headers.
	const takesHeaders = connection.provider === "openai_compatible";
	const [headerRows, setHeaderRows] = useState<HeaderRow[]>([]);
	const [removeSavedHeaders, setRemoveSavedHeaders] = useState(false);
	const [showApiKey, setShowApiKey] = useState(false);
	const apiKeyFieldId = useId();
	const apiKeyStatusId = useId();
	const [isSavingConnectionSettings, setIsSavingConnectionSettings] = useState(false);
	const [draftEnabledModelIds, setDraftEnabledModelIds] = useState(() =>
		enabledModelIds(connection.models)
	);
	const [draftContextLimits, setDraftContextLimits] = useState<Record<number, number | null>>(() =>
		Object.fromEntries(
			connection.models
				.filter((model) => typeof model.id === "number")
				.map((model) => [Number(model.id), model.max_input_tokens ?? null])
		)
	);

	const keyIntent = savedKeyIntent(apiKeyDraft, removeSavedKey);
	const headersRead = headersFromRows(headerRows);
	const headersIntent = savedHeadersIntent(
		headersRead.problem === null ? headersRead.headers : {},
		removeSavedHeaders
	);
	const connectionChanged = hasConnectionChanges(
		connection.base_url,
		baseUrlDraft,
		keyIntent,
		headersIntent
	);
	const draftModels = useMemo(
		() =>
			connection.models.map((model) =>
				typeof model.id === "number"
					? {
							...model,
							enabled: draftEnabledModelIds.has(model.id),
							max_input_tokens: draftContextLimits[model.id] ?? null,
						}
					: model
			),
		[connection.models, draftContextLimits, draftEnabledModelIds]
	);
	const hasModelChanges = connection.models.some(
		(model) => typeof model.id === "number" && draftEnabledModelIds.has(model.id) !== model.enabled
	);
	const hasContextChanges = connection.models.some(
		(model) =>
			typeof model.id === "number" &&
			(draftContextLimits[model.id] ?? null) !== (model.max_input_tokens ?? null)
	);
	// Rows that do not read as headers block the save and are said under
	// them; they are never saved as something else.
	const canUpdate =
		(connectionChanged || hasModelChanges || hasContextChanges) && headersRead.problem === null;

	function handleOpenChange(open: boolean) {
		setIsOpen(open);
		if (open) {
			setBaseUrlDraft(connection.base_url ?? "");
			setApiKeyDraft("");
			setRemoveSavedKey(false);
			setShowApiKey(false);
			setHeaderRows([]);
			setRemoveSavedHeaders(false);
			setIsSavingConnectionSettings(false);
			setDraftEnabledModelIds(enabledModelIds(connection.models));
			setDraftContextLimits(
				Object.fromEntries(
					connection.models
						.filter((model) => typeof model.id === "number")
						.map((model) => [Number(model.id), model.max_input_tokens ?? null])
				)
			);
		}
	}

	async function saveContextChanges() {
		const changedModels = connection.models.filter(
			(model) =>
				typeof model.id === "number" &&
				(draftContextLimits[model.id] ?? null) !== (model.max_input_tokens ?? null)
		);
		await Promise.all(
			changedModels.map((model) =>
				updateModel.mutateAsync({
					id: Number(model.id),
					data: { max_input_tokens: draftContextLimits[Number(model.id)] ?? null },
				})
			)
		);
	}

	async function saveModelChanges() {
		const toEnable = connection.models
			.filter((model) => typeof model.id === "number" && draftEnabledModelIds.has(model.id))
			.filter((model) => !model.enabled)
			.map((model) => Number(model.id));
		const toDisable = connection.models
			.filter((model) => typeof model.id === "number" && !draftEnabledModelIds.has(model.id))
			.filter((model) => model.enabled)
			.map((model) => Number(model.id));

		if (toEnable.length > 0) {
			await bulkUpdateModels.mutateAsync({
				connectionId: connection.id,
				data: { model_ids: toEnable, enabled: true },
			});
		}
		if (toDisable.length > 0) {
			await bulkUpdateModels.mutateAsync({
				connectionId: connection.id,
				data: { model_ids: toDisable, enabled: false },
			});
		}
	}

	async function saveConnectionSettings() {
		if (isSavingConnectionSettings) return;

		// `api_key` is in here only if a key was typed or removal was asked
		// for. Left out, the server keeps -- and tests with -- the saved key.
		const data = connectionUpdateFor(baseUrlDraft, keyIntent, headersIntent);

		const enabledModels = draftModels.filter((model) => model.enabled);
		const testModel = enabledModels.find((model) => capability(model, "chat")) ?? enabledModels[0];
		setIsSavingConnectionSettings(true);
		try {
			if (connectionChanged) {
				if (testModel) {
					// The same fields the update below will send, so the test
					// is of exactly what is about to be saved.
					const result = await testConnectionUpdate.mutateAsync({
						id: connection.id,
						data: { ...data, model_id: testModel.model_id },
					});
					if (!result.ok) return;
				}
				await updateConnection.mutateAsync({ id: connection.id, data });
				setApiKeyDraft("");
				setRemoveSavedKey(false);
				setShowApiKey(false);
				setHeaderRows([]);
				setRemoveSavedHeaders(false);
			}

			if (hasModelChanges) {
				await saveModelChanges();
			}
			if (hasContextChanges) {
				await saveContextChanges();
			}
		} finally {
			setIsSavingConnectionSettings(false);
		}
	}

	function handleContextLimitChange(model: SelectableModel, value: number | null) {
		if (typeof model.id !== "number") return;
		const modelId = model.id;
		setDraftContextLimits((current) => ({ ...current, [modelId]: value }));
	}

	function handleToggleModel(model: SelectableModel, enabled: boolean) {
		if (typeof model.id !== "number") return;
		const modelId = model.id;
		setDraftEnabledModelIds((current) => {
			const next = new Set(current);
			if (enabled) {
				next.add(modelId);
			} else {
				next.delete(modelId);
			}
			return next;
		});
	}

	function handleBulkToggle(models: SelectableModel[], enabled: boolean) {
		const modelIds = models
			.map((model) => model.id)
			.filter((id): id is number => typeof id === "number");
		if (modelIds.length === 0) return;
		setDraftEnabledModelIds((current) => {
			const next = new Set(current);
			for (const id of modelIds) {
				if (enabled) {
					next.add(id);
				} else {
					next.delete(id);
				}
			}
			return next;
		});
	}

	return (
		<Dialog open={isOpen} onOpenChange={handleOpenChange}>
			<DialogTrigger asChild>
				<Button
					variant="ghost"
					size="icon"
					className="text-muted-foreground hover:text-accent-foreground"
					aria-label={`Configure ${providerLabel}`}
				>
					<Settings className="h-4 w-4" />
				</Button>
			</DialogTrigger>
			<DialogContent className="flex max-h-[90vh] max-w-3xl flex-col overflow-hidden bg-popover p-0 text-popover-foreground">
				<DialogHeader className="shrink-0 border-b px-6 py-5">
					<div className="flex items-center gap-3">
						{providerIcon(connection.provider, "size-5")}
						<div>
							<DialogTitle>
								Configure <span className="italic">{providerLabel}</span>
							</DialogTitle>
							<DialogDescription>
								Manage credentials and choose which models are available from this provider.
							</DialogDescription>
						</div>
					</div>
				</DialogHeader>

				<div className="min-h-0 flex-1 overflow-y-auto px-6 py-5">
					<div className="space-y-6">
						<div className="space-y-2">
							<Label>API Base URL</Label>
							<Input
								value={baseUrlDraft}
								onChange={(event) => setBaseUrlDraft(event.target.value)}
								placeholder="https://api.example.com/v1"
							/>
							<p className="text-xs text-muted-foreground">
								{takesHeaders
									? "The endpoint's public address, used as-is."
									: "Leave empty to use the provider default endpoint."}
							</p>
						</div>

						<div className="space-y-2">
							<div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1">
								<Label htmlFor={apiKeyFieldId}>API Key</Label>
								{connection.has_api_key && keyIntent.kind !== "remove" ? (
									<span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
										<Check
											className="size-3.5 text-emerald-600 dark:text-emerald-500"
											aria-hidden
										/>
										A key is saved
									</span>
								) : null}
							</div>
							<div className="relative">
								<Input
									id={apiKeyFieldId}
									value={apiKeyDraft}
									onChange={(event) => {
										setApiKeyDraft(event.target.value);
										// Typing a key replaces a request to remove the saved one.
										setRemoveSavedKey(false);
									}}
									placeholder={
										connection.has_api_key && keyIntent.kind !== "remove"
											? "Enter a new key to replace it"
											: "Paste an API key"
									}
									type={showApiKey ? "text" : "password"}
									// An empty field means "keep the saved key", so the browser
									// and password managers must not fill it in by themselves.
									autoComplete="new-password"
									data-1p-ignore
									data-lpignore="true"
									aria-describedby={apiKeyStatusId}
									className="pr-11"
								/>
								<Button
									type="button"
									variant="ghost"
									size="icon"
									className="absolute top-1/2 right-1 size-8 -translate-y-1/2 text-muted-foreground"
									onClick={() => setShowApiKey((current) => !current)}
									disabled={!apiKeyDraft}
									aria-label={showApiKey ? "Hide API key" : "Show API key"}
								>
									{showApiKey ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
								</Button>
							</div>
							<div
								id={apiKeyStatusId}
								className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 text-xs text-muted-foreground"
							>
								{!connection.has_api_key ? (
									<p>No key is saved for this provider.</p>
								) : keyIntent.kind === "replace" ? (
									<p>This key will replace the saved one when you update.</p>
								) : keyIntent.kind === "remove" ? (
									<>
										<p>The saved key will be removed when you update.</p>
										<Button
											type="button"
											variant="link"
											size="sm"
											className="h-auto p-0 text-xs"
											onClick={() => setRemoveSavedKey(false)}
										>
											Keep the saved key
										</Button>
									</>
								) : (
									<>
										<p>The saved key is never shown. Leave this empty to keep it.</p>
										<Button
											type="button"
											variant="link"
											size="sm"
											className="h-auto p-0 text-xs text-muted-foreground hover:text-destructive"
											onClick={() => setRemoveSavedKey(true)}
										>
											Remove saved key
										</Button>
									</>
								)}
							</div>
						</div>

						{takesHeaders ? (
							<div className="space-y-2">
								<HeadersField
									rows={headerRows}
									onChange={(rows) => {
										setHeaderRows(rows);
										// Typing headers replaces a request to remove the saved ones.
										setRemoveSavedHeaders(false);
									}}
									problem={headersRead.problem}
									hint={
										headersIntent.kind === "replace" && connection.header_names.length > 0
											? "These headers will replace the saved ones when you update."
											: undefined
									}
								/>
								{connection.header_names.length > 0 && headersIntent.kind !== "replace" ? (
									<div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 text-xs text-muted-foreground">
										<p className="min-w-0 break-words">
											{headersIntent.kind === "remove"
												? "The saved headers will be removed when you update."
												: `Saved: ${connection.header_names.join(", ")}. Their values are never shown. Leave these rows empty to keep them.`}
										</p>
										<Button
											type="button"
											variant="link"
											size="sm"
											className="h-auto p-0 text-xs text-muted-foreground hover:text-destructive"
											onClick={() => setRemoveSavedHeaders((current) => !current)}
										>
											{headersIntent.kind === "remove"
												? "Keep the saved headers"
												: "Remove saved headers"}
										</Button>
									</div>
								) : null}
							</div>
						) : null}

						<Separator className="bg-muted-foreground/20" />

						<ModelsSelectionPanel
							models={draftModels}
							description="Choose available models. Max input tokens caps the prompt we send in one request; leave it blank to let the provider decide."
							isRefreshing={discoverModels.isPending}
							isUpdatingModel={isSavingConnectionSettings}
							isBulkUpdating={isSavingConnectionSettings || bulkUpdateModels.isPending}
							refreshLabel={`Refresh ${providerLabel} models`}
							onRefresh={canRefreshModels ? () => discoverModels.mutate(connection.id) : undefined}
							onToggleModel={handleToggleModel}
							onBulkToggle={handleBulkToggle}
							onMaxInputTokensChange={handleContextLimitChange}
						/>
					</div>
				</div>

				<DialogFooter className="shrink-0 border-t bg-popover px-6 py-4">
					<Button
						onClick={saveConnectionSettings}
						disabled={isSavingConnectionSettings || !canUpdate}
						className="relative min-w-[96px]"
					>
						<span className={isSavingConnectionSettings ? "opacity-0" : ""}>Update</span>
						{isSavingConnectionSettings ? <Spinner size="sm" className="absolute" /> : null}
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}
