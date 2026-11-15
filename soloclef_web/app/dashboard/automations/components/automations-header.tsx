"use client";
import Link from "next/link";
import { Button } from "@/components/ui/button";
import { AUTOMATIONS_PAGE } from "@/lib/dashboard-paths";
import { CreateViaChatMenu } from "./create-via-chat-menu";

interface AutomationsHeaderProps {
	total: number;
	loading: boolean;
	/**
	 * Render the header's Create CTA. Defaults to true; the empty state owns
	 * the primary CTA on its own card, so the orchestrator turns this off
	 * there to avoid a duplicate button.
	 */
	showCreateCta?: boolean;
}

/**
 * Page header: title + count + the two ways to make one: in a notebook's
 * chat, where the assistant drafts it onto a card (CreateViaChatMenu), or
 * by hand in the builder.
 */
export function AutomationsHeader({
	total,
	loading,
	showCreateCta = true,
}: AutomationsHeaderProps) {
	return (
		<div className="flex items-center justify-between gap-4 flex-wrap">
			<div className="flex items-baseline gap-3">
				<h1 className="text-xl md:text-2xl font-semibold text-foreground">Automations</h1>
				{!loading && (
					<span className="text-sm text-muted-foreground">
						{total} {total === 1 ? "automation" : "automations"}
					</span>
				)}
			</div>
			{showCreateCta && (
				<div className="flex items-center gap-2">
					<Button
						asChild
						size="sm"
						variant="ghost"
						className="justify-start rounded-md bg-muted px-3 hover:bg-accent"
					>
						<Link href={`${AUTOMATIONS_PAGE}/new`}>Create manually</Link>
					</Button>
					<CreateViaChatMenu />
				</div>
			)}
		</div>
	);
}
