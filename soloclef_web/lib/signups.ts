/**
 * Whether the site offers sign-up (Create an account) or "Request access".
 *
 * Exactly `NEXT_PUBLIC_SIGNUPS_OPEN=true` in docker/.env opens it; anything
 * else, unset included, offers "Request access" (REQUEST_ACCESS_HREF).
 * docker-compose.yml hands the line to the website build, and Next.js writes
 * it into the pages and scripts there, so a change needs the website rebuilt:
 * the running container never reads it. The backend reads the same line for
 * GET /api/v1/sign-in-options, which the phone app follows.
 *
 * It only changes what is offered. The API's POST /auth/register stays open
 * unless the proxy in front of the server refuses it, which is how a server
 * closes sign-ups and keeps sign-in (REGISTRATION_ENABLED=FALSE stops every
 * sign-in). content/docs/self-hosting.mdx, "Closing sign-ups".
 */
export const SIGNUPS_OPEN = process.env.NEXT_PUBLIC_SIGNUPS_OPEN === "true";

/** Where "Request access" leads while sign-ups are closed. */
export const REQUEST_ACCESS_HREF = "/contact";
