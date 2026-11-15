"use client";

import { CheckCircle2, ChevronDown, ChevronUp, XCircle } from "lucide-react";
import { useState } from "react";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import type { MCPTestConnectionResponse } from "@/contracts/types/mcp.types";

interface ConnectionTestProps {
	/** Null when the test cannot run yet, with the reason in `blockedReason`. */
	onTest: (() => void) | null;
	blockedReason?: string;
	testing: boolean;
	result: MCPTestConnectionResponse | null;
	disabled?: boolean;
}

/**
 * Connects to the server as the form describes it, before anything is
 * saved, and lists what it offers: the one way to know a key is right
 * without waiting for a chat to fail.
 */
export function ConnectionTest({
	onTest,
	blockedReason,
	testing,
	result,
	disabled,
}: ConnectionTestProps) {
	const [showTools, setShowTools] = useState(false);
	return (
		<div className="flex flex-col gap-3">
			{onTest ? (
				<Button
					type="button"
					variant="secondary"
					className="h-9 w-full sm:w-fit"
					disabled={testing || disabled}
					onClick={onTest}
				>
					{testing ? (
						<>
							<Spinner size="xs" />
							Testing connection…
						</>
					) : (
						"Test connection"
					)}
				</Button>
			) : (
				<p className="text-xs text-muted-foreground">{blockedReason}</p>
			)}
			{result && (
				<Alert
					className={
						result.status === "success"
							? "border-green-500/50 bg-green-500/10"
							: "border-red-500/50 bg-red-500/10"
					}
				>
					{result.status === "success" ? (
						<CheckCircle2 className="size-4 text-green-600" />
					) : (
						<XCircle className="size-4 text-red-600" />
					)}
					<div className="col-start-2 flex flex-wrap items-center justify-between gap-2">
						<AlertTitle className="text-sm">
							{result.status === "success" ? "Connected" : "Could not connect"}
						</AlertTitle>
						{result.tools.length > 0 && (
							<Button
								type="button"
								variant="ghost"
								size="sm"
								className="h-6 px-2 text-xs"
								aria-expanded={showTools}
								onClick={() => setShowTools((now) => !now)}
							>
								{showTools ? <ChevronUp className="size-3" /> : <ChevronDown className="size-3" />}
								{showTools ? "Hide tools" : `Show ${result.tools.length} tools`}
							</Button>
						)}
					</div>
					<AlertDescription className="text-xs">
						{result.message}
						{showTools && result.tools.length > 0 && (
							<ul className="mt-3 flex flex-col gap-1.5 border-t border-green-500/20 pt-3">
								{result.tools.map((tool) => (
									<li key={tool.name} className="min-w-0">
										<span className="font-mono text-xs font-medium break-all">{tool.name}</span>
										{tool.description && (
											<span className="block text-muted-foreground line-clamp-2">
												{tool.description}
											</span>
										)}
									</li>
								))}
							</ul>
						)}
					</AlertDescription>
				</Alert>
			)}
		</div>
	);
}
