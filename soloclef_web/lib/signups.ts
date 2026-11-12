/**
 * Whether the site offers sign-up (Create an account) or "Request access".
 *
 * Exactly `NEXT_PUBLIC_SIGNUPS_OPEN=true` at build time opens it; anything
 * else, unset included, offers "Request access" (REQUEST_ACCESS_HREF).
 * docker-compose.go.yml builds the website with it set from
 * SOLOCLEF_API_SIGNUP_ALLOWED in docker/.env.go, and Next.js writes it into
 * the pages and scripts there, so a change needs the website rebuilt: the
 * running container never reads it. The API reads the same setting for
 * GET /api/v1/sign-in-options, which the phone app follows.
 *
 * The API enforces the same switch itself: with SOLOCLEF_API_SIGNUP_ALLOWED
 * false it refuses POST /auth/register, so nothing in front of the server
 * has to (content/docs/self-hosting.mdx, "Sign-in").
 */
export const SIGNUPS_OPEN = process.env.NEXT_PUBLIC_SIGNUPS_OPEN === "true";

/** Where "Request access" leads while sign-ups are closed. */
export const REQUEST_ACCESS_HREF = "/contact";
