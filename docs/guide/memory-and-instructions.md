# Memory and instructions

Three settings shape how the assistant answers you: your instructions, your
saved prompts, and its memory of you. All three are under Settings.

## Instructions

Settings, Instructions holds standing instructions for every chat and every
Studio output: the tone, the audience, the format, the length. For example,
"Explain things for a first-year medical student, and keep answers short."
They can be up to 4,000 characters.

Answers still come from your sources, with their citations, and still follow
the assistant's safety rules. A line that tries to change those is ignored,
and saving warns "Some instructions won't be followed".

A notebook's own instructions, set with Configure chat (see
[Chat and citations](chat-and-citations.md#configure-chat)), win inside that
notebook. While instructions are in use, the chat shows Instructions applied.

The Citations switch on the same page decides whether answers number each
claim and link it to its passage.

## My prompts

Settings, My prompts keeps prompts you use often. Type / in the chat to use
one.

Choose New and fill in:

- Name, such as "Fix grammar".
- Prompt template. Put `{selection}` where the selected text should go;
  without it, the text is added at the end.
- Mode: Transform, which rewrites or changes your text, or Explore, which
  answers a question about it.

Each prompt has Edit and Delete.

## Memory

The assistant notes lasting facts about you as you chat, such as your field,
how you like answers and what you are working on. It reads them at the start
of every chat.

Settings, Memory shows what it has kept, as plain text. Correct anything
wrong, add anything you want it to know, and choose Save memory. Forget
everything empties it, once you confirm. Memory can be up to 25,000
characters, and the page warns you as it gets close.

Memory is only about you. Your notebooks, notes and chats are not touched by
it. To stop the assistant writing to it, switch the memory tool off in the
chat's + menu, under Manage Tools.
