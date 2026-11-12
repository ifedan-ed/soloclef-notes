import { type NextRequest, NextResponse } from "next/server";
import { OAUTH_RESULT_COOKIE, type OAuthCallbackResult } from "@/contracts/types/oauth.types";
import { settingsPath } from "@/lib/dashboard-paths";

/**
 * Where the backend sends the browser once a provider's OAuth flow is done.
 * The result travels in a short-lived cookie to the connectors settings,
 * where `useConnectorDialog` (mounted there) reads it and carries on with
 * indexing or editing.
 */
export async function GET(request: NextRequest) {
	const searchParams = request.nextUrl.searchParams;

	const payload: OAuthCallbackResult = {
		success: searchParams.get("success"),
		error: searchParams.get("error"),
		connector: searchParams.get("connector"),
		connectorId: searchParams.get("connectorId"),
	};
	const result = JSON.stringify(payload);

	const response = new NextResponse(null, {
		status: 302,
		headers: { Location: settingsPath("connectors") },
	});
	response.cookies.set(OAUTH_RESULT_COOKIE, result, {
		path: "/",
		maxAge: 60,
		httpOnly: false,
		sameSite: "lax",
	});

	return response;
}
