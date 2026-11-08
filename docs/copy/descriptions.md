# Soloclef Notes — descriptions to post

Two audiences, two different pitches. The self-hosted one sells the server;
the App Store one sells the app and must be honest that the app needs a
server you run.

---

## 1. Self-hosted: the post

For r/selfhosted, Hacker News, the GitHub repo and soloclef.com.

### GitHub "About" (350 characters max — this is 232)

> A self-hosted, NotebookLM-style workspace for your own documents. Add PDFs, mail, web pages and notes; ask questions and get answers that cite the exact passage; generate podcasts, videos, mind maps, quizzes and reports. Apache-2.0.

Topics: `self-hosted` `notebooklm` `rag` `pdf` `documents` `ai` `docker`
`fastapi` `nextjs` `pgvector` `mcp` `research`

### The post

**Title:** Soloclef Notes — a self-hosted NotebookLM, with an iPhone app and an MCP server

I wanted NotebookLM without handing Google my documents, so I built one that
runs on my own box. It is a fork of SurfSense, Apache-2.0, and it has been my
daily driver for months.

**What it does**

- **Every kind of source.** PDFs including scans, Word, PowerPoint, Excel,
  images, web pages. Pull files straight from Google Drive or Nextcloud. The
  default parser is local PyMuPDF4LLM — OCR only on the pages that need it,
  and the file never leaves your server.
- **Answers that cite.** One chat per notebook, answering only from the
  sources you tick, and every claim links back to the passage it came from.
- **Research that imports.** Search the web, PubMed, arXiv, medRxiv, Google
  Scholar, YouTube with transcripts, and Reddit, and add what you find in one
  tap. SearXNG is bundled, so web search works with no third-party key.
- **Studio.** Reports, mind maps, infographics, flashcards, quizzes, slide
  decks, data tables — and podcast-style audio overviews and narrated slide
  videos you can play in the background on your phone.
- **Your mail, read-only.** Connect any IMAP mailbox, Gmail or Microsoft, and
  ask questions about your mail and its attachments. Nothing is sent, moved
  or deleted.
- **Automations.** Scheduled tasks: a weekly digest of a notebook, a morning
  round-up of new papers on a search. Saved as notes, and emailed to you.
- **Workspaces.** Invite people with roles; share a chat or an output.
- **MCP server included**, so your coding agent can read your notebooks.
- **iPhone app** on the App Store, which points at your server.

**Bring your own model.** Any OpenAI-compatible endpoint: OpenAI, OpenRouter,
LiteLLM, Ollama, vLLM. Embeddings default to a small model that runs on your
server. With a local model nothing leaves the machine at all.

**No hosted service, no trial, no codes, nothing to buy.** `BILLING_MODE=off`
and nobody on your server is ever limited. Sign-in is a local email and
password by default; OIDC is there if you want it, and it is optional.

**What it takes:** Docker Engine 24+ with Compose v2 and about 25 GB of disk.
The first build is large — the sandbox image carries LibreOffice, Remotion and
a PDF stack — and later builds use the cache.

```
git clone https://github.com/ifedan-ed/soloclef-notes.git soloclef
cd soloclef && cp docker/.env.example docker/.env
# set SECRET_KEY, DB_PASSWORD, SURFSENSE_PUBLIC_URL, BILLING_ADMIN_EMAILS
cd docker && docker compose up -d --build
```

Then <http://localhost:3929>.

Docs: <https://ifedan-ed.github.io/soloclef-notes/> · Site:
<https://soloclef.com> · Licence: Apache-2.0, built on
[SurfSense](https://github.com/MODSetter/SurfSense).

---

## 2. App Store

The app talks to a Soloclef server. It cannot be used without one, and the
listing says so in the first screen of text rather than burying it — both
because it is true and because a reviewer who discovers it later rejects the
build.

### Name (30 max)

> Soloclef

### Subtitle (30 max — this is 27)

> Your own private NotebookLM

### Promotional text (170 max — this is 152)

> Ask your own documents anything and get answers that cite the exact passage.
> Podcasts, mind maps and quizzes from your sources. Runs on your own server.

### Keywords (100 max, comma separated, no spaces — this is 96)

> notebook,research,documents,pdf,notes,citations,podcast,mindmap,flashcards,quiz,study,selfhosted

### Description

> **Soloclef connects to your own Soloclef server.** It is not a hosted
> service and there is nothing to buy. If you do not run a server yet, the
> free, open-source software is at soloclef.com.
>
> Soloclef is a private, self-hosted workspace for your own documents —
> everything a research notebook should do, on a machine you control.
>
> **Ask your documents anything**
> Add PDFs (scans included), Word, PowerPoint, Excel, images and web pages.
> Each notebook has a chat that answers only from the sources you tick, and
> every claim links to the exact passage it came from, so you can check it.
>
> **Listen on the move**
> Turn a notebook into a podcast-style audio overview or a narrated slide
> video, and play it in the background — on the bus, in the gym, anywhere you
> would not read a PDF.
>
> **Studio**
> Reports, mind maps, infographics, flashcards, quizzes, slide decks and data
> tables, from the sources you choose, in two taps.
>
> **Research that imports**
> Search the web, PubMed, arXiv, medRxiv, Google Scholar, YouTube with
> transcripts and Reddit, and add what you find to a notebook in one tap.
>
> **Your mail, read-only**
> Connect a mailbox and ask questions about your mail and its attachments.
> Nothing is sent, moved or deleted.
>
> **Automations**
> A weekly digest of a notebook, or a morning round-up of new papers on a
> search, saved as a note and emailed to you.
>
> **Share it**
> Workspaces with roles, so a team or a study group works from the same
> sources.
>
> **Your documents stay yours**
> They live on your server, not ours. Only what a question needs is sent to
> the model you chose, and if you run a local model nothing leaves your
> machine at all. There is no Soloclef account, because there is no Soloclef
> cloud.
>
> **Getting started**
> On the sign-in screen, tap Change server and enter your server's https
> address. The open-source server is Apache-2.0 and runs on Docker: see
> soloclef.com.

### What a reviewer will ask

They need a working account. Give App Review the demo server address and a
test account in the review notes, and say in the notes that the app is a
client for self-hosted software — the same sentence that opens the
description.
