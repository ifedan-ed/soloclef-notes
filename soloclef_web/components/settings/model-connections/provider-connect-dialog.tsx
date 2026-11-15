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
import { ConnectFormFooter } from "./connect-fields";
import type { ConnectionDraft, ConnectionPreset } from "./connection-presets";
import { EndpointConnectForm } from "./endpoint-connect-form";
import { previewRequestFor } from "./model-listing";
import type { SelectableModel } from "./model-utils";
import { ModelsSelectionPanel } from "./models-selection-panel";
import { NamedModelField } from "./named-model-field";
import { presetIcon } from "./preset-icon";

interface ProviderConnectDialogProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	preset: ConnectionPreset;
	isPending: boolean;
	onSubmit: (draft: ConnectionDraft) => void;
	previewModels?: SelectableModel[];
	isPreviewingModels?: boolean;
	/** List the endpoint's models with the address, key and headers typed. */
	onPreviewModels?: (draft: ConnectionDraft) => void;
	/**
	 * Why the last listing failed, in the server's words, or null. typeNames
	 * says the endpoint keeps no list, and models are typed by name instead.
	 */
	previewFailure?: { message: string; typeNames: boolean } | null;
	/** Add a model by name, for an endpoint that keeps no list. */
	onAddNamedModel?: (name: string) => void;
	onTogglePreviewModel?: (model: SelectableModel, enabled: boolean) => void;
	onBulkTogglePreviewModels?: (models: SelectableModel[], enabled: boolean) => void;
}

/**
 * Connecting an OpenAI-compatible endpoint: the address, key and headers,
 * then the models to use from it. Every preset opens this same dialog with
 * its base URL already typed (connection-presets.tsx). The form remounts on
 * open (Radix unmounts closed content), so each opening starts fresh.
 */
export function ProviderConnectDialog({
	open,
	onOpenChange,
	preset,
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
	const titleRef = useRef<HTMLHeadingElement>(null);
	const [currentDraft, setCurrentDraft] = useState<ConnectionDraft>({
		base_url: null,
		api_key: null,
	});
	const [canSubmit, setCanSubmit] = useState(false);

	const handleDraftChange = useCallback((draft: ConnectionDraft, nextCanSubmit: boolean) => {
		setCurrentDraft(draft);
		setCanSubmit(nextCanSubmit);
	}, []);

	// The endpoint is asked with what is typed, so there is nothing to ask
	// until an address and a key are there.
	const canList = previewRequestFor(currentDraft) !== null;
	// Names are typed only once the endpoint has said it keeps no list: until
	// then its own list is the way to pick, and a name spelled by hand is how
	// a model ends up misnamed.
	const namesItsModels = Boolean(previewFailure?.typeNames);
	const canConnect = canSubmit && previewModels.some((model) => model.enabled);

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent
				className="flex h-[85vh] max-h-[760px] max-w-2xl flex-col overflow-hidden bg-popover p-0 text-popover-foreground"
				onOpenAutoFocus={(event) => {
					event.preventDefault();
					titleRef.current?.focus();
				}}
			>
				<DialogHeader className="shrink-0 border-b px-6 py-5">
					<div className="flex items-center gap-3">
						{presetIcon(preset, "size-5")}
						<div>
							<DialogTitle ref={titleRef} tabIndex={-1}>
								Connect {preset.name}
							</DialogTitle>
							<DialogDescription>{preset.subtitle}</DialogDescription>
						</div>
					</div>
				</DialogHeader>
				<div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-6 py-5">
					<EndpointConnectForm preset={preset} onDraftChange={handleDraftChange} />

					<Separator className="bg-muted-foreground/20" />

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

					{namesItsModels ? <NamedModelField onAdd={(name) => onAddNamedModel?.(name)} /> : null}

					<ModelsSelectionPanel
						models={previewModels}
						description={
							namesItsModels
								? "Add the models to use by name. Connecting tests one of them."
								: "Enter the address and key above, then list the models the endpoint offers."
						}
						emptyMessage={namesItsModels ? "No models added yet." : undefined}
						isRefreshing={isPreviewingModels}
						onToggleModel={onTogglePreviewModel}
						onBulkToggle={onBulkTogglePreviewModels}
					/>
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
