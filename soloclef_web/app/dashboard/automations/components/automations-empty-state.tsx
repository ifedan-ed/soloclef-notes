"use client";
import { AlarmClock } from "lucide-react";
import Link from "next/link";
import { Button } from "@/components/ui/button";
import { BRAND } from "@/lib/brand";
import { AUTOMATIONS_PAGE, NOTEBOOKS_HOME } from "@/lib/dashboard-paths";

/**
 * Zero-state for the automations list. The primary CTA points to a new
 * chat — creation happens via the ``create_automation`` HITL tool, not a
 * "new automation" form. We surface the chat path explicitly so users
 * don't go hunting for an "add" button that doesn't exist.
 */
export function AutomationsEmptyState() {
	return (
		<div className="rounded-lg border border-dashed border-border/60 bg-muted/20 px-6 py-12 text-center">
			<div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-muted text-muted-foreground">
				<AlarmClock className="h-6 w-6" aria-hidden />
			</div>
			<h3 className="mt-4 text-base font-semibold text-foreground">No automations yet</h3>
			<p className="mt-1 text-sm text-muted-foreground max-w-md mx-auto">
				Automations let {BRAND.name} run agent tasks on a schedule. Describe what you want in chat
				and {BRAND.name} drafts the automation for your approval.
			</p>
			<div className="mt-6 flex items-center justify-center gap-2 flex-wrap">
				<Button asChild>
					<Link href={NOTEBOOKS_HOME}>Create via chat</Link>
				</Button>
				<Button
					asChild
					variant="ghost"
					className="h-10 justify-start rounded-md bg-muted px-3 text-sm hover:bg-accent"
				>
					<Link href={`${AUTOMATIONS_PAGE}/new`}>Create manually</Link>
				</Button>
			</div>
		</div>
	);
}
