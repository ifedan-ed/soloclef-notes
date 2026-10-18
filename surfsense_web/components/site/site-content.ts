/**
 * The public site's links and copy, in one module.
 *
 * Every sentence here describes the app as it runs on this server today; check
 * the code before adding a claim. The copy is kept apart from the components
 * so it can move into an admin-edited store without touching the layout: the
 * app has no site-content table yet, so for now it ships in the bundle.
 *
 * Prices are not written here. They come from the billing catalog
 * (`lib/pricing/catalog.ts`), which the pricing page and the docs share.
 */
import { BRAND } from "@/lib/brand";
import { SIGNUPS_OPEN } from "@/lib/signups";

export type SiteLink = { name: string; href: string };

export { REQUEST_ACCESS_HREF, SIGNUPS_OPEN } from "@/lib/signups";

export const NAV_LINKS: SiteLink[] = [
	{ name: "Features", href: "/features" },
	{ name: "Pricing", href: "/pricing" },
	{ name: "Docs", href: "/docs" },
	{ name: "Contact", href: "/contact" },
];

export const FOOTER_COLUMNS: { heading: string; links: SiteLink[] }[] = [
	{
		heading: "Product",
		links: [
			{ name: "Features", href: "/features" },
			{ name: "Pricing", href: "/pricing" },
			{ name: "Phone app", href: "/docs/mobile-app" },
		],
	},
	{
		heading: "Help",
		links: [
			{ name: "Docs", href: "/docs" },
			{ name: "Getting started", href: "/docs/getting-started" },
			{ name: "Contact", href: "/contact" },
		],
	},
	{
		heading: "Legal",
		links: [
			{ name: "Privacy", href: "/privacy" },
			{ name: "Terms", href: "/terms" },
		],
	},
];

export type Advantage = { key: string; title: string; body: string; href: string };

/** The landing page's reasons, each backed by something in the app. */
export const ADVANTAGES: Advantage[] = [
	{
		key: "grounded",
		title: "Answers from your sources, with citations",
		body: "Each notebook has one chat, and it answers only from the sources you switched on. Every claim carries a numbered citation that opens the passage it came from.",
		href: "/docs/chat-and-citations",
	},
	{
		key: "studio",
		title: "Studio turns sources into something new",
		body: "Reports with a table of contents, mind maps, infographics, flashcards, quizzes, slides, data tables, and audio or video overviews, all built from the same notebook.",
		href: "/docs/studio",
	},
	{
		key: "research",
		title: "Research that lands in the notebook",
		body: "Search PubMed, arXiv, medRxiv and bioRxiv, Google Scholar, Google and the open web, or Exa and Tavily, and import the results you pick as sources.",
		href: "/docs/notebooks-and-sources",
	},
	{
		key: "parsing",
		title: "Local or Cloud parsing, per upload",
		body: "Read PDFs and Office files on the server with the Local parser, or send hard ones such as scans and complex layouts to the Cloud parser. An optional vision model describes images and figures.",
		href: "/docs/notebooks-and-sources",
	},
	{
		key: "nextcloud",
		title: "Your files from Nextcloud",
		body: "Pick files and folders straight from a connected Nextcloud and add them to a notebook, with the same parser and vision choices as an upload.",
		href: "/docs/notebooks-and-sources",
	},
	{
		key: "phone",
		title: "A phone app",
		body: "The Android app covers notebooks, sources, chat and Studio, with audio overviews that keep playing in the background. An iOS build uses the same code.",
		href: "/docs/mobile-app",
	},
];

export type StudioOutput = { key: string; name: string; body: string };

