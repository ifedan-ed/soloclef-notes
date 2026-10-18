"use client";

import { useParams } from "next/navigation";
import { InviteAcceptCard } from "@/features/workspace-team/ui/invite-accept-card";

export default function InviteAcceptPage() {
	const params = useParams();
	return <InviteAcceptCard inviteCode={params.invite_code as string} />;
}
