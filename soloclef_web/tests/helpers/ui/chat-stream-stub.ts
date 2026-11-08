import type { Page } from "@playwright/test";

/**
 * A browser-side stand-in for `POST /api/v1/new_chat` that streams only what
 * the test pushes, so a test can look at an answer while it is still being
 * written (the isolated stack's fake model answers at once). The events are
 * the backend's own shapes (`data: {...}` frames); every other request still
 * reaches the e2e backend. Installed before the page loads, again on reload.
 */

declare global {
	interface Window {
		__chatStreamStub?: {
			connected: boolean;
			body: string | null;
			push: (text: string) => void;
			close: () => void;
		};
	}
}

export type StreamEvent = Record<string, unknown> | "[DONE]";

const frame = (event: StreamEvent) =>
	`data: ${event === "[DONE]" ? event : JSON.stringify(event)}\n\n`;

export async function stubChatStream(page: Page) {
	await page.addInitScript(() => {
		const realFetch = window.fetch.bind(window);
		const encoder = new TextEncoder();
		let controller: ReadableStreamDefaultController<Uint8Array> | null = null;
		window.__chatStreamStub = {
			connected: false,
			body: null,
			push: (text) => controller?.enqueue(encoder.encode(text)),
			close: () => controller?.close(),
		};
		window.fetch = (input, init) => {
			const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
			const method = (init?.method ?? (input instanceof Request ? input.method : "GET"))
				.toString()
				.toUpperCase();
			const stub = window.__chatStreamStub;
			if (stub && method === "POST" && /\/api\/v1\/new_chat(\?|$)/.test(url)) {
				stub.body = typeof init?.body === "string" ? init.body : null;
				const body = new ReadableStream<Uint8Array>({
					start(c) {
						controller = c;
						stub.connected = true;
					},
				});
				return Promise.resolve(
					new Response(body, { status: 200, headers: { "Content-Type": "text/event-stream" } })
				);
			}
			return realFetch(input, init);
		};
	});

	return {
		/** The page has sent its question and is reading the stream. */
		whenConnected: () => page.waitForFunction(() => window.__chatStreamStub?.connected === true),
		/** The request body the page sent (JSON). */
		requestBody: async () =>
			JSON.parse((await page.evaluate(() => window.__chatStreamStub?.body)) ?? "{}") as Record<
				string,
				unknown
			>,
		push: (events: readonly StreamEvent[]) =>
			page.evaluate((text) => window.__chatStreamStub?.push(text), events.map(frame).join("")),
		close: () => page.evaluate(() => window.__chatStreamStub?.close()),
	};
}
