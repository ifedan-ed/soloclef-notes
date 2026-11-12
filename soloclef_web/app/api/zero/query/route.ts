import { mustGetQuery } from "@rocicorp/zero";
import { handleQueryRequest } from "@rocicorp/zero/server";
import { NextResponse } from "next/server";
import { SERVER_BACKEND_URL } from "@/lib/env-config";
import { ZERO_CONTEXT_PATH, zeroContextFromAccount } from "@/lib/zero/zero-context";
import type { Context } from "@/types/zero";
import { queries } from "@/zero/queries";
import { schema } from "@/zero/schema";

// This route is invoked server-to-server by zero-cache (via ZERO_QUERY_URL),
// so it must reach the backend over the internal Docker network
// (e.g. http://backend:8000). The browser-facing NEXT_PUBLIC_FASTAPI_BACKEND_URL
// (e.g. http://localhost:8929) does NOT resolve from inside the frontend
// container and would make every authenticated Zero query fail with a 503.
const backendURL = SERVER_BACKEND_URL.replace(/\/$/, "");
const zeroQueryApiKey = process.env.ZERO_QUERY_API_KEY;

function validateZeroCacheRequest(request: Request): NextResponse | null {
	if (!zeroQueryApiKey) return null;
	if (request.headers.get("X-Api-Key") === zeroQueryApiKey) return null;
	return NextResponse.json({ error: "Forbidden" }, { status: 403 });
}

async function authenticateRequest(
	request: Request
): Promise<
	{ ctx: Exclude<Context, undefined>; error?: never } | { ctx?: never; error: NextResponse }
> {
	const authHeader = request.headers.get("Authorization");
	const cookieHeader = request.headers.get("Cookie");
	const headers: HeadersInit = {};
	if (authHeader?.startsWith("Bearer ")) {
		headers.Authorization = authHeader;
	} else if (cookieHeader) {
		headers.Cookie = cookieHeader;
	} else {
		return { error: NextResponse.json({ error: "Unauthorized" }, { status: 401 }) };
	}

	try {
		const res = await fetch(`${backendURL}${ZERO_CONTEXT_PATH}`, {
			headers,
		});

		if (!res.ok) {
			return { error: NextResponse.json({ error: "Unauthorized" }, { status: 401 }) };
		}

		const ctx = zeroContextFromAccount(await res.json().catch(() => null));
		if (!ctx) {
			return { error: NextResponse.json({ error: "Unauthorized" }, { status: 401 }) };
		}
		return { ctx };
	} catch {
		return { error: NextResponse.json({ error: "Auth service unavailable" }, { status: 503 }) };
	}
}

export async function POST(request: Request) {
	const forbidden = validateZeroCacheRequest(request);
	if (forbidden) {
		return forbidden;
	}

	const auth = await authenticateRequest(request);
	if (auth.error) {
		return auth.error;
	}

	const result = await handleQueryRequest({
		handler: (name, args) => {
			const query = mustGetQuery(queries, name);
			return query.fn({ args, ctx: auth.ctx });
		},
		schema,
		request,
		userID: auth.ctx.userId,
	});

	return NextResponse.json(result);
}
