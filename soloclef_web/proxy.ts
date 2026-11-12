import { type NextRequest, NextResponse } from "next/server";
import { signedInHomeRedirect } from "@/lib/home-redirect";

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

	return NextResponse.next();
}

export const config = {
	matcher: ["/((?!api|auth|_next/static|_next/image|favicon.ico|.*\\..*).*)"],
};
