import {
	AlarmClock,
	Bell,
	FileText,
	type LucideIcon,
	MailOpen,
	Smartphone,
	Sparkles,
	Trash2,
	Unplug,
} from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * The small mark at the start of a status row, saying what it is about at a
 * glance; the same icons the app uses for those things elsewhere (Studio's
 * sparkles, the automations' alarm clock, the connectors' plug). A failed
 * item's mark turns the destructive colour, so failures stand out in a list.
 */
const ICONS: Record<string, LucideIcon> = {
	studio_output: Sparkles,
	automation_run: AlarmClock,
	mail_draft: MailOpen,
	app_update: Smartphone,
	document_processing: FileText,
	connector_indexing: Unplug,
	connector_deletion: Trash2,
};

export function InboxTypeIcon({
	type,
	failed,
	className,
}: {
	type: string;
	failed: boolean;
	className?: string;
}) {
	// A type this build does not know yet still gets a mark: the bell of the inbox itself.
	const Icon = ICONS[type] ?? Bell;
	return (
		<Icon
			aria-hidden
			className={cn(
				"size-3.5 shrink-0",
				failed ? "text-destructive" : "text-muted-foreground",
				className
			)}
		/>
	);
}
