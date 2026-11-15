"use client";

import { useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { useEffect, useRef } from "react";
import { toast } from "sonner";
import { replaceAddressQuery } from "@/lib/routing/replace-query";
import { JMAP_MAIL_PARAM, jmapMailOutcome } from "../lib/jmap-mail-outcome";

/**
 * Back from Stalwart's sign-in (`?jmap_mail=<outcome>`): say how it went
 * once, let the card show the mailbox, and drop the marker from the address
 * so a reload does not say it again.
 */
export function useJmapMailReturn(onReturn: () => void) {
	const t = useTranslations("jmap_mail_connect");
	const params = useSearchParams();
	const outcome = params.get(JMAP_MAIL_PARAM);
	const handled = useRef<string | null>(null);

	useEffect(() => {
		if (!outcome || handled.current === outcome) return;
		handled.current = outcome;
		const message = jmapMailOutcome(outcome);
		if (message?.tone === "success") toast.success(t(message.messageKey));
		else if (message) toast.error(t(message.messageKey));
		onReturn();
		const next = new URLSearchParams(params.toString());
		next.delete(JMAP_MAIL_PARAM);
		const query = next.toString();
		replaceAddressQuery(query);
	}, [outcome, params, t, onReturn]);
}
