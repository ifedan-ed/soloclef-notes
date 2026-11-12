import { AutomationEditContent } from "./automation-edit-content";

export default async function AutomationEditPage({
	params,
}: {
	params: Promise<{ automation_id: string }>;
}) {
	const { automation_id } = await params;

	return (
		<div className="w-full space-y-6">
			<AutomationEditContent automationId={Number(automation_id)} />
		</div>
	);
}
