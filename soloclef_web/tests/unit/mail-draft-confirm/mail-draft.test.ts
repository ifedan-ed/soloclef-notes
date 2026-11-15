import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { NextIntlClientProvider } from "next-intl";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { isBodyTool } from "@/features/chat-messages/timeline/grouping";
import { mailDraftViewSchema } from "@/features/mail-draft-confirm/api/mail-draft-api";
import {
	DRAFT_EMAIL_TOOL,
	parseDraftCardPayload,
} from "@/features/mail-draft-confirm/lib/draft-card-payload";
import { fileSize } from "@/features/mail-draft-confirm/lib/file-size";
import { sendRefusal } from "@/features/mail-draft-confirm/lib/send-refusal";
import { DraftBody } from "@/features/mail-draft-confirm/ui/draft-body";
import { UnusualConfirm } from "@/features/mail-draft-confirm/ui/unusual-confirm";
import { AppError, NotFoundError } from "@/lib/error";

// Run with: pnpm exec tsx --test tests/unit/mail-draft-confirm/mail-draft.test.ts

const DRAFT_ID = "0b6f9a1c-5d2e-4f3a-8b7c-1d2e3f4a5b6c";

/** A draft as the confirmation route answers it. */
const view = {
	draft: {
		id: DRAFT_ID,
		status: "pending",
		account: { id: "acct-1", label: "Home mail", username: "ada@example.org" },
		from: { name: "Ada Lovelace", email: "ada@example.org" },
		to: [
			{ name: "Ben", email: "ben@example.org" },
			{ name: "", email: "billing@vendor.example" },
		],
		cc: [{ name: "", email: "carol@example.org" }],
		bcc: [{ name: "", email: "hidden@elsewhere.example" }],
		subject: "Re: Invoice 42",
		body: "Hi Ben,\n<b>not bold</b>\nThanks",
		attachments: [],
		reply_to: { subject: "Invoice 42", from: "Ben <ben@example.org>" },
		outside_thread: ["Hidden@Elsewhere.example"],
		created_at: "2026-11-12T10:00:00Z",
		expires_at: "2026-11-13T10:00:00Z",
		sent_at: null,
		failure: null,
		superseded_by: null,
		blocked: null,
	},
	confirmation: {
		token: "tok-once",
		content_hash: "sha256:abc",
		expires_at: "2026-11-12T10:10:00Z",
		unusual: [],
	},
};

test("only the drafted payload with a real draft id makes a card", () => {
	assert.deepEqual(parseDraftCardPayload({ status: "drafted", draft_id: DRAFT_ID }), {
		status: "drafted",
		draft_id: DRAFT_ID,
	});
	assert.equal(parseDraftCardPayload({ status: "drafted", draft_id: "../send" }), null);
	assert.equal(parseDraftCardPayload({ status: "sent", draft_id: DRAFT_ID }), null);
	assert.equal(parseDraftCardPayload(`draft_id=${DRAFT_ID}`), null);
	assert.equal(parseDraftCardPayload(undefined), null);
	// A payload carrying message content is not one the server sends: the card
	// must never draw recipients or a body from the tool's side of things.
	assert.equal(
		parseDraftCardPayload({ status: "drafted", draft_id: DRAFT_ID, to: ["x@evil.example"] }),
		null
	);
});

test("a draft call is a body card only once it has drafted", () => {
	const names = new Set([DRAFT_EMAIL_TOOL]);
	const part = (result: unknown) => ({ type: "tool-call", toolName: DRAFT_EMAIL_TOOL, result });
	assert.equal(isBodyTool(part({ status: "drafted", draft_id: DRAFT_ID }), names), true);
	assert.equal(isBodyTool(part(undefined), names), false);
	assert.equal(isBodyTool(part({ error: "no such mailbox" }), names), false);
});

test("the view keeps the draft's fields and drops anything else", () => {
	const parsed = mailDraftViewSchema.parse({ ...view, model_args: { to: ["x"] } });
	assert.ok(!("model_args" in parsed));
	assert.equal(parsed.draft.bcc[0].email, "hidden@elsewhere.example");
	assert.equal(mailDraftViewSchema.parse({ ...view, confirmation: null }).confirmation, null);
	assert.equal(
		mailDraftViewSchema.safeParse({ ...view, draft: { ...view.draft, status: "queued" } }).success,
		false
	);
});

