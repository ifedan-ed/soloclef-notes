---
hide:
  - navigation
---

# Soloclef Notes

A NotebookLM-style workspace that runs on your own server. Add PDFs, web pages
and notes to a notebook, ask questions and get answers that cite the exact
passage, and turn your sources into reports, slide decks, mind maps, quizzes,
flashcards, and audio and video overviews.

There is no hosted service to sign up for, no trial and nothing to buy. You
run the server, you choose the models, and your documents stay with you.

[Run your own server](self-hosting.md){ .md-button .md-button--primary }
[Get the iPhone app](phone-app.md){ .md-button }

![A notebook: sources on the left, the chat in the middle, Studio on the right](images/notebook.png)

## What it does

- **Notebooks of your own sources.** Upload PDFs, Office files and images, paste
  a link, or write notes. PDFs are read on the server by default; nothing leaves
  it unless you pick a cloud parser for a file.
- **Answers with citations.** Every answer links to the passages it came from.
  Tick sources on and off to change what the chat reads.
- **Studio.** Audio overviews, video overviews, mind maps, reports, slide decks,
  infographics, quizzes, flashcards and data tables, made from the sources you
  pick.
- **Any model.** Point it at any OpenAI-compatible endpoint: LiteLLM, Ollama,
  vLLM, OpenRouter, OpenAI and others.
- **Phone app.** The [Soloclef app](phone-app.md) for iPhone signs in to your
  server, with notebooks, chat, Studio and changes made offline.
- **Agents.** A built-in MCP server lets Claude, ChatGPT, Cursor and other agents
  search your notebooks.

![An answer with a citation](images/chat-answer.png)

## Start here

| Page | What it covers |
| --- | --- |
| [Run your own server](self-hosting.md) | Requirements, the first start, the first account, models |
| [Sign-in](sign-in.md) | Email and password, single sign-on (Pocket ID, Authentik, Keycloak), closing sign-ups |
| [Usage and limits](usage.md) | No trials or codes; unlimited use for the admin, and how to give it to others |
| [Phone app](phone-app.md) | The App Store app and connecting it to your server |
| [Updating and backups](operations.md) | Upgrading, and what to back up |

The full reference for every setting is the in-app guide, which a running
server serves at `/docs`; its source is in
[`surfsense_web/content/docs`](https://github.com/ifedan-ed/soloclef-notes/tree/main/surfsense_web/content/docs).

Soloclef Notes is a fork of [SurfSense](https://github.com/MODSetter/SurfSense),
under the Apache License 2.0.
