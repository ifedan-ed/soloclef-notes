import { type NextRequest, NextResponse } from "next/server";
import { BUILD_TIME_AUTH_TYPE } from "@/lib/env-config";
import { signedInHomeRedirect } from "@/lib/home-redirect";
import { RUNTIME_AUTH_TYPE_COOKIE_NAME, resolveRuntimeAuthUiMode } from "@/lib/runtime-auth-config";

export function proxy(request: NextRequest) {
	// The landing page is for visitors. Someone with a session cookie goes
	// straight to their notebooks; an expired cookie lands on /dashboard, which
	// sends them to /login as it does today.
	const home = signedInHomeRedirect(request.nextUrl.pathname, (name) => request.cookies.has(name));
	if (home) {
		const destination = request.nextUrl.clone();
		destination.pathname = home;
		destination.search = "";
		return NextResponse.redirect(destination);
	}

	const response = NextResponse.next();
	const authType = resolveRuntimeAuthUiMode(process.env.AUTH_TYPE, BUILD_TIME_AUTH_TYPE);

	response.cookies.set(RUNTIME_AUTH_TYPE_COOKIE_NAME, authType, {
		path: "/",
		maxAge: 60 * 60 * 24 * 365,
		sameSite: "lax",
		secure: request.nextUrl.protocol === "https:",
	});

	return response;
}

export const config = {
	matcher: ["/((?!api|auth|_next/static|_next/image|favicon.ico|.*\\..*).*)"],
};
