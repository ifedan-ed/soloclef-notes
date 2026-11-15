"use client";

import { AlertTriangle } from "lucide-react";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { useAgentTools, useSetAskFirst } from "../hooks/use-ask-first";
import { groupTools, toolKey } from "../lib/tool-groups";
import { toolLabel } from "../lib/tool-label";

/**
 * Which tools the assistant asks about first.
 *
 * Every tool runs without asking (the owner's rule: "let default be to
 * always allow all, then settings you can change it"), and each switch here
 * makes one tool ask first. The list is the server's, headings and order
 * included, so a tool added there appears here with nothing to change. A
 * switch saves at once; there is nothing to confirm, because either way the
 * person can flip it back.
 */
export function AskFirstSettings() {
	const tools = useAgentTools();
	const setAskFirst = useSetAskFirst();

	return (
		<div className="min-w-0 space-y-8" data-testid="ask-first-settings">
			<div className="space-y-2 text-sm text-pretty text-muted-foreground">
				<p>
					The assistant uses every tool without asking. Switch on Ask first for a tool you want to
					approve each time: the chat then shows what it is about to do and waits for you. Choosing
					Always allow on that card switches it off again.
				</p>
				<p>
					An email is never sent from the chat without you: a draft waits for you to press Send,
					whatever is set here. A connected app&apos;s tools are listed under its name once it has
					been connected.
				</p>
			</div>

			{tools.isLoading ? (
				<ToolsSkeleton />
			) : tools.isError ? (
				<Alert variant="destructive">
					<AlertTriangle />
					<AlertTitle>Could not load the tools</AlertTitle>
					<AlertDescription className="space-y-3">
						<p>{tools.error instanceof Error ? tools.error.message : "Please try again."}</p>
						<Button size="sm" variant="outline" onClick={() => tools.refetch()}>
							Try again
						</Button>
					</AlertDescription>
				</Alert>
			) : (
				groupTools(tools.data ?? []).map((group) => (
					<section
						key={`${group.tools[0]?.connector_id ?? "chat"}:${group.heading}`}
						className="space-y-3"
					>
						<h3 className="text-base font-semibold tracking-tight">{group.heading}</h3>
						<ul className="divide-y divide-border/60 rounded-lg border border-border/60">
							{group.tools.map((tool) => {
								const id = `ask-first-${toolKey(tool)}`;
								const label = toolLabel(tool.tool);
								return (
									<li key={id} className="flex items-center justify-between gap-4 px-4 py-3">
										<div className="min-w-0 space-y-0.5">
											<Label htmlFor={id} className="block truncate">
												{label}
											</Label>
											{/* The name the approval card shows, so the two can be matched. */}
											<code className="block break-all font-mono text-xs text-muted-foreground">
												{tool.tool}
											</code>
										</div>
										<div className="flex shrink-0 items-center gap-2">
											<span className="text-xs text-muted-foreground" aria-hidden>
												Ask first
											</span>
											<Switch
												id={id}
												aria-label={`Ask first before ${label.toLowerCase()}`}
												checked={tool.ask_first}
												onCheckedChange={(on) =>
													setAskFirst.mutate({
														tool: tool.tool,
														connector_id: tool.connector_id,
														ask_first: on,
													})
												}
											/>
										</div>
									</li>
								);
							})}
						</ul>
					</section>
				))
			)}
		</div>
	);
}

function ToolsSkeleton() {
	return (
		<div className="space-y-3" aria-busy>
			<Skeleton className="h-5 w-40" />
			<div className="divide-y rounded-lg border">
				{[0, 1, 2].map((row) => (
					<div key={row} className="flex items-center justify-between gap-4 px-4 py-3">
						<div className="space-y-2">
							<Skeleton className="h-4 w-32" />
							<Skeleton className="h-3 w-44 max-w-full" />
						</div>
						<Skeleton className="h-6 w-11 shrink-0 rounded-full" />
					</div>
				))}
			</div>
		</div>
	);
}
