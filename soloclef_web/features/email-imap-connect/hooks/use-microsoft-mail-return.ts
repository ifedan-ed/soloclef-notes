"use client";

import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { useEffect, useRef } from "react";
import { toast } from "sonner";
import { MICROSOFT_MAIL_PARAM, microsoftMailOutcome } from "../lib/microsoft-mail-outcome";

/**
 * Back from Microsoft's sign-in (`?microsoft_mail=<outcome>`): say how it
 * went once, let the card show the mailbox, and drop the marker from the URL.
 */
export function useMicrosoftMailReturn(onReturn: () => void) {
	const t = useTranslations("email_imap_connect");
	const params = useSearchParams();
	const router = useRouter();
	const pathname = usePathname();
	const outcome = params.get(MICROSOFT_MAIL_PARAM);
	const handled = useRef<string | null>(null);

	useEffect(() => {
		if (!outcome || handled.current === outcome) return;
		handled.current = outcome;
		const message = microsoftMailOutcome(outcome);
		if (message?.tone === "success") toast.success(t(message.messageKey));
		else if (message) toast.error(t(message.messageKey));
		onReturn();
		const next = new URLSearchParams(params.toString());
		next.delete(MICROSOFT_MAIL_PARAM);
		const query = next.toString();
		router.replace(`${pathname}${query ? `?${query}` : ""}`, { scroll: false });
	}, [outcome, params, pathname, router, t, onReturn]);
}
