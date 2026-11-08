"use client";

import { Check, Copy } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { BRAND } from "@/lib/brand";
import {
	API_KEY_PLACEHOLDER,
	DEFAULT_SERVER_DIR,
	MCP_CLIENTS,
	type McpSnippetOptions,
	type McpTransport,
	REMOTE_URL,
} from "@/lib/mcp/clients";

function CopyButton({ text }: { text: string }) {
	const [copied, setCopied] = useState(false);

	const handleCopy = async () => {
		try {
			await navigator.clipboard.writeText(text);
			setCopied(true);
			setTimeout(() => setCopied(false), 2000);
		} catch {
			// Clipboard unavailable (permissions/insecure context); nothing to recover.
		}
	};

	return (
		<Button
			variant="ghost"
			size="sm"
			className="absolute top-2 right-2 size-7 p-0"
			onClick={handleCopy}
			aria-label={copied ? "Configuration copied" : "Copy configuration"}
		>
			{copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
		</Button>
	);
}

const TRANSPORTS: { id: McpTransport; label: string; hint: string }[] = [
	{ id: "remote", label: "Remote", hint: "this server, nothing to install" },
	{ id: "stdio", label: "Self-host", hint: `runs against this ${BRAND.name}` },
];

/**
 * Per-agent MCP setup instructions as tabs: pick a client, then Hosted or
 * Self-host, and copy its exact config. Used on the /mcp-server marketing page
 * and in the in-app connect dialog; `options` fills in real values where the caller
 * has them.
 */
/**
 * `selfHosted` is a prop rather than a `useIsSelfHosted()` call because this
 * component is also rendered on the public /mcp-server page, which Next
 * prerenders at build time with no RuntimeConfigProvider around it — reading
 * the runtime config here failed the build outright. The in-app dialog knows
 * the answer and passes it; the marketing page keeps the hosted-first default
 * it is written for.
 */
export function AgentSetupTabs({
	options,
	selfHosted = false,
}: {
	options?: Partial<McpSnippetOptions>;
	selfHosted?: boolean;
}) {
	// A self-hosted deployment has no account on mcp.surfsense.com, so opening
	// on Hosted showed a config that could never work here and hid the one
	// that does behind a second click.
	// A deployment that serves its own /mcp passes it as remoteUrl; then Remote
	// works here and is the easiest option.
	const ownRemote = Boolean(options?.remoteUrl);
	const [transport, setTransport] = useState<McpTransport>(
		selfHosted && !ownRemote ? "stdio" : "remote"
	);

	const resolved: McpSnippetOptions = {
		remoteUrl: options?.remoteUrl || REMOTE_URL,
		apiKey: options?.apiKey || API_KEY_PLACEHOLDER,
		// Upstream's cloud API is only the right fallback on the cloud. On a
		// self-hosted box an unset backend URL should look obviously unset,
		// not quietly point the agent at somebody else's server.
		baseUrl:
			options?.baseUrl || (selfHosted ? "http://localhost:8000" : "https://api.surfsense.com"),
		serverDir: options?.serverDir || DEFAULT_SERVER_DIR,
	};

	// Upstream's hosted server is not a thing a self-hoster can use, so it is
	// not offered here — an option that cannot work is worse than no option.
	const transports =
		selfHosted && !ownRemote ? TRANSPORTS.filter((t) => t.id === "stdio") : TRANSPORTS;
	const active = transports.find((t) => t.id === transport) ?? transports[0];

	return (
		<div className="min-w-0 space-y-4">
			<div className="flex flex-wrap items-center gap-3">
				<div className="inline-flex rounded-lg border bg-muted/40 p-0.5">
					{transports.map((t) => (
						<Button
							key={t.id}
							variant={t.id === transport ? "secondary" : "ghost"}
							size="sm"
							className="h-7 px-3 text-xs"
							onClick={() => setTransport(t.id)}
							aria-pressed={t.id === transport}
						>
							{t.label}
						</Button>
					))}
				</div>
				<span className="text-xs text-muted-foreground">{active.hint}</span>
			</div>

			<Tabs defaultValue={MCP_CLIENTS[0].id} className="min-w-0 w-full">
				<div className="w-full max-w-full overflow-x-auto overscroll-x-contain">
					<TabsList className="flex h-auto w-max min-w-full flex-nowrap justify-start gap-1">
						{MCP_CLIENTS.map((client) => (
							<TabsTrigger key={client.id} value={client.id}>
								{client.label}
							</TabsTrigger>
						))}
					</TabsList>
				</div>
				{MCP_CLIENTS.map((client) => {
					const snippet = client[transport];
					const config = snippet.build(resolved);
					return (
						<TabsContent key={client.id} value={client.id} className="min-w-0 space-y-3">
							<ol className="list-decimal space-y-1 pl-5 text-sm leading-relaxed text-muted-foreground">
								{snippet.steps.map((step) => (
									<li key={step}>{step}</li>
								))}
							</ol>
							<div>
								<p className="mb-1.5 font-mono text-xs text-muted-foreground">
									{snippet.configFile}
								</p>
								<div className="relative">
									<CopyButton text={config} />
									<pre className="overflow-x-auto rounded-lg border bg-muted/50 p-4 font-mono text-xs leading-relaxed">
										<code>{config}</code>
									</pre>
								</div>
							</div>
						</TabsContent>
					);
				})}
			</Tabs>
		</div>
	);
}
