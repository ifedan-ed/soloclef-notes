import { useCallback, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { Separator } from "@/components/ui/separator";
import { Spinner } from "@/components/ui/spinner";
import type { ModelProviderRead } from "@/contracts/types/model-connections.types";
import { AzureConnectForm } from "./azure-connect-form";
import { BedrockConnectForm } from "./bedrock-connect-form";
import { ConnectFormFooter } from "./connect-fields";
import { DefaultConnectForm } from "./default-connect-form";
import { hasModelCatalogue, listsFromEndpoint, previewRequestFor } from "./model-listing";
import type { SelectableModel } from "./model-utils";
import { ModelsSelectionPanel } from "./models-selection-panel";
import { NamedModelField } from "./named-model-field";
import {
	type ConnectionDraft,
	type ProviderConnectFormProps,
	providerDefaultBaseUrl,
	providerDisplay,
	providerIcon,
} from "./provider-metadata";
import { VertexConnectForm } from "./vertex-connect-form";

interface ProviderConnectDialogProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	provider: string;
	selectedProvider?: ModelProviderRead;
	isPending: boolean;
	onSubmit: (draft: ConnectionDraft) => void;
	previewModels?: SelectableModel[];
	isPreviewingModels?: boolean;
	/** List the provider's models; an endpoint's list is asked with the draft's address and key. */
	onPreviewModels?: (draft: ConnectionDraft) => void;
	/**
	 * Why the last listing failed, in the server's words, or null. typeNames
	 * says there is no list to be had, and models are typed by name instead.
	 */
	previewFailure?: { message: string; typeNames: boolean } | null;
	/** Add a model by name, for a provider the model service has no list for. */
	onAddNamedModel?: (name: string) => void;
	onTogglePreviewModel?: (model: SelectableModel, enabled: boolean) => void;
	onBulkTogglePreviewModels?: (models: SelectableModel[], enabled: boolean) => void;
}

/**
 * Shared dialog shell for the "Add Provider" flow. It owns the header and routes
 * to the provider-specific connect form. Forms remount on open (Radix unmounts
 * closed content), so each gets fresh, prefilled state.
 */
