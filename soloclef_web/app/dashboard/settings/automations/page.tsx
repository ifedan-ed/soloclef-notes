import { AutomationsContent } from "../../automations/automations-content";

/** The account's automations, listed beside its other settings. */
export default function AutomationsSettingsPage() {
	return (
		<div className="w-full space-y-6">
			<AutomationsContent />
		</div>
	);
}
