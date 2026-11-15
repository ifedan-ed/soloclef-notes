"use client";

import { DraftsWaitingPage } from "@/features/mail-drafts-waiting/ui/drafts-waiting-page";

/**
 * The emails the assistant drafted that wait to be sent or discarded. The
 * inbox row a waiting draft raises opens here (maildraftnotice.WaitingPage
 * on the server names this path).
 */
export default function MailDraftsRoute() {
	return <DraftsWaitingPage />;
}
