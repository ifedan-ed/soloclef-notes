# Settings and sharing

Everything you make here is your account's own: notebooks, documents, notes,
chats, connectors, automations, prompts, the assistant's memory and the model
you chat with. Nobody else can see or change any of it unless you share it with
a public link. (Shared workspaces, members and invitations were removed; an old
invitation link now says "Invitations are no longer used".)

## Settings

Open the account menu (your avatar at the foot of the rail) and choose
Settings. The page lists its sections on the left; on a phone, tap the section
name at the top to switch.

- Profile: your display name and avatar, and deleting your account.
- Appearance: light, dark or the system's theme.
- API access: the switch that lets API keys and agents act for you, your API
  keys, and the runs made with them. See [API access](#api-access) below.
- Models: the model your chats use. See
  [Chat and citations](chat-and-citations.md#choosing-the-model).
- Instructions: your standing instructions for every chat, and the Citations
  switch. See [Instructions](#instructions) below.
- Memory: what the assistant remembers about you. See [Memory](#memory) below.
- My prompts: your saved prompts, offered when you type / in the chat.
- Connectors: Google Drive, Nextcloud, Email and MCP servers, each switched on
  or off for all your notebooks. See
  [Notebooks and sources](notebooks-and-sources.md#connectors) and
  [Email](email.md).
- Automations: see [Automations](automations.md).
- Public links: every link you have made, below.

Administrators also see Agent permissions, Announcements (a
message sent to every account's notifications) and Community prompts. The
desktop app adds App preferences and Hotkeys.

### API access

API keys and AI agents (Claude, ChatGPT and others signed in through
[Connect your agent](connect-your-agent.md)) can only act for you while
"API and agent access" is on. When it is on, they can read and change
everything in your account: notebooks, documents, notes and chats. When it is
off, they are refused, even with a valid key. It is off for a new account.

### Instructions

Your instructions shape every answer, in chat and in Studio: tone, audience,
format and length. A notebook's own instructions win inside that notebook.
Answers still come only from your sources, keep their citations and follow the
safety rules. A line that tries to change those is ignored, and saving warns
"Some instructions won't be followed". Instructions can be up to 4,000
characters. While they are set, the chat shows "Instructions applied".

The Citations switch on the same page decides whether answers number each
claim and link it to the passage it came from.

### Memory

The assistant notes lasting facts about you as you chat (your field, how you
like answers, what you are working on) and reads them at the start of every
chat. The Memory section shows that text: correct anything that is wrong and
save, or choose Forget everything to empty it. Your notebooks, notes and chats
are not touched.

## Public links

You can share something read-only with anyone who has the link, without them
signing in:

- Share chat publicly… in the notebook menu shares a copy of the chat as it is
  now.
- Share publicly, in a finished Studio output's viewer, shares a copy of that
  output. It opens as you see it: a mind map to explore, flashcards and a quiz
  to study (a visitor's answers stay in their browser), a slide deck's slides,
  a data table's sheet, a report with its contents. Your prompt and sources,
  Explain and a report's recommended outputs are not shown. Sharing it again
  while it is unchanged gives the same link, with its current title and the
  expiry you pick.
- Share notebook… in the notebook menu makes a live link to the notebook.
  Visitors see your newest chat in it, its finished Studio outputs and its
  source files, as they are each time the link is opened. Notes are not shown.
  The notebook then shows Shared. New address replaces the link, and Stop
  sharing turns it off.

A link expires after 7 days (the default), after 30 days or never. An expired
link says "This link has expired".

Settings → Public links lists every link (Chat, Output or Live notebook), with
Copy link, Open link and Revoke. Revoking stops a link at once.
