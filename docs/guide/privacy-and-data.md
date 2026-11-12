# Privacy and data

Soloclef is self-hosted: it runs on its owner's own server, not on a
large cloud platform. Your files, notebooks, notes, chats and Studio outputs
are stored there.

Some features need AI models or services that run elsewhere. This page lists
exactly what goes where, so you can choose.

## What stays on the server

- Your files and everything made from them. Uploads, sources, notes,
  chats and Studio outputs are stored on the server.
- Local parsing. The Local parser reads files on the server. The file
  itself is not sent anywhere.
- Search index. Sources are turned into search vectors with the BGE-M3
  embedding model, which runs on the server. If that local service is down,
  the text is sent to the same model at OpenRouter instead, so indexing
  doesn't stop.
- No analytics. There is no third-party tracking or analytics. If the app
  crashes in your browser, the error report goes to the server itself.
- Report Issue. On an error message, Report Issue only opens your own
  mail app with a draft to the site's owner: the error code, request ID,
  message, page, browser and time. Nothing is sent unless you send it, through
  your own mail provider.

## What is sent to outside services

| When you… | What is sent | To |
| --- | --- | --- |
| Chat, or make a Studio output | Your question and the relevant passages from your sources | The AI model provider of the model in use (for example OpenAI, Google, DeepSeek or others through OpenRouter) |
| Choose a hosted parser | The whole file, and with Vision on, the images in it | The parser you chose (Datalab, LlamaParse or Unstructured), and no other |
| Turn on Vision | The images in the file | The vision model's provider |
| Make an audio or video overview | The script to be read aloud | OpenAI, for the voices |
| Search for new sources | Your search query | The service you search (PubMed, arXiv, Google Scholar, Exa, Tavily and so on). The web pages you import are reached through the site's web proxy service |
| Read a web page | The page's address | Browserbase, if you have a Browserbase connector; otherwise, or if Browserbase fails, the server reads it through the site's web proxy service |
| Read a page that shows a captcha | The page's address and its captcha | A captcha-solving service (2captcha) |
| Pick Exa, Tavily or Browserbase in the chat's Tools | The search words, or the address of the page to open | Exa, Tavily or Browserbase, with your own key |
| Pick files from Nextcloud | Nothing extra: the server fetches the files from your Nextcloud | — |
| Pick files from Google Drive | Nothing extra: the server fetches only the files you picked, and Google tells the app about no others | — |
| Connect an agent over MCP | Whatever the agent asks for with your API key or sign-in: search results and documents from your account, while API access is on | Your agent and its model provider |
| Connect a mailbox (Email (IMAP)) | Your username and app password, which are stored encrypted here and never shown again | Your mail server, which the server signs in to |
| Let the assistant read your mail | The text of the messages it reads, and of any attachment it opens; images in attachments go to the vision model | The AI model provider of the model in use, and the vision model's provider |
| Get an email from the app (password reset, automation result) | The email: for an automation, the run's full result | The site's mail provider (SMTP2GO), then your own mail provider |
| Sign in with the single sign-on button | You sign in there, and it tells the app your email address | The site owner's own single sign-on service |

If a file must not leave the server, use the Local parser, leave
Vision off, and bear in mind that asking about it still sends the
relevant passages to a model provider.

## Who can see what

Everything in your account is yours alone: notebooks, documents, notes,
chats, Studio outputs, connections, [automations](automations.md) and their
results. No other account can see any of it. Your connections, your mailbox
included, are used only by your own chats and automations.

You can make public links: a read-only copy of a chat or of a finished Studio
output, or a live link to a notebook that shows your newest chat in it, its
finished outputs and its source files. Anyone with the link can open it
without signing in. See
[Settings and sharing](workspaces-and-sharing.md#public-links).

Agents you connect over MCP, and your API keys, can read and change everything
in your account while API access is on in Settings → API access (see
[Connect your agent](connect-your-agent.md)).

## Deleting your data

Deleting a notebook removes its source links (your documents and notes
stay), and Delete chat history removes the chats in it. To have all of your data
deleted, including uploaded documents and your account, ask the owner through
the contact page (`/contact` on your server).

You can also delete your account yourself, with Settings → Profile → Delete
account (type DELETE to confirm). It happens at once and can't be undone: your
notebooks, notes, chats, documents, connectors and API keys are deleted with
it.

Removing a mailbox from the Email (IMAP) card removes it from the app
only; nothing in the mailbox changes.
