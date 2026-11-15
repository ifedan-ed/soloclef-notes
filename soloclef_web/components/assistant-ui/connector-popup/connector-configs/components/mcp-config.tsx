"use client";

import { XCircle } from "lucide-react";
import Link from "next/link";
import { type FC, useMemo, useState } from "react";
import { toast } from "sonner";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { useMcpPolicy } from "@/features/mcp-server-form/hooks/use-mcp-policy";
import { useStartOAuth } from "@/features/mcp-server-form/hooks/use-start-oauth";
import { serverFormFromAnswer } from "@/features/mcp-server-form/model/server-form-state";
import {
	McpServerForm,
	type ServerSubmission,
} from "@/features/mcp-server-form/ui/mcp-server-form";
import { connectorsApiService } from "@/lib/apis/connectors-api.service";
import { settingsPath } from "@/lib/dashboard-paths";
import { mcpAddressRule, mcpServerOrigin } from "@/lib/mcp/server-origin";
import { queryClient } from "@/lib/query-client/client";
import type { ConnectorConfigProps } from "../index";
import { refreshConnectorAfterChange } from "../views/editable-connector";

/**
 * A saved MCP server's edit view: the same form it was added with, filled
 * from the server's masked answer, saved with its own button. Which of its
 * tools ask first is chosen with every other tool's, under Settings, Agent
 * permissions, and the note below says so.
 */
export const MCPConfig: FC<ConnectorConfigProps> = ({ connector }) => {
	const policy = useMcpPolicy();
	const { start, starting } = useStartOAuth();
	const [saving, setSaving] = useState(false);
	// The form starts from the answer the view opened with; the sign-in
	// state below is read from the latest answer, so it follows a save.
	// biome-ignore lint/correctness/useExhaustiveDependencies: the form is filled once per server
	const opened = useMemo(
		() => serverFormFromAnswer(connector.name, connector.config?.server_config),
		[connector.id]
	);
	const latest = serverFormFromAnswer(connector.name, connector.config?.server_config);
	// The rule this row is saved under: its own origin, not the editor's role.
	const addressRule = mcpAddressRule(mcpServerOrigin(connector.config) === "site");

	if (!opened) {
		return (
			<Alert className="border-red-500/50 bg-red-500/10">
				<XCircle className="size-4 text-red-600" />
				<AlertTitle>This server's settings cannot be read</AlertTitle>
				<AlertDescription>Remove it and add it again.</AlertDescription>
			</Alert>
		);
	}

	const signIn = async () => {
		const refusal = await start(connector.id);
		if (refusal) toast.error(refusal);
	};
	const signedIn = Boolean(latest?.oauth?.authorized);

	const submit = async ({ name, serverConfig }: ServerSubmission) => {
		setSaving(true);
		try {
			await connectorsApiService.updateMCPConnector({
				id: connector.id,
				data: { name, server_config: serverConfig },
			});
			await refreshConnectorAfterChange(queryClient, connector.id);
			// An OAuth server not yet signed in has nothing to call with: the
			// save goes straight on to its sign-in.
			if (serverConfig.auth.type === "oauth" && !signedIn) {
				await signIn();
				return;
			}
			toast.success(`${name} saved`);
		} finally {
			setSaving(false);
		}
	};

	return (
		<div className="flex flex-col gap-6">
			<McpServerForm
				initial={opened.state}
				connectorId={connector.id}
				savedOAuth={latest?.oauth ?? null}
				addressRule={addressRule}
				oauthAvailable={Boolean(policy?.oauth_available)}
				submitting={saving || starting}
				submitLabel={
					opened.state.auth === "oauth" && !signedIn ? "Save and sign in" : "Save changes"
				}
				oauthAction={
					<Button
						type="button"
						variant="outline"
						size="sm"
						className="relative h-8"
						disabled={starting}
						onClick={() => void signIn()}
					>
						<span className={starting ? "opacity-0" : ""}>
							{signedIn ? "Sign in again" : "Sign in"}
						</span>
						{starting && <Spinner size="xs" className="absolute" />}
					</Button>
				}
				onSubmit={submit}
			/>
			{connector.id > 0 && (
				<p className="text-xs text-muted-foreground">
					This app&apos;s tools run without asking. To approve one each time, switch on Ask first
					for it under{" "}
					<Link href={settingsPath("agent-permissions")} className="underline underline-offset-2">
						Agent permissions
					</Link>
					.
				</p>
			)}
		</div>
	);
};
