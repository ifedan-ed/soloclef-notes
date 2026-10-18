"use client";

import { AgentSetupTabs } from "@/components/mcp/agent-setup-tabs";
import { SignInSetup } from "@/components/mcp/sign-in-setup";
import { useIsSelfHosted } from "@/components/providers/runtime-config";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { BRAND } from "@/lib/brand";
import { publicBackendBaseUrl } from "@/lib/env-config";
import { cn } from "@/lib/utils";

/** The Model Context Protocol mark, drawn in the current text colour. */
export function McpIcon({ className }: { className?: string }) {
	return (
		<span
			aria-hidden="true"
			className={cn("size-3.5 shrink-0 bg-current", className)}
			style={{
				mask: "url('/connectors/modelcontextprotocol.svg') center / contain no-repeat",
				WebkitMask: "url('/connectors/modelcontextprotocol.svg') center / contain no-repeat",
			}}
		/>
	);
}

/**
 * The MCP setup guide. Claude (web, desktop, mobile) and ChatGPT sign in
 * through single sign-on when the deployment has it on; coding agents (Claude
 * Code, Codex, OpenCode, ...) use an API key: pick one, copy its config.
 * Either way the agent sees only workspaces with API key access on. Opened
 * from "Connect your agent" in the account menu (the user avatar in the
 * rail), so it has no trigger of its own.
 */
export function ConnectAgentDialog({
	open,
	onOpenChange,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
}) {
	// This dialog only ever renders inside the app, where the provider is
	// present — unlike the shared tabs component, which the public
	// /mcp-server page prerenders without one.
	const selfHosted = useIsSelfHosted();
	// This deployment runs its own MCP server behind the same host.
	const remoteUrl = typeof window === "undefined" ? undefined : `${window.location.origin}/mcp`;
	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className="max-h-[85vh] min-w-0 overflow-x-hidden overflow-y-auto sm:max-w-2xl">
				<DialogHeader>
					<DialogTitle>Connect your agent to {BRAND.name}</DialogTitle>
					<DialogDescription>
						Give your agent {BRAND.name}'s scrapers and your knowledge base. It sees only the
						workspaces where API key access is on (workspace settings, General).
					</DialogDescription>
				</DialogHeader>
				{remoteUrl && <SignInSetup serverUrl={remoteUrl} />}
				<section className="min-w-0 space-y-3">
					<h3 className="text-sm font-medium">Coding agents: API key</h3>
					<p className="text-sm text-muted-foreground">
						Create an API key in your settings under API Access, choose your agent, then paste the
						config.
					</p>
					<AgentSetupTabs
						options={{ baseUrl: publicBackendBaseUrl(), remoteUrl }}
						selfHosted={selfHosted}
					/>
				</section>
			</DialogContent>
		</Dialog>
	);
}
