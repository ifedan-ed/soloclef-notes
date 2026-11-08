/**
 * Agents that connect by signing in (the MCP authorization spec) instead of
 * with an API key: claude.ai (also Claude Desktop and mobile, which share its
 * connectors) and ChatGPT. Both reach Soloclef's Authentik through the
 * protected-resource document the MCP server publishes; Authentik offers them
 * no client registration they can use, so the person pastes the client ID and
 * secret shown next to these steps. Wording follows each product's own docs
 * (Claude: "Use your own OAuth client"; ChatGPT: developer mode, Advanced
 * OAuth settings).
 */

export interface SignInClient {
	id: "claude" | "chatgpt";
	label: string;
	steps: (serverUrl: string) => string[];
}

export const SIGN_IN_CLIENTS: SignInClient[] = [
	{
		id: "claude",
		label: "Claude",
		steps: (serverUrl) => [
			"In Claude (web, desktop or mobile) open Customize → Connectors → Add custom connector. On a Team or Enterprise plan an Owner adds it under Organization settings → Connectors.",
			`Name it, and enter ${serverUrl} as the server URL.`,
			"Under OAuth client choose “Use your own OAuth client” (or open Advanced settings) and paste the client ID and secret below.",
			"Add it, click Connect and sign in with your single sign-on account.",
		],
	},
	{
		id: "chatgpt",
		label: "ChatGPT",
		steps: (serverUrl) => [
			"In ChatGPT open Settings → Apps & Connectors → Advanced settings and turn on Developer mode (on Business and Enterprise an admin may need to allow it).",
			`Create an app: name it, enter ${serverUrl} as the MCP server URL and choose OAuth.`,
			"Under Advanced OAuth settings paste the client ID and secret below; the token method is client_secret_post.",
			"Create it and sign in with your single sign-on account when asked.",
		],
	},
];
