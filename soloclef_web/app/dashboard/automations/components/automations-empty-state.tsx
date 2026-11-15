"use client";
import { AlarmClock } from "lucide-react";
import Link from "next/link";
import { Button } from "@/components/ui/button";
import { BRAND } from "@/lib/brand";
import { AUTOMATIONS_PAGE } from "@/lib/dashboard-paths";
import { CreateViaChatMenu } from "./create-via-chat-menu";

/**
 * Zero-state for the automations list: the two ways to make one, the chat
 * first (CreateViaChatMenu), since describing it is the quicker way.
 */
export function AutomationsEmptyState() {
	return (
		<div className="rounded-lg border border-dashed border-border/60 bg-muted/20 px-6 py-12 text-center">
			<div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-muted text-muted-foreground">
				<AlarmClock className="h-6 w-6" aria-hidden />
			</div>
			<h3 className="mt-4 text-base font-semibold text-foreground">No automations yet</h3>
			<p className="mt-1 text-sm text-muted-foreground max-w-md mx-auto">
				Automations let {BRAND.name} run tasks on a schedule or when a document arrives. Describe
				what you want in a notebook's chat and {BRAND.name} drafts it for you to check and create.
			</p>
			<div className="mt-6 flex items-center justify-center gap-2 flex-wrap">
				<CreateViaChatMenu size="default" />
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
