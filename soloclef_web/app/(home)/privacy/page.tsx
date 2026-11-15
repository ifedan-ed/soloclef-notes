import type { Metadata } from "next";
import Link from "next/link";
import { LegalPage } from "@/components/site/legal-page";
import { BRAND, brandTitle, OG_IMAGES, siteUrl } from "@/lib/brand";

/**
 * Privacy policy. Every statement was checked against this deployment: no
 * analytics key is configured (PostHog stays off without one), web sign-in is
 * a pair of httpOnly cookies, no model runs on the server (every model call,
 * embeddings included, goes out through the model proxy), and the named
 * outside services are the ones the code calls. Change this page in the same
 * commit as any change to those facts.
 */

const TITLE = brandTitle("Privacy");
const DESCRIPTION = `What ${BRAND.name} stores, what it sends to other services, and what it does not do.`;
const UPDATED = "13 November 2026";

export const metadata: Metadata = {
	title: TITLE,
	description: DESCRIPTION,
	alternates: { canonical: siteUrl("/privacy") },
	openGraph: {
		title: TITLE,
		description: DESCRIPTION,
		url: siteUrl("/privacy"),
		siteName: BRAND.name,
		images: OG_IMAGES,
	},
};

export default function PrivacyPage() {
	return (
		<LegalPage
			title="Privacy"
			updated={UPDATED}
			lede={`${BRAND.name} runs on one privately operated server. This page says what is kept there, what is sent elsewhere and why.`}
		>
			<h2>What is stored</h2>
			<ul>
				<li>
					<strong>Your account:</strong> email address and a hashed password.
				</li>
				<li>
					<strong>Your work:</strong> notebooks, the files and links you add, the text extracted
					from them, chats, notes and Studio outputs.
				</li>
				<li>
					<strong>Connections you set up:</strong> for example a Nextcloud address and app password,
					or a search provider key. The Nextcloud app password and sign-in tokens for connected
					services are stored encrypted.
				</li>
				<li>
					<strong>Logs:</strong> the server logs requests and errors for troubleshooting, with
					secrets removed. If the app crashes in your browser, it sends an error report without your
					documents.
				</li>
			</ul>

			<h2>What is sent to other services</h2>
			<p>
				Some features need an outside service. Only what that feature needs is sent, and only when
				you use it.
			</p>
			<ul>
				<li>
					<strong>AI models.</strong> To answer a question or build a Studio output, your question
					and the relevant passages from your sources go to the language model provider you chose in
					Settings. Audio and video narration is spoken by a text-to-speech model. If you connect
					your own model API, those models are called directly at that address with your key.
				</li>
				<li>
					<strong>Hosted parsers.</strong> If you choose Datalab, LlamaParse or Unstructured for a
					file, it is uploaded to that service to be read, and to no other. The Local parser reads
					it on the server instead.
				</li>
				<li>
					<strong>Image understanding.</strong> If you turn on the vision model, images from your
					files are sent to that model to be described.
				</li>
				<li>
					<strong>Research and web answers.</strong> Searches send your query to the provider you
					picked (PubMed, arXiv, medRxiv / bioRxiv, Exa or Tavily).
				</li>
				<li>
					<strong>Your mail, files and connected apps.</strong> If you connect a mailbox, Google
					Drive, Nextcloud or an MCP server, the assistant reads from it when a question needs it,
					and what it reads goes to the language model like any passage. A call to an MCP server
					sends that server the request the assistant makes. Mail is read live and not stored; a
					draft is kept until you send or discard it. An answer that used these is never shown on a
					notebook's public link.
				</li>
			</ul>
			<p>
				The search index is kept on the server, but no model runs there: to index a document, its
				text is sent in passages to an embedding model, through the same model service as the
				chat.
			</p>

			<h2>What is not done</h2>
			<ul>
				<li>No advertising, and your data is not sold or shared for marketing.</li>
				<li>No analytics or tracking scripts are loaded.</li>
				<li>Your documents are not used to train models by this service.</li>
			</ul>

			<h2>Cookies and browser storage</h2>
			<p>
				Signing in sets two cookies that keep you signed in. Your theme, language and a few layout
				choices are kept in your browser&apos;s local storage. There are no third-party cookies.
			</p>

			<h2>Deleting your data</h2>
			<p>
				You can delete sources, outputs, notes and whole notebooks in the app at any time. To close
				your account and remove everything, write to the address on the{" "}
				<Link href="/contact">contact page</Link>. Server backups may keep copies for a while after
				deletion.
			</p>

			<h2>Questions</h2>
			<p>
				Ask anything about this policy at the address on the{" "}
				<Link href="/contact">contact page</Link>.
			</p>
		</LegalPage>
	);
}
