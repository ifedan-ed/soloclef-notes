import { AutomationDetailContent } from "./automation-detail-content";

export default async function AutomationDetailPage({
	params,
}: {
	params: Promise<{ automation_id: string }>;
}) {
	const { automation_id } = await params;

	return (
		<div className="w-full space-y-6">
			<AutomationDetailContent automationId={Number(automation_id)} />
		</div>
	);
}
