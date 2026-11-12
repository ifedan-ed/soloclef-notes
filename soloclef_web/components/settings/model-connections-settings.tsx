"use client";

import { useAtom } from "jotai";
import { useTranslations } from "next-intl";
import { modelConnectionsAtom } from "@/atoms/model-connections/model-connections-query.atoms";
import { Separator } from "@/components/ui/separator";
import { ChatModelSetting } from "@/features/chat-model/ui/chat-model-setting";
import { ModelJobSetting, ModelRoleSetting } from "@/features/model-choice/ui/model-choice-setting";
import { useSiteAdmin } from "@/hooks/use-site-admin";
import { ModelProviderConnectionsPanel } from "./model-connections/model-provider-connections-panel";

/**
 * The model settings page: which model does each of the account's jobs, and
 * the account's own provider connections.
 *
 * Every job's model is chosen from one menu, the server's proxy models and
 * the models of the account's own connections, so the five pickers are one
 * control over five settings (features/chat-model, features/model-choice).
 *
 * Every account may connect an OpenAI-compatible endpoint with its own key;
 * the server calls it directly and its models join the account's menus. The
 * other providers are an administrator's to connect, so only an
 * administrator is offered them (the panel filters by `any_account`).
 */
export function ModelConnectionsSettings() {
	const [{ data: connections = [] }] = useAtom(modelConnectionsAtom);
	const isAdmin = useSiteAdmin().isAdmin === true;
	const quick = useTranslations("quick_model");
	const studio = useTranslations("studio_model");

	return (
		<div className="flex flex-col gap-6">
			<div className="flex flex-col gap-4">
				<div>
					<h3 className="text-base font-semibold">Model Roles</h3>
					<p className="text-sm text-muted-foreground">
						Pick which of the available models powers chat, quick tasks, Studio, vision, and image
						generation.
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
					<ModelRoleSetting
						kind="image"
						label="Image generation model"
						description="Used when generating images in chat."
					/>
				</div>
			</div>

			<Separator />
			<ModelProviderConnectionsPanel
				connections={connections}
				isAdmin={isAdmin}
				addProviderTitle={isAdmin ? undefined : "Your own models"}
				addProviderDescription={
					isAdmin
						? undefined
						: "Connect any OpenAI-compatible endpoint with your own key. Its models join your model menus, marked as yours."
				}
			/>
		</div>
	);
}
