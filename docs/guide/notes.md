# Notes

Notes live in a notebook's Studio panel, alongside the outputs, and all of
them are listed together on the Notes page.

- Add note writes a new note.
- Save to note under a chat answer keeps that answer as a note, titled
  with its first line of words. A title taken from a note's text, and the
  preview under it, come from its first 2,000 characters, read after each
  link and image has become its words (an image with no description, such
  as a pasted picture, is nothing, so the title comes from the next line);
  with nothing but blank lines or formatting before that, the note is
  "Untitled".
- Convert to source turns a note into a source, so the chat and Studio
  can use it. The note then shows "Added as a source".
- An [automation](automations.md) set to Save results to notebook adds
  its result here as a note after each run, titled with the automation's name
  and the date.

Notes are part of the notebook: Copy notebook copies them, and the home
search looks inside them (see [Search](search.md)).

Notes are not shown on a
[shared notebook link](workspaces-and-sharing.md#public-links).

## The Notes page

Notes in the left rail lists all your notes: the ones
written by hand, the ones kept from a chat, and the ones made by automations.
They are grouped where they live: a notebook's notes under the notebook, and
the others under their folder, as your documents have them, or
under Not in a folder. Each group shows its newest note first; click a
group's name to fold it away. Each note shows what made it (By an
automation, From a chat or Written by hand), the notebooks it is a
source of, and when it was made. Search notes finds notes by their title
or text.

Once an automation has made a note, Automation notes at the top groups
the notes by the automation that made them instead.

Some notes are Not in a notebook. These are notes the assistant wrote as a
file, for example an automation told to "save the summary as a note" without
a notebook to save into. No Studio panel shows them until you add them to
one.

Open a note to read and edit it. It opens once its text is there, drawn
whole: headings, lists, tables, code, links, maths and citations as the chat
showed the answer. Opening a note or clicking into it does not change it;
only what you type is saved.

- A notebook's note saves as you type, as in the Studio panel. Open in
  notebook takes you to it there, and Copy to a notebook puts a copy in
  another notebook.
- A note that is not in a notebook has a Save button. Add to a
  notebook moves it into the notebook you choose, where it shows in the
  Studio panel from then on; it keeps its text, date and automation.
  Convert to source makes it a source of the notebook you choose.

Delete removes a note from wherever it is.

## Notes from automations

An automation saves its result as a note when its task has Save results to
notebook set. If that notebook has been deleted, the task fails before it
starts and says so. When you ask a notebook's chat to set up an automation
that keeps its result "as a note", the result is saved in the notebook you
name, or in that chat's notebook if you name none.

An automation without Save results to notebook may still write a note of
its own when its task asks for one. That note is listed on the Notes page as
By the automation and Not in a notebook; use Add to a notebook to
put it where you want it.
