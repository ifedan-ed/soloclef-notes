import { STATIC_PLACEHOLDER } from "@/lib/routing/dynamic-segments";
import { AutomationRoute } from "../automation-route";

// One file for every automation's editor, served at /dashboard/automations/<id>/edit.
export function generateStaticParams() {
	return [{ automation_id: STATIC_PLACEHOLDER }];
}

export default function AutomationEditPage() {
	return <AutomationRoute edit />;
}
