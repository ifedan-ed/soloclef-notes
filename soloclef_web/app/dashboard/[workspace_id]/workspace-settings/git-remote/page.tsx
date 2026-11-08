import { redirect } from "next/navigation";

/**
 * The Connected repo page is gone: it needs git storage, which is off, and
 * the backend code stays dormant. An old link or bookmark lands on General
 * straight from the server, so nothing flashes.
 */
export default async function Page({ params }: { params: Promise<{ workspace_id: string }> }) {
	const { workspace_id } = await params;
	redirect(`/dashboard/${workspace_id}/workspace-settings/general`);
}
