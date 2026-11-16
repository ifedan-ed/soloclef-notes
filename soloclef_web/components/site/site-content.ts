/**
 * The public site's links and copy, in one module.
 *
 * Every sentence here describes the app as it runs on this server today; check
 * the code, and the README's "What it does" table, before adding a claim. The copy is kept apart from the components
 * so it can move into an admin-edited store without touching the layout: the
 * app has no site-content table yet, so for now it ships in the bundle.
 */
import { BRAND } from "@/lib/brand";
import { SIGNUPS_OPEN } from "@/lib/signups";

export type SiteLink = { name: string; href: string };

/** The contact page: a dead end on a server that publishes no address. */
export const CONTACT_HREF = "/contact";

export { REQUEST_ACCESS_HREF, SIGNUPS_OPEN } from "@/lib/signups";

export const NAV_LINKS: SiteLink[] = [
	{ name: "Features", href: "/features" },
	{ name: "Docs", href: "/docs" },
	{ name: "Contact", href: CONTACT_HREF },
];

export const FOOTER_COLUMNS: { heading: string; links: SiteLink[] }[] = [
	{
		heading: "Product",
		links: [
			{ name: "Features", href: "/features" },
			{ name: "Phone app", href: "/docs/mobile-app" },
		],
	},
	{
		heading: "Help",
		links: [
			{ name: "Docs", href: "/docs" },
			{ name: "Getting started", href: "/docs/getting-started" },
			{ name: "Contact", href: CONTACT_HREF },
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

/**
 * The links to show: all of them, or all but Contact when the server publishes
 * no contact address (useSupportEmail), since a contact page with nobody to
 * contact is a dead end. Privacy and Terms keep their own in-text link: they
 * must say where to write, and the page itself says when there is no one.
 */
export function shownLinks(links: SiteLink[], hasContact: boolean): SiteLink[] {
	return hasContact ? links : links.filter((link) => link.href !== CONTACT_HREF);
}

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
		body: "Search PubMed, arXiv and medRxiv / bioRxiv, or the web through Exa and Tavily with your own key, and import the results you pick as sources. A paper or link that is a PDF is stored and read like an upload.",
		href: "/docs/notebooks-and-sources",
	},
	{
		key: "parsing",
		title: "Your choice of parser, per source",
		body: "Read PDFs and Office files on the server with the Local parser, or send hard ones such as scans and complex layouts to Datalab, LlamaParse or Unstructured. Each upload, import and connection names its parser, and only that parser reads it.",
		href: "/docs/notebooks-and-sources",
	},
	{
		key: "drives",
		title: "Your files from Google Drive and Nextcloud",
		body: "Pick files and folders from a connected Google Drive or Nextcloud and add them to a notebook, with the same parser and vision choices as an upload.",
		href: "/docs/notebooks-and-sources",
	},
	{
		key: "phone",
		title: "An iPhone app",
		body: "The iPhone app, on the App Store, has notebooks, sources, chat, Studio, notes, automations and the inbox, with audio overviews that keep playing in the background.",
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
		body: "Single-answer questions with the reasoning behind every option, right or wrong.",
	},
	{
		key: "slides",
		name: "Slides",
		body: "An editable PowerPoint deck drafted from your sources, ready to download.",
	},
	{
		key: "table",
		name: "Data table",
		body: "Facts pulled out of the sources into rows and columns, downloadable as a spreadsheet.",
	},
	{
		key: "audio",
		name: "Audio overview",
		body: "A two-host podcast about your sources: a deep dive, brief, critique or debate, with a transcript.",
	},
	{
		key: "video",
		name: "Video overview",
		body: "A narrated, animated explainer in a visual style you choose, or a short of about a minute.",
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
		title: "Search index kept on the server",
		body: "Documents are split into passages and indexed on the server, by meaning and by keyword. No model runs on the server itself: to index a passage, its text is sent to an embedding model.",
	},
	{
		title: "What does leave the server",
		body: "Every model call (answers, Studio outputs, embeddings, image descriptions, speech) sends the passages it needs to the model provider. A model API you connect yourself is called directly with your key. A hosted parser uploads the file to the service you chose (Datalab, LlamaParse or Unstructured) and to no other, and research searches send your query to the search provider you picked. The Local parser keeps the file on the server.",
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
			"Not on its own. A notebook's chat answers from that notebook's enabled sources. With your own Exa or Tavily key, you can pick a web search from the Tools menu for a single message. If you connect your mail, Google Drive, Nextcloud or an MCP server, the chat can use them when you ask about them. To keep something from the web, import it as a source with Search the web for new sources.",
	},
	{
		question: "Which files can I add?",
		answer:
			"PDF, Word, PowerPoint and EPUB files, text, Markdown, CSV and HTML, and pictures. You can also import web pages, links to PDFs, papers from PubMed, arXiv and medRxiv / bioRxiv, and files from Google Drive and Nextcloud.",
	},
	{
		question: "Can I sign up?",
		answer: SIGNUPS_OPEN
			? "Yes. Create an account from the sign-up page."
			: "Not yet: new accounts are by invitation. If you already have an account, sign in.",
	},
	{
		question: "Is there an app?",
		answer:
			"Yes, for iPhone, on the App Store. It has notebooks, sources, chat, Studio, notes, automations and the inbox, and stays signed in. See the phone app page in the docs.",
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
				body: "Drag files onto a notebook or pick them from your device: PDF, Word, PowerPoint and EPUB files, text, Markdown, CSV and HTML, and pictures.",
			},
			{
				title: "Four parsers to choose from",
				body: "Choose per upload, import or connection. Local reads the file on the server with PyMuPDF4LLM. Datalab, LlamaParse and Unstructured are hosted parsers that handle scans, tables and complex layouts better. A file is read by the parser you chose and by no other.",
			},
			{
				title: "Optional vision model",
				body: "Turn on image understanding and a vision model describes pictures, charts and figures so they can be searched and cited.",
			},
			{
				title: "Google Drive and Nextcloud",
				body: "Browse a connected Google Drive or Nextcloud and add files or whole folders, with the same parser and vision choices.",
			},
			{
				title: "Research",
				body: "Search the web through Exa or Tavily with your own key, or papers on PubMed, arXiv, medRxiv and bioRxiv. Import the results you choose; arXiv papers are read from their PDF.",
			},
			{
				title: "Web pages and PDF links",
				body: "Paste a web page and it is imported as a source. A link to a PDF is stored as the file and read like an upload.",
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
				body: "Citations are numbered 1, 2, 3 in the order they appear in each answer, and a click opens the passage or page they point to.",
			},
			{
				title: "Point at one source",
				body: "Type @ to narrow a question to one of the notebook's sources.",
			},
			{
				title: "Your choice of model",
				body: "Choose from the server's models, or connect your own OpenAI-compatible API with your own key (OpenAI, Anthropic, Gemini, OpenRouter, Requesty) and use its models too.",
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
				body: "An editable PowerPoint deck, and a workbook of facts pulled from the sources, both downloadable.",
			},
			{
				title: "Audio overviews",
				body: "A two-host podcast: deep dive, brief, critique or debate, short to long, with a transcript.",
			},
			{
				title: "Video overviews",
				body: "A narrated, animated explainer in a visual style you pick, or a short of about a minute.",
			},
		],
		docs: "/docs/studio",
	},
	{
		key: "automations",
		eyebrow: "Automations",
		title: "Tasks that run without you",
		intro:
			"An automation is a task the assistant runs on a schedule or when something happens, with the chat's own tools.",
		points: [
			{
				title: "On a schedule or an event",
				body: "Results are saved as notes, and can be emailed to you as well.",
			},
			{
				title: "Your mail, in the chat and in automations",
				body: "Connect an IMAP, Microsoft or Stalwart mailbox. A notebook's chat and your automations search and read mail and its PDF, Word and PowerPoint attachments live, and cite each email they use; nothing is copied in.",
			},
			{
				title: "Drafts you send yourself",
				body: "The assistant can only draft. A draft goes out when you press Send on its card, and an unusual send, such as many recipients or new addresses, asks you to confirm each point.",
			},
			{
				title: "Notifications",
				body: "An inbox entry and a phone push when an output is ready, an import fails, a run ends or a draft is waiting, with quiet hours in your own time zone.",
			},
			{
				title: "Your MCP servers",
				body: "Add an MCP server with an API key, a token or its own sign-in, and its tools join the assistant's, in a notebook's chat and in automations. Switch any of them off from the chat's Tools menu. Secrets are stored encrypted.",
			},
			{
				title: "Use it from other agents",
				body: `Claude Code, Cursor and other agents can search and read your notebooks and notes through ${BRAND.name}'s MCP server.`,
			},
		],
		docs: "/docs/automations",
	},
	{
		key: "organise",
		eyebrow: "Organise",
		title: "Notes, search and sharing",
		intro: "The small things that keep a lot of notebooks usable.",
		points: [
			{
				title: "Notes",
				body: "Save an answer as a note, write your own, and turn a note into a source.",
			},
			{
				title: "Search inside notebooks",
				body: "One search, by meaning and by keyword, over a notebook's sources and notes. The chat uses the same search.",
			},
			{
				title: "Notebooks name themselves",
				body: "An untitled notebook takes a name from its first source or question. Rename it any time.",
			},
			{
				title: "Copy an answer",
				body: "Copy keeps headings, lists, tables and maths, with [1], [2] citations and a list of sources at the end. Or download it as Markdown.",
			},
			{
				title: "Public links",
				body: "Share a read-only link to a chat, a Studio output or a whole notebook.",
			},
			{
				title: "iPhone app",
				body: "The iPhone app on the App Store has notebooks, sources, chat, Studio, notes, automations and the inbox.",
			},
		],
		docs: "/docs/notes",
	},
];
