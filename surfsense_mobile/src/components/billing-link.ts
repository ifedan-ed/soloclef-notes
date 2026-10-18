import * as WebBrowser from "expo-web-browser";
import { useCallback } from "react";
import { useApp } from "../lib/app-context";
import { type BillingAction, billingActionLabelKey, billingUrl } from "../lib/billing";

/** Notice props that send a billing refusal to the web's billing page; nothing for other errors. */
export function useBillingNoticeAction() {
	const { api, t } = useApp();
	return useCallback(
		(action: BillingAction | null | undefined) =>
			action
				? {
						actionLabel: t(billingActionLabelKey(action)),
						onAction: () => void WebBrowser.openBrowserAsync(billingUrl(api.serverUrl)),
					}
				: {},
		[api, t]
	);
}
