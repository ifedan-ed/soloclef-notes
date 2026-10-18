# Documentation

The user guide and self-hosting reference are written as MDX pages in
[`surfsense_web/content/docs`](../surfsense_web/content/docs). A running
server serves the same pages at `/docs` (for example
`http://localhost:3929/docs/self-hosting`).

## Start here

| Page | What it covers |
| --- | --- |
| [Getting started](../surfsense_web/content/docs/getting-started.mdx) | Sign in, create a notebook, add a source, ask a question, make a Studio output |
| [Notebooks and sources](../surfsense_web/content/docs/notebooks-and-sources.mdx) | Uploads, PDFs, web pages, connectors, MCP servers |
| [Chat and citations](../surfsense_web/content/docs/chat-and-citations.mdx) | Answers, citations, the Tools menu, model choice |
| [Studio](../surfsense_web/content/docs/studio.mdx) | Reports, slides, audio/video, mind maps, quizzes, flashcards, tables |
| [Search](../surfsense_web/content/docs/search.mdx) | Notebook search and web research |
| [Workspaces and sharing](../surfsense_web/content/docs/workspaces-and-sharing.mdx) | Members, roles, invites, public links |
| [Plans and credits](../surfsense_web/content/docs/plans-and-credits.mdx) | What credits mean (optional for self-hosting) |
| [Privacy and data](../surfsense_web/content/docs/privacy-and-data.mdx) | What leaves the server and when |
| [Self-hosting](../surfsense_web/content/docs/self-hosting.mdx) | Every server setting: address, sign-in, SMTP, parsing, embeddings, reranking, phone app, billing |
| [Connect your agent](../surfsense_web/content/docs/connect-your-agent.mdx) | The MCP server for Claude, ChatGPT, Cursor and other agents |
| [Mobile app](../surfsense_web/content/docs/mobile-app.mdx) | Using the iPhone/Android app with your server |
| [Automations](../surfsense_web/content/docs/automations.mdx) | Scheduled agent runs |

## Self-hosting quick answers

- **No billing:** set `BILLING_MODE=off` in `docker/.env` (or leave the default
  `shadow`, which records but never blocks). Administrators listed in
  `BILLING_ADMIN_EMAILS` are exempt.
- **No OIDC:** the default is a local email/password account. Create one with
  `create_account.py --set-password`; OIDC is optional.
- **PDFs:** the default parser is Local PyMuPDF4LLM. No key is needed.
- **Phone:** the beta is at <https://testflight.apple.com/join/1tjGWu1Q>, or
  build `surfsense_mobile` and enter your server at sign-in.

## Building the docs

The documentation is part of the Next.js app. Rebuild the frontend after
changing these files:

```bash
cd docker
docker compose -f docker-compose.yml build frontend
docker compose -f docker-compose.yml up -d frontend
```