test("every refusal ends with the draft shown again, never a send", () => {
	const coded = (status: number, code: string, message = "") =>
		new AppError(message, status, "", code);
	assert.deepEqual(sendRefusal(coded(409, "draft_changed")), {
		next: "reconfirm",
		messageKey: "draft_changed",
	});
	assert.equal(sendRefusal(coded(410, "confirmation_expired")).next, "reconfirm");
	assert.equal(sendRefusal(coded(410, "confirmation_spent")).next, "reconfirm");
	assert.equal(sendRefusal(coded(410, "draft_not_pending")).next, "refresh");
	// An unusual send is asked about again, never refused for good: the card
	// is drawn again with the point to tick.
	assert.deepEqual(sendRefusal(coded(428, "unusual_not_confirmed")), {
		next: "reconfirm",
		messageKey: "unusual_not_confirmed",
	});
	assert.equal(sendRefusal(coded(409, "unusual_changed")).next, "reconfirm");
	assert.deepEqual(sendRefusal(coded(502, "send_failed", "Not allowed to send")), {
		next: "refresh",
		messageKey: "send_failed",
		detail: "Not allowed to send",
	});
	assert.equal(sendRefusal(new NotFoundError("Draft not found", 404)).messageKey, "draft_gone");
	assert.equal(sendRefusal(new Error("offline")).next, "stay");
});

test("the card body lists every recipient, flags the outsider, and prints the body as text", () => {
	const messages = JSON.parse(readFileSync(join(process.cwd(), "messages", "en.json"), "utf8"));
	const html = renderToStaticMarkup(
		createElement(NextIntlClientProvider, {
			locale: "en",
			messages,
			timeZone: "UTC",
			// biome-ignore lint/correctness/noChildrenProp: the provider's props type requires children
			children: createElement(DraftBody, {
				view: mailDraftViewSchema.parse(view),
				actionable: true,
			}),
		})
	);
	for (const email of [
		"ben@example.org",
		"billing@vendor.example",
		"carol@example.org",
		"hidden@elsewhere.example",
	]) {
		assert.ok(html.includes(email), `${email} is shown`);
	}
	assert.equal(html.split("not in the original conversation").length - 1, 1);
	assert.ok(html.includes("Hidden from the other recipients"));
	// Escaped, not rendered: a body is never markup.
	assert.ok(html.includes("&lt;b&gt;not bold&lt;/b&gt;"));
	assert.ok(!html.includes("<b>not bold</b>"));
	assert.ok(html.includes("No attachments"));
});

/** Renders a card part with the English messages. */
function rendered(element: ReturnType<typeof createElement>): string {
	const messages = JSON.parse(readFileSync(join(process.cwd(), "messages", "en.json"), "utf8"));
	return renderToStaticMarkup(
		createElement(NextIntlClientProvider, {
			locale: "en",
			messages,
			timeZone: "UTC",
			// biome-ignore lint/correctness/noChildrenProp: the provider's props type requires children
			children: element,
		})
	);
}

test("the card lists every file with its size and where it came from", () => {
	const withFiles = mailDraftViewSchema.parse({
		...view,
		draft: {
			...view.draft,
			attachments: [
				{
					name: "Q3 report.pdf",
					type: "application/pdf",
					size: 2_621_440,
					origin: "Document: Quarterly report",
				},
				{
					name: "notes.txt",
					type: "text/plain",
					size: 300,
					origin: 'Attachment of "Notes" from Carol',
				},
			],
		},
	});
	const html = rendered(createElement(DraftBody, { view: withFiles, actionable: true }));
	assert.ok(html.includes("2 attachments"));
	assert.ok(
		html.includes("Q3 report.pdf") &&
			html.includes("2.5 MB") &&
			html.includes("Document: Quarterly report")
	);
	// A small file is a kilobyte at least, never "0.0 MB".
	assert.ok(html.includes("notes.txt") && html.includes("1 KB"));
	assert.ok(!html.includes("No attachments"));
	assert.deepEqual(fileSize(10 * 1024 * 1024 + 1), { key: "size_mb", value: "10.0" });
});

test("each unusual point is worded with its own box to tick", () => {
	const confirmation = mailDraftViewSchema.parse({
		...view,
		confirmation: {
			...view.confirmation,
			unusual: [
				{ code: "many_recipients", message: "m", count: 34, limit: 20 },
				{ code: "new_recipients", message: "m", count: 1, addresses: ["carol@example.org"] },
				{
					code: "large_attachments",
					message: "m",
					bytes: 12 * 1024 * 1024,
					limit: 10 * 1024 * 1024,
				},
				{ code: "a_point_from_a_newer_server", message: "The server's own sentence." },
			],
		},
	}).confirmation;
	assert.ok(confirmation);
	const html = rendered(
		createElement(UnusualConfirm, {
			points: confirmation.unusual,
			ticked: new Set(["many_recipients"]),
			onToggle: () => {},
			disabled: false,
		})
	);
	assert.ok(html.includes("It goes to 34 recipients. More than 20 at once"));
	assert.ok(html.includes("never emailed this address") && html.includes("carol@example.org"));
	assert.ok(html.includes("12.0 MB together"));
	assert.ok(html.includes("The server&#x27;s own sentence."));
	// One box per point; the one ticked is shown ticked.
	assert.equal(html.split('role="checkbox"').length - 1, 4);
	assert.equal(html.split('aria-checked="true"').length - 1, 1);
});