export const STUDIO_OUTPUTS: StudioOutput[] = [
	{
		key: "report",
		name: "Report",
		body: "A PDF or Word report, or an interactive document with a table of contents and suggestions for each section.",
	},
	{
		key: "mindmap",
		name: "Mind map",
		body: "The notebook's ideas as a branching map you can expand, collapse and download.",
	},
	{
		key: "infographic",
		name: "Infographic",
		body: "One image that summarises the sources. Choose landscape, portrait or square, the level of detail, and one of ten styles or let it choose.",
	},
	{
		key: "flashcards",
		name: "Flashcards",
		body: "Cards to study from. Ask for an explanation of any card and it opens in the notebook's chat.",
	},
	{
		key: "quiz",
		name: "Quiz",
		body: "Multiple-choice questions with the reasoning behind every option, right or wrong.",
	},
	{
		key: "slides",
		name: "Slides",
		body: "A presentation deck drafted from your sources, ready to download.",
	},
	{
		key: "table",
		name: "Data table",
		body: "Facts pulled out of the sources into rows and columns, downloadable as a spreadsheet.",
	},
	{
		key: "audio",
		name: "Audio overview",
		body: "A deep dive, brief, critique or debate about your sources, spoken by natural voices, with a transcript.",
	},
	{
		key: "video",
		name: "Video overview",
		body: "A narrated, animated video in a visual style you choose, up to ten minutes long.",
	},
];

export type PrivacyPoint = { title: string; body: string };

/**
 * What stays on the server and what leaves it. The second half matters as much
 * as the first: a privacy claim that hides the model provider is not honest.
 */
export const PRIVACY_POINTS: PrivacyPoint[] = [
	{
		title: "Hosted on one private server",
		body: `${BRAND.name} runs on its owner's own server, not on a shared cloud platform. Your files, notes, chats and outputs are stored there.`,
	},
	{
		title: "Search index built on the server",
		body: "Documents are split and embedded with a BGE-M3 model running on the server. Only if that service is down does the same model on OpenRouter step in.",
	},
	{
		title: "What does leave the server",
		body: "To answer a question or build a Studio output, the relevant passages are sent to the language model provider. The Cloud parser uploads the file to Datalab (or to LlamaCloud for a few file types Datalab can't open, or if Datalab fails), and research searches send your query to the search provider you picked. The Local parser keeps the file on the server.",
	},
];

export type Faq = { question: string; answer: string };

export const HOME_FAQ: Faq[] = [
	{
		question: `What is ${BRAND.name}?`,
		answer: `A research notebook. You add sources to a notebook, ask questions about them, and turn them into study and presentation material. It works like NotebookLM and runs on its own server.`,
	},
	{
		question: "Does the chat answer from the internet?",
		answer:
			"No. A notebook's chat answers from that notebook's enabled sources only. You can pick a web search from the Tools menu for a single message, and those results are marked Web. To keep something from the web, import it as a source with Search the web for new sources.",
	},
	{
		question: "Which files can I add?",
		answer:
			"PDFs, Word, PowerPoint and Excel files, text and Markdown, images, audio and video, web pages and YouTube links, and files from Nextcloud.",
	},
	{
		question: "Can I sign up?",
		answer: SIGNUPS_OPEN
			? "Yes. Create an account and you start on the Free plan."
			: "Not yet: new accounts are not open. You can request access from the contact page, and if you already have an account, sign in.",
	},
	{
		question: "Is there an app?",
		answer:
			"Yes. The Android app covers notebooks, sources, chat, Studio and the output viewers, and an iOS build is made from the same code. See the phone app page in the docs for how to install it.",
	},
];

export type FeatureGroup = {
	key: string;
	eyebrow: string;
	title: string;
	intro: string;
	points: { title: string; body: string }[];
	docs: string;
};

