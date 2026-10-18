import type { Metadata } from "next";
import Link from "next/link";
import { LegalPage } from "@/components/site/legal-page";
import { BRAND, brandTitle, OG_IMAGES, siteUrl } from "@/lib/brand";

/**
 * Privacy policy. Every statement was checked against this deployment: no
 * analytics key is configured (PostHog stays off without one), web sign-in is
 * a pair of httpOnly cookies, embeddings run on the server, and the named
 * outside services are the ones the code calls. Change this page in the same
 * commit as any change to those facts.
 */

const TITLE = brandTitle("Privacy");
const DESCRIPTION = `What ${BRAND.name} stores, what it sends to other services, and what it does not do.`;
const UPDATED = "25 September 2026";

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
					and the relevant passages from your sources go to the language model provider configured
					for the workspace. Audio and video narration is spoken by a text-to-speech model.
				</li>
				<li>
					<strong>Cloud parsing.</strong> If you choose the Cloud parser, the file is uploaded to
					Datalab to be read, and with the vision model on, the images inside it go there too. A few
					file types Datalab can't open, and any file it fails to read, go to LlamaCloud. The Local
					parser reads it on the server instead.
				</li>
				<li>
					<strong>Image understanding.</strong> If you turn on the vision model, images from your
					files are sent to that model to be described.
				</li>
				<li>
					<strong>Research and web answers.</strong> Searches send your query to the provider you
					picked (for example PubMed, arXiv, Google Scholar, Exa or Tavily). General web searches go
					through a private SearXNG instance.
				</li>
			</ul>
			<p>
				The search index is built on the server with a BGE-M3 embedding model, so indexing your
				documents normally sends them nowhere. If that local service is down, the same model on
				OpenRouter is used as a fallback, and the text being indexed is sent there.
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

			<h2>Payments</h2>
			<p>
				Payments are made in cryptocurrency through our own BTCPay Server (once payments open): it
				records the invoice amount and whether it was paid, and the payment goes straight to our
				wallets on the blockchain, so no bank or card details are involved. Card payments are not
				offered; if they are added, this page will say how they are handled before they open.
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
