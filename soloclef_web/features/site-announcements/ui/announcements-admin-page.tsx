"use client";

import { Send } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Textarea } from "@/components/ui/textarea";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import { useSiteAdmin } from "@/hooks/use-site-admin";
import { useAnnouncementsOverview, useSendAnnouncement } from "../hooks/use-announcements-admin";
import {
	type AnnouncementDraft,
	BODY_MAX,
	draftProblems,
	draftToBody,
	emptyDraft,
	TITLE_MAX,
} from "../lib/announcement-draft";
import { AnnouncementPreview } from "./announcement-preview";
import { SentAnnouncementsList } from "./sent-announcements-list";

/** Site administrators write an announcement, preview it, send it, and withdraw it later. */
export function AnnouncementsAdminPage() {
	const me = useSiteAdmin();
	const isAdmin = me.isAdmin === true;
	const overview = useAnnouncementsOverview(isAdmin);
	const [draft, setDraft] = useState<AnnouncementDraft>(emptyDraft);
	const [tried, setTried] = useState(false);
	const send = useSendAnnouncement(() => {
		setDraft(emptyDraft);
		setTried(false);
	});
	const showSkeleton = useDelayedFlag(me.isLoading || (isAdmin && overview.isLoading));

	if (me.isAdmin === false) {
		return (
			<p className="text-sm text-muted-foreground">
				Only site administrators can send announcements.
			</p>
		);
	}
	if (overview.isError) {
		return <p className="text-sm text-destructive">Announcements could not be loaded.</p>;
	}
	if (!overview.data) {
		return showSkeleton ? <Skeleton className="h-64 w-full" /> : null;
	}

	const problems = draftProblems(draft);
	const set = (patch: Partial<AnnouncementDraft>) => setDraft((prev) => ({ ...prev, ...patch }));

	const onSend = () => {
		setTried(true);
		if (problems.length === 0) send.mutate(draftToBody(draft));
	};

	return (
		<div className="min-w-0 space-y-6">
			<p className="text-sm text-muted-foreground">
				Send a notification to every account. It lands in Notifications with the unread badge.
			</p>

			<div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_360px]">
				<div className="space-y-4">
					<div className="space-y-1.5">
						<Label htmlFor="announcement-title">Title</Label>
						<Input
							id="announcement-title"
							value={draft.title}
							maxLength={TITLE_MAX}
							placeholder="Podcasts now run to 30 minutes"
							onChange={(event) => set({ title: event.target.value })}
						/>
					</div>
					<div className="space-y-1.5">
						<Label htmlFor="announcement-body">Message</Label>
						<Textarea
							id="announcement-body"
							value={draft.body}
							maxLength={BODY_MAX}
							rows={5}
							placeholder="Short and to the point."
							onChange={(event) => set({ body: event.target.value })}
						/>
						<p className="text-xs text-muted-foreground">
							**bold**, *italic*, `code`, [text](https://…) and “- ” bullets.
						</p>
					</div>
					<div className="grid gap-4 sm:grid-cols-2">
						<div className="space-y-1.5">
							<Label htmlFor="announcement-link">Link (optional)</Label>
							<Input
								id="announcement-link"
								value={draft.link}
								placeholder="https://… or /docs"
								onChange={(event) => set({ link: event.target.value })}
							/>
						</div>
						<div className="space-y-1.5">
							<Label htmlFor="announcement-expiry">Expires (optional)</Label>
							<Input
								id="announcement-expiry"
								type="datetime-local"
								value={draft.expiresAt}
								onChange={(event) => set({ expiresAt: event.target.value })}
							/>
						</div>
					</div>
					{tried && problems.length > 0 ? (
						<ul className="list-disc space-y-0.5 pl-5 text-sm text-destructive">
							{problems.map((problem) => (
								<li key={problem}>{problem}</li>
							))}
						</ul>
					) : null}
					<div className="flex flex-wrap items-center gap-3">
						<Button onClick={onSend} disabled={send.isPending}>
							<Send className="size-4" aria-hidden />
							Send
						</Button>
						<p className="text-sm text-muted-foreground">Goes to every account.</p>
					</div>
				</div>

				<div className="space-y-1.5">
					<p className="text-sm font-medium">Preview</p>
					<AnnouncementPreview title={draft.title} body={draft.body} link={draft.link} />
				</div>
			</div>

			<div className="space-y-2">
				<h3 className="text-sm font-semibold">Sent</h3>
				<SentAnnouncementsList items={overview.data.announcements} />
			</div>
		</div>
	);
}