export function ProviderConnectDialog({
	open,
	onOpenChange,
	provider,
	selectedProvider,
	isPending,
	onSubmit,
	previewModels = [],
	isPreviewingModels = false,
	onPreviewModels,
	previewFailure = null,
	onAddNamedModel,
	onTogglePreviewModel,
	onBulkTogglePreviewModels,
}: ProviderConnectDialogProps) {
	const meta = providerDisplay(provider);
	const isAzure = provider === "azure";
	const isBedrock = provider === "bedrock";
	const isVertex = provider === "vertex_ai";
	const titleRef = useRef<HTMLHeadingElement>(null);
	const [currentDraft, setCurrentDraft] = useState<ConnectionDraft>({
		base_url: null,
		api_key: null,
		extra: {},
	});
	const [canSubmit, setCanSubmit] = useState(false);

	const handleDraftChange = useCallback((draft: ConnectionDraft, nextCanSubmit: boolean) => {
		setCurrentDraft(draft);
		setCanSubmit(nextCanSubmit);
	}, []);

	const formProps: ProviderConnectFormProps = {
		provider,
		defaultBaseUrl: providerDefaultBaseUrl(provider, selectedProvider?.default_base_url),
		baseUrlRequired: Boolean(selectedProvider?.base_url_required),
		onDraftChange: handleDraftChange,
	};

	const modelDescription = (() => {
		if (isAzure) {
			return "Select the models to enable for Azure OpenAI";
		}
		if (isBedrock) {
			return "Select the models to enable for Amazon Bedrock";
		}
		if (isVertex) {
			return "Select the models to enable for Gemini";
		}
		if (!hasModelCatalogue(selectedProvider) || previewFailure?.typeNames) {
			return "Add the models to use by name. Connecting tests one of them.";
		}
		if (listsFromEndpoint(selectedProvider)) {
			return "Enter the address and key above, then list the models the endpoint offers.";
		}
		return "Select the models to enable for this provider";
	})();

	// Azure names its one deployment in the form above. Every other provider
	// either has a list (the model service's catalogue, or an endpoint's own
	// GET /models), which is refreshed, or has none, and its models are typed
	// by name. An endpoint that turns out to list nothing (409) is typed by
	// name too, and the server's sentence above the field says why.
	const hasCatalogue = hasModelCatalogue(selectedProvider);
	const namesItsModels = !isAzure && (!hasCatalogue || Boolean(previewFailure?.typeNames));
	// An endpoint is asked with what is typed, so there is nothing to ask
	// until an address and a key are there.
	const canList = previewRequestFor(selectedProvider, currentDraft) !== null;
	// An endpoint's list is the first thing to do once the fields are filled
	// in, so it gets a button with words; a catalogue's is already there and
	// keeps the small refresh icon.
	const endpoint = listsFromEndpoint(selectedProvider);
	const hasEnabledModel =
		previewModels.some((model) => model.enabled) || Boolean(currentDraft.seedModelId);
	const canConnect = canSubmit && hasEnabledModel;

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent
				className="flex h-[85vh] max-h-[760px] min-h-[640px] max-w-2xl flex-col overflow-hidden bg-popover p-0 text-popover-foreground"
				onOpenAutoFocus={(event) => {
					event.preventDefault();
					titleRef.current?.focus();
				}}
			>
				<DialogHeader className="shrink-0 border-b px-6 py-5">
					<div className="flex items-center gap-3">
						{providerIcon(provider, "size-5")}
						<div>
							<DialogTitle ref={titleRef} tabIndex={-1}>
								Connect {meta.name}
							</DialogTitle>
							<DialogDescription>{meta.subtitle}</DialogDescription>
						</div>
					</div>
				</DialogHeader>
				<div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-6 py-5">
					{provider === "azure" ? (
						<AzureConnectForm {...formProps} />
					) : provider === "bedrock" ? (
						<BedrockConnectForm {...formProps} />
					) : provider === "vertex_ai" ? (
						<VertexConnectForm {...formProps} />
					) : (
						<DefaultConnectForm {...formProps} />
					)}

					{!isAzure ? (
						<>
							<Separator className="bg-muted-foreground/20" />

							{endpoint ? (
								<div>
									<Button
										type="button"
										variant="secondary"
										onClick={() => onPreviewModels?.(currentDraft)}
										disabled={!canList || isPreviewingModels}
										className="relative min-w-[120px]"
									>
										<span className={isPreviewingModels ? "opacity-0" : ""}>List models</span>
										{isPreviewingModels ? <Spinner size="sm" className="absolute" /> : null}
									</Button>
								</div>
							) : null}

							{previewFailure ? (
								<p
									role="alert"
									className={
										previewFailure.typeNames
											? "text-sm text-muted-foreground"
											: "text-sm text-destructive"
									}
								>
									{previewFailure.message}
								</p>
							) : null}

							{namesItsModels ? (
								<NamedModelField onAdd={(name) => onAddNamedModel?.(name)} />
							) : null}

							<ModelsSelectionPanel
								models={previewModels}
								description={modelDescription}
								emptyMessage={namesItsModels ? "No models added yet." : undefined}
								isRefreshing={isPreviewingModels}
								isRefreshDisabled={!canList}
								refreshLabel={`Refresh ${meta.name} models`}
								onRefresh={
									hasCatalogue && !endpoint ? () => onPreviewModels?.(currentDraft) : undefined
								}
								onToggleModel={onTogglePreviewModel}
								onBulkToggle={onBulkTogglePreviewModels}
							/>
						</>
					) : null}
				</div>
				<ConnectFormFooter
					onCancel={() => onOpenChange(false)}
					onSubmit={() => onSubmit(currentDraft)}
					canSubmit={canConnect}
					isPending={isPending}
				/>
			</DialogContent>
		</Dialog>
	);
}
