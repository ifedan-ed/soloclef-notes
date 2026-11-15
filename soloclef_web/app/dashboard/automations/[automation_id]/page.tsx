import { STATIC_PLACEHOLDER } from "@/lib/routing/dynamic-segments";
import { AutomationRoute } from "./automation-route";

// One file for every automation, served by Caddy at /dashboard/automations/<id>.
export function generateStaticParams() {
	return [{ automation_id: STATIC_PLACEHOLDER }];
}

export default function AutomationDetailPage() {
	return <AutomationRoute />;
}
