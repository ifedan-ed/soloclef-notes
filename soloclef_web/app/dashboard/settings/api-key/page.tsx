"use client";

import { ApiAccessSwitch } from "@/features/account-settings/ui/api-access-switch";
import { ApiKeyContent } from "../components/ApiKeyContent";

/** API Access: the switch that allows it, and the personal tokens. */
export default function ApiKeySettingsPage() {
	return (
		<div className="space-y-10">
			<ApiAccessSwitch />
			<ApiKeyContent />
		</div>
	);
}
