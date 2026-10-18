import { ExternalLink, Megaphone } from "lucide-react";
import { MarkdownLiteView } from "./markdown-lite-view";

/** The announcement as a recipient sees it in Notifications. */
export function AnnouncementPreview({
	title,
	body,
	link,
}: {
	title: string;
	body: string;
	link: string;
}) {
	return (
		<div className="w-full max-w-[360px] rounded-xl border bg-popover p-2 text-popover-foreground shadow-sm">
			<div className="rounded-lg bg-accent/40 px-2 py-2">
				<div className="flex min-w-0 items-start gap-2">
					<Megaphone className="mt-0.5 h-3.5 w-3.5 shrink-0 text-primary" aria-hidden />
					<p className="flex-1 text-sm font-semibold">{title.trim() || "Title"}</p>
					<span className="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-primary" />
				</div>
				<MarkdownLiteView
					source={body.trim() || "The announcement text."}
					className="mt-1 pl-5.5 text-xs text-muted-foreground"
				/>
				{link.trim() ? (
					<p className="mt-1.5 flex items-center gap-1 pl-5.5 text-xs font-medium text-primary">
						Open
						<ExternalLink className="h-3 w-3" aria-hidden />
					</p>
				) : null}
				<p className="mt-1 pl-5.5 text-[11px] text-muted-foreground/70">now</p>
			</div>
		</div>
	);
}