/** The features page, one group per part of the app. */
export const FEATURE_GROUPS: FeatureGroup[] = [
	{
		key: "sources",
		eyebrow: "Sources",
		title: "Bring in what you are reading",
		intro:
			"A notebook holds its own sources. Switch any of them off to leave it out of answers and outputs.",
		points: [
			{
				title: "Upload or drop files",
				body: "Drag files onto a notebook or pick them from your device: PDFs, Word, PowerPoint, Excel, text and Markdown, images, audio and video.",
			},
			{
				title: "Local or Cloud parser",
				body: "Choose per upload. Local reads the file on the server with PyMuPDF4LLM. Cloud sends it to Datalab, which handles scans, tables and complex layouts better; LlamaCloud reads the few file types Datalab can't open, and steps in if Datalab fails.",
			},
			{
				title: "Optional vision model",
				body: "Turn on image understanding and a vision model describes pictures, charts and figures so they can be searched and cited.",
			},
			{
				title: "Nextcloud",
				body: "Browse a connected Nextcloud and add files or whole folders, with the same parser and vision choices.",
			},
			{
				title: "Research",
				body: "Search the web (through a private SearXNG), Google, Exa or Tavily, papers on PubMed, arXiv, medRxiv and bioRxiv or Google Scholar, and platforms such as YouTube and Reddit. Import the results you choose.",
			},
			{
				title: "Links and videos",
				body: "Paste a web page or a YouTube link and it is imported as a source, transcript included.",
			},
		],
		docs: "/docs/notebooks-and-sources",
	},
	{
		key: "chat",
		eyebrow: "Chat",
		title: "Ask, and see where the answer came from",
		intro:
			"Each notebook has one chat. It keeps the conversation with the notebook, so you can come back to it.",
		points: [
			{
				title: "Grounded in the notebook",
				body: "Answers come from the notebook's enabled sources only. To look something up on the web for one message, pick a web search from the Tools menu; those results are marked Web, never as sources.",
			},
			{
				title: "Citations you can open",
				body: "Numbered citations open the exact passage in the source, so you can check a claim in one click.",
			},
			{
				title: "Point at one source",
				body: "Type @ to narrow a question to one of the notebook's sources.",
			},
			{
				title: "Your choice of model",
				body: "Each workspace picks its chat and Studio models in its settings.",
			},
		],
		docs: "/docs/chat-and-citations",
	},
	{
		key: "studio",
		eyebrow: "Studio",
		title: "Make something from your sources",
		intro:
			"Studio builds outputs from all of a notebook's sources or the ones you pick, with a focus you can describe in your own words.",
		points: [
			{
				title: "Reports",
				body: "Download a PDF or Word report, or open an interactive one with a table of contents and suggestions for each section. Select a passage and ask the chat to explain it.",
			},
			{
				title: "Mind maps and infographics",
				body: "A mind map you can expand and download, and infographics in landscape, portrait or square, three levels of detail and ten styles.",
			},
			{
				title: "Flashcards and quizzes",
				body: "Choose how many and how hard. Quizzes explain why each option is right or wrong, and Explain continues any question in the notebook's chat.",
			},
			{
				title: "Slides and data tables",
				body: "A presentation deck and a table of extracted facts, both downloadable.",
			},
			{
				title: "Audio overviews",
				body: "Deep dive, brief, critique or debate, short to long, spoken with natural voices, with a transcript.",
			},
			{
				title: "Video overviews",
				body: "A narrated, animated video in a visual style you pick, up to ten minutes.",
			},
		],
		docs: "/docs/studio",
	},
	{
		key: "organise",
		eyebrow: "Organise",
		title: "Notes, search and tidy notebooks",
		intro: "The small things that keep a lot of notebooks usable.",
		points: [
			{
				title: "Notes",
				body: "Write notes beside the sources and outputs in a notebook's Studio panel.",
			},
			{
				title: "Search inside notebooks",
				body: "The home search looks through what notebooks contain, not only their titles.",
			},
			{
				title: "Notebooks name themselves",
				body: "An untitled notebook takes a name from its first source or question. Rename it any time.",
			},
			{
				title: "Phone app",
				body: "The Android app has notebooks, sources, chat, Studio and the output viewers; an iOS build uses the same code.",
			},
		],
		docs: "/docs/notes",
	},
];
