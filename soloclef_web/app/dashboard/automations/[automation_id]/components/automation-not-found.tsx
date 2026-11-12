"use client";
import { ArrowLeft, FileWarning } from "lucide-react";
import Link from "next/link";
import { Button } from "@/components/ui/button";
import { settingsPath } from "@/lib/dashboard-paths";

interface AutomationNotFoundProps {
	error?: Error | null;
}

/**
 * Rendered when the detail fetch fails (404 / network) or the id is not a
 * number. Another account's automation is the same 404 as one that never
 * existed, so the copy only says it is not there.
 */
export function AutomationNotFound({ error }: AutomationNotFoundProps) {
	return (
		<div className="rounded-lg border border-border/60 bg-muted/20 px-6 py-12 text-center">
			<FileWarning className="mx-auto h-10 w-10 text-muted-foreground" aria-hidden />
			<h2 className="mt-3 text-base font-semibold text-foreground">Automation not found</h2>
			<p className="mt-1 text-sm text-muted-foreground max-w-md mx-auto">
				This automation doesn't exist, or it was deleted.
				{error?.message ? ` (${error.message})` : null}
			</p>
			<Button asChild variant="outline" size="sm" className="mt-6">
				<Link href={settingsPath("automations")}>
					<ArrowLeft className="mr-2 h-4 w-4" />
					Back to automations
				</Link>
			</Button>
		</div>
	);
}
