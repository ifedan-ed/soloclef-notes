"use client";

import { Info } from "lucide-react";
import type { FC } from "react";
import { useState } from "react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { isHttpUrl } from "@/lib/url";
import { cn } from "@/lib/utils";
import type { ConnectorConfigProps } from "../index";

/** Return the malformed (non-http/https) lines from a newline-separated URL list. */
function invalidUrlLines(value: string): string[] {
	return value
		.split("\n")
		.map((line) => line.trim())
		.filter((line) => line && !isHttpUrl(line));
}

export const WebcrawlerConfig: FC<ConnectorConfigProps> = ({ connector, onConfigChange }) => {
	// The page list is the only setting of a crawler connection the server
	// answers or accepts. There used to be a Firecrawl key field here, filled
	// in from the connection's config: the server no longer sends a stored key
	// back to a browser, and refuses one sent to it, so the field is gone.
	const existingUrls = (connector.config?.INITIAL_URLS as string | undefined) || "";

	const [initialUrls, setInitialUrls] = useState(existingUrls);

	const invalidUrls = invalidUrlLines(initialUrls);

	const handleUrlsChange = (value: string) => {
		setInitialUrls(value);
		if (onConfigChange) {
			// Preserve newlines for multi-line URL input
			// Backend will handle trimming individual URLs when splitting by newline
			onConfigChange({
				...connector.config,
				INITIAL_URLS: value || undefined,
			});
		}
	};

	return (
		<div className="rounded-xl border border-border bg-slate-400/5 dark:bg-white/5 p-3 sm:p-6 space-y-4 sm:space-y-6">
			<div className="space-y-1 sm:space-y-2">
				<h3 className="font-medium text-sm sm:text-base">Web Crawler Configuration</h3>
				<p className="text-xs sm:text-sm text-muted-foreground">
					Add webpages to your knowledge base for periodic indexing.
				</p>
			</div>

			{/* Chat tip */}
			<Alert>
				<Info />
				<AlertDescription>
					Want a quick answer from a webpage without indexing it? Just paste the URL directly into
					the chat instead.
				</AlertDescription>
			</Alert>

			{/* Initial URLs Field */}
			<div className="space-y-2">
				<Label htmlFor="initial-urls" className="text-xs sm:text-sm">
					Initial URLs (Optional)
				</Label>
				<Textarea
					id="initial-urls"
					placeholder="https://example.com&#10;https://docs.example.com&#10;https://blog.example.com"
					value={initialUrls}
					onChange={(e) => handleUrlsChange(e.target.value)}
					aria-invalid={invalidUrls.length > 0}
					className={cn(
						"min-h-[100px] font-mono text-xs sm:text-sm bg-slate-400/5 dark:bg-white/5 border-slate-400/20 resize-none",
						invalidUrls.length > 0 && "border-destructive"
					)}
				/>
				{invalidUrls.length > 0 ? (
					<p className="text-[10px] sm:text-xs text-destructive">
						Not valid http(s) URLs: {invalidUrls.join(", ")}
					</p>
				) : (
					<p className="text-[10px] sm:text-xs text-muted-foreground">
						Enter URLs to crawl (one per line). You can add more URLs later.
					</p>
				)}
			</div>

			{/* Info Alert */}
			<Alert>
				<Info />
				<AlertDescription>
					Configuration is saved when you start indexing. You can update these settings anytime from
					the connector management page.
				</AlertDescription>
			</Alert>
		</div>
	);
};
