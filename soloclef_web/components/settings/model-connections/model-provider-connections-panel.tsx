"use client";

import { useAtomValue } from "jotai";
import { useState } from "react";
import { toast } from "sonner";
import {
	createModelConnectionMutationAtom,
	previewConnectionModelsMutationAtom,
	testPreviewModelMutationAtom,
} from "@/atoms/model-connections/model-connections-mutation.atoms";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import type { ConnectionRead, ModelSelection } from "@/contracts/types/model-connections.types";
import { ConnectionCard } from "./connection-card";
import {
	CONNECTION_PRESETS,
	type ConnectionDraft,
	type ConnectionPreset,
	CUSTOM_PRESET,
	OWN_PROVIDER,
} from "./connection-presets";
import { previewFailureOf, previewRequestFor, withNamedModel } from "./model-listing";
import { capability, type SelectableModel } from "./model-utils";
import { presetIcon } from "./preset-icon";
import { ProviderConnectDialog } from "./provider-connect-dialog";

function toModelSelection(model: SelectableModel): ModelSelection {
	return {
		model_id: model.model_id,
		display_name: model.display_name,
		source: model.source || "DISCOVERED",
		supports_chat: model.supports_chat,
		max_input_tokens: model.max_input_tokens,
		supports_image_input: model.supports_image_input,
		supports_tools: model.supports_tools,
		supports_image_generation: model.supports_image_generation,
		enabled: model.enabled,
		metadata: "metadata" in model ? (model.metadata ?? {}) : (model.catalog ?? {}),
	};
}

/**
 * The account's own model connections: the presets to start one from, and
 * the ones already made.
 *
 * Every preset makes the same kind of connection, an OpenAI-compatible
 * endpoint the server calls with the person's key (connection-presets.tsx).
 * Any account may make one; there is nothing here for an administrator alone.
 */
