"use client";

import { Info } from "lucide-react";
import { type FC, useState } from "react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { EnumConnectorName } from "@/contracts/enums/connector";
import { useMcpPolicy } from "@/features/mcp-server-form/hooks/use-mcp-policy";
import { useStartOAuth } from "@/features/mcp-server-form/hooks/use-start-oauth";
import { emptyServerForm } from "@/features/mcp-server-form/model/server-form-state";
import {
	McpServerForm,
	type ServerSubmission,
} from "@/features/mcp-server-form/ui/mcp-server-form";
import { connectorsApiService } from "@/lib/apis/connectors-api.service";
import { mcpAddressRule } from "@/lib/mcp/server-origin";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { queryClient } from "@/lib/query-client/client";
import type { ConnectFormProps } from "..";

/**
 * "Add MCP server": the shared server form, submitted by the add view's
 * footer button (form id "mcp-connect-form").
 *
 * A server signed in to with a key is created through the dialog's own
 * create flow, as before. One signed in to with OAuth is created here and the
 * person is sent straight to its sign-in: that flow leaves the page, and the
 * sign-in needs the new server's id, which the dialog's flow does not hand
 * back.
 */
export const MCPConnectForm: FC<ConnectFormProps> = ({ onSubmit, isSubmitting }) => {
	const policy = useMcpPolicy();
	const { start, starting } = useStartOAuth();
	const [creating, setCreating] = useState(false);
	const canAdd = policy?.can_add !== false;

	const submit = async ({ name, serverConfig }: ServerSubmission) => {
		if (serverConfig.auth.type !== "oauth") {
			await onSubmit({
				name,
				connector_type: EnumConnectorName.MCP_CONNECTOR,
				config: { server_config: serverConfig },
				is_indexable: false,
				is_active: true,
				last_indexed_at: null,
				periodic_indexing_enabled: false,
				indexing_frequency_minutes: null,
				next_scheduled_at: null,
			});
			return;
		}
		setCreating(true);
		try {
			const created = await connectorsApiService.createMCPConnector({
				data: { name, server_config: serverConfig },
			});
			void queryClient.invalidateQueries({ queryKey: cacheKeys.connectors.all() });
			const refusal = await start(created.id);
			if (refusal) {
				throw new Error(
					`The server is saved, but its sign-in could not start: ${refusal} Sign in from the server's settings.`
				);
			}
		} finally {
			setCreating(false);
		}
	};

	return (
		<div className="flex flex-col gap-6 pb-6">
			<Alert>
				<Info />
				<AlertDescription>
					Connect a remote Model Context Protocol server. Its tools become available in your chats;
					each server is its own connector.
				</AlertDescription>
			</Alert>
			{!canAdd && (
				<Alert className="border-amber-500/50 bg-amber-500/10">
					<Info />
					<AlertDescription>
						Adding MCP servers is switched off for your account. Servers already connected still
						work in chats.
					</AlertDescription>
				</Alert>
			)}
			<McpServerForm
				formId="mcp-connect-form"
				initial={emptyServerForm()}
				addressRule={mcpAddressRule(Boolean(policy?.allow_list_applies))}
				oauthAvailable={Boolean(policy?.oauth_available)}
				disabled={!canAdd}
				submitting={isSubmitting || creating || starting}
				onSubmit={submit}
			/>
		</div>
	);
};
