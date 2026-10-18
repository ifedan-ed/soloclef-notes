"use client";

import { useQuery } from "@tanstack/react-query";
import { Check, Copy, Eye, EyeOff } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { agentSignInApiService } from "@/lib/apis/agent-sign-in-api.service";
import { SIGN_IN_CLIENTS } from "@/lib/mcp/sign-in-clients";

function CopyableField({
	label,
	value,
	secret,
}: {
	label: string;
	value: string;
	secret?: boolean;
}) {
	const [copied, setCopied] = useState(false);
	const [shown, setShown] = useState(!secret);

	const copy = async () => {
		try {
			await navigator.clipboard.writeText(value);
			setCopied(true);
			setTimeout(() => setCopied(false), 2000);
		} catch {
			// Clipboard unavailable (permissions/insecure context); nothing to recover.
		}
	};

	return (
		<div className="flex min-w-0 items-center gap-2">
			<span className="w-24 shrink-0 text-xs text-muted-foreground">{label}</span>
			<code className="min-w-0 flex-1 truncate rounded border bg-muted/50 px-2 py-1 font-mono text-xs">
				{shown ? value : "•".repeat(16)}
			</code>
			{secret && (
				<Button
					variant="ghost"
					size="sm"
					className="size-7 p-0"
					onClick={() => setShown((s) => !s)}
					aria-label={shown ? `Hide ${label}` : `Show ${label}`}
				>
					{shown ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
				</Button>
			)}
			<Button
				variant="ghost"
				size="sm"
				className="size-7 p-0"
				onClick={copy}
				aria-label={copied ? `${label} copied` : `Copy ${label}`}
			>
				{copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
			</Button>
		</div>
	);
}

/**
 * claude.ai and ChatGPT connect by signing in (Authentik), not with an API
 * key. Shown only when the deployment has agent sign-in on
 * (MCP_OAUTH_ISSUER and MCP_OAUTH_CLIENT_ID); otherwise renders nothing.
 */
export function SignInSetup({ serverUrl }: { serverUrl: string }) {
	const { data } = useQuery({
		queryKey: ["mcp", "agent-sign-in"],
		queryFn: () => agentSignInApiService.getDetails(),
		staleTime: 5 * 60 * 1000,
	});

	if (!data?.enabled || !data.client_id) return null;

	return (
		<section className="min-w-0 space-y-3">
			<h3 className="text-sm font-medium">Claude and ChatGPT: sign in</h3>
			<Tabs defaultValue={SIGN_IN_CLIENTS[0].id} className="min-w-0 w-full">
				<TabsList>
					{SIGN_IN_CLIENTS.map((client) => (
						<TabsTrigger key={client.id} value={client.id}>
							{client.label}
						</TabsTrigger>
					))}
				</TabsList>
				{SIGN_IN_CLIENTS.map((client) => (
					<TabsContent key={client.id} value={client.id} className="min-w-0 space-y-3">
						<ol className="list-decimal space-y-1 pl-5 text-sm leading-relaxed text-muted-foreground">
							{client.steps(serverUrl).map((step) => (
								<li key={step}>{step}</li>
							))}
						</ol>
					</TabsContent>
				))}
			</Tabs>
			<div className="space-y-1.5">
				<CopyableField label="Server URL" value={serverUrl} />
				<CopyableField label="Client ID" value={data.client_id} />
				{data.client_secret && (
					<CopyableField label="Client secret" value={data.client_secret} secret />
				)}
			</div>
		</section>
	);
}