export function ModelProviderConnectionsPanel({ connections }: { connections: ConnectionRead[] }) {
	const createConnection = useAtomValue(createModelConnectionMutationAtom);
	const previewModels = useAtomValue(previewConnectionModelsMutationAtom);
	const testPreviewModel = useAtomValue(testPreviewModelMutationAtom);

	const [isAddProviderOpen, setIsAddProviderOpen] = useState(false);
	const [preset, setPreset] = useState<ConnectionPreset>(CUSTOM_PRESET);
	const [connectModels, setConnectModels] = useState<ModelSelection[]>([]);
	// Why the last listing failed, as the server said it; cleared by the next.
	const [previewFailure, setPreviewFailure] = useState<ReturnType<typeof previewFailureOf> | null>(
		null
	);

	function resetConnectState() {
		setConnectModels([]);
		setPreviewFailure(null);
	}

	function handleConnectOpenChange(open: boolean) {
		setIsAddProviderOpen(open);
		if (!open) {
			resetConnectState();
		}
	}

	function mergePreviewModels(fetchedModels: SelectableModel[]) {
		setConnectModels((current) => {
			const currentById = new Map(current.map((model) => [model.model_id, model]));
			return fetchedModels.map((model) => {
				const prior = currentById.get(model.model_id);
				return {
					...toModelSelection(model),
					enabled: prior ? prior.enabled : model.enabled,
				};
			});
		});
	}

	function representativeTestModel(models: ModelSelection[]) {
		const enabledModels = models.filter((model) => model.enabled);
		return enabledModels.find((model) => capability(model, "chat")) ?? enabledModels[0];
	}

	// Connecting tests one chosen model first, through the server, so a
	// connection that cannot answer is never saved; the test's own message is
	// shown by the mutation when it fails.
	function handleCreate(draft: ConnectionDraft) {
		const testModel = representativeTestModel(connectModels);
		if (!testModel) {
			toast.error("Select at least one model before connecting");
			return;
		}

		const request = {
			provider: OWN_PROVIDER,
			base_url: draft.base_url,
			api_key: draft.api_key,
			...(draft.headers ? { headers: draft.headers } : {}),
			// Always the signed-in account's own.
			scope: "user" as const,
			extra: {},
			enabled: true,
			models: connectModels,
		};

		testPreviewModel.mutate(
			{ ...request, model_id: testModel.model_id },
			{
				onSuccess: (result) => {
					if (!result.ok) return;
					createConnection.mutate(request, {
						onSuccess: () => {
							setIsAddProviderOpen(false);
							resetConnectState();
						},
					});
				},
			}
		);
	}

	function refreshConnectModels(draft: ConnectionDraft) {
		const request = previewRequestFor(draft);
		if (!request) return;
		setPreviewFailure(null);
		previewModels.mutate(request, {
			onSuccess: mergePreviewModels,
			onError: (error) => {
				// Models already typed by name are kept: they are the person's
				// own work, and a failed listing says nothing against them.
				setConnectModels((current) => current.filter((model) => model.source === "MANUAL"));
				setPreviewFailure(previewFailureOf(error));
			},
		});
	}

	function openPresetDialog(next: ConnectionPreset) {
		resetConnectState();
		setPreset(next);
		setIsAddProviderOpen(true);
	}

	function toggleConnectModel(model: SelectableModel, enabled: boolean) {
		setConnectModels((current) =>
			current.map((item) => (item.model_id === model.model_id ? { ...item, enabled } : item))
		);
	}

	function bulkToggleConnectModels(models: SelectableModel[], enabled: boolean) {
		const modelIds = new Set(models.map((model) => model.model_id));
		setConnectModels((current) =>
			current.map((item) => (modelIds.has(item.model_id) ? { ...item, enabled } : item))
		);
	}

	return (
		<div className="flex flex-col gap-6">
			<div className="flex flex-col gap-3">
				<div>
					<h3 className="text-base font-semibold">Your own models</h3>
					<p className="text-sm text-muted-foreground">
						Connect any OpenAI-compatible API with your own key. Its models join your model menus,
						marked as yours. A preset only fills in the provider&apos;s address.
					</p>
				</div>
				{/* minmax(0,1fr) and min-w-0: a grid item is as wide as its content by
				    default, so a long subtitle pushed the cards past a phone's edge. */}
				<div className="grid grid-cols-[minmax(0,1fr)] gap-3 md:grid-cols-2">
					{CONNECTION_PRESETS.map((item) => (
						<Button
							key={item.id}
							variant="ghost"
							type="button"
							className="h-auto w-full min-w-0 justify-between gap-3 whitespace-normal rounded-lg border border-border/60 p-4 text-left transition-colors hover:bg-accent hover:text-accent-foreground"
							onClick={() => openPresetDialog(item)}
						>
							<span className="flex min-w-0 items-center gap-3">
								{presetIcon(item, "size-5")}
								<span className="min-w-0">
									<span className="block truncate text-sm font-semibold">{item.name}</span>
									<span className="block truncate text-xs text-muted-foreground">
										{item.subtitle}
									</span>
								</span>
							</span>
							<span className="shrink-0 text-sm font-medium text-muted-foreground">Connect</span>
						</Button>
					))}
				</div>
			</div>

			<ProviderConnectDialog
				open={isAddProviderOpen}
				onOpenChange={handleConnectOpenChange}
				preset={preset}
				isPending={createConnection.isPending || testPreviewModel.isPending}
				onSubmit={handleCreate}
				previewModels={connectModels}
				isPreviewingModels={previewModels.isPending}
				onPreviewModels={refreshConnectModels}
				previewFailure={previewFailure}
				onAddNamedModel={(name) => setConnectModels((current) => withNamedModel(current, name))}
				onTogglePreviewModel={toggleConnectModel}
				onBulkTogglePreviewModels={bulkToggleConnectModels}
			/>

			{connections.length > 0 ? (
				<div className="flex flex-col gap-3">
					<Separator />
					<h3 className="text-base font-semibold">Connected</h3>
					<div className="flex flex-col gap-3">
						{connections.map((connection) => (
							<ConnectionCard key={connection.id} connection={connection} />
						))}
					</div>
				</div>
			) : null}
		</div>
	);
}
