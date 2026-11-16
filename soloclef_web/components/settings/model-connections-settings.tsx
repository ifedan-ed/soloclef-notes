"use client";

import { useAtom } from "jotai";
import { useTranslations } from "next-intl";
import { modelConnectionsAtom } from "@/atoms/model-connections/model-connections-query.atoms";
import { Separator } from "@/components/ui/separator";
import { ChatModelSetting } from "@/features/chat-model/ui/chat-model-setting";
import { ModelJobSetting, ModelRoleSetting } from "@/features/model-choice/ui/model-choice-setting";
import { ModelProviderConnectionsPanel } from "./model-connections/model-provider-connections-panel";

/**
 * The model settings page: which model does each of the account's jobs, and
 * the account's own provider connections.
 *
 * Every job's model is chosen from one menu, the server's proxy models and
 * the models of the account's own connections, so the four pickers are one
 * control over four settings (features/chat-model, features/model-choice).
 *
 * Every account may connect an OpenAI-compatible endpoint with its own key;
 * the server calls it directly and its models join the account's menus. That
 * is the only kind of connection there is (model-connections/connection-presets.tsx).
 */
export function ModelConnectionsSettings() {
	const [{ data: connections = [] }] = useAtom(modelConnectionsAtom);
	const quick = useTranslations("quick_model");
	const studio = useTranslations("studio_model");

	return (
		<div className="flex flex-col gap-6">
			<div className="flex flex-col gap-4">
				<div>
					<h3 className="text-base font-semibold">Model roles</h3>
					<p className="text-sm text-muted-foreground">
						Pick which of the available models powers chat, quick tasks, Studio, and vision.
					</p>
				</div>
				<div className="flex w-full max-w-2xl flex-col gap-4">
					<ChatModelSetting />
					<ModelJobSetting job="quick" label={quick("title")} description={quick("description")} />
					<ModelJobSetting
						job="studio"
						label={studio("title")}
						description={studio("description")}
					/>
					<ModelRoleSetting
						kind="vision"
						label="Vision model"
						description="Used to understand images in uploads and documents."
					/>
				</div>
			</div>

			<Separator />
			<ModelProviderConnectionsPanel connections={connections} />
		</div>
	);
}
