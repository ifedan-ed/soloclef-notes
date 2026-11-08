"""Every note in a workspace, in one list: the Notes page.

A workspace has two kinds of note:

* **notebook notes** (``notebook_notes``): written with Add note, kept with
  Save to note under an answer, or saved by an automation's "Save results to
  notebook". They show in their notebook's Studio panel.
* **note documents** (``documents`` of type ``NOTE``) that no notebook note
  stands for: Markdown files the assistant wrote into ``/documents`` (in a
  chat, or in an automation run told to "save the summary as a note"). No
  Studio panel shows them, which is why an automation that wrote only these
  looked as if it had never run.

The Notes page lists both, newest first, says where each lives (its notebook,
or its folder of the documents tree, which the page groups by) and what made
it, and can move a note document into a notebook (it then becomes that
notebook's note).
"""
