# Automations

An automation runs a task for you with nobody watching. For example, a weekly
summary of what is new in your documents, or a morning look through your
mail. It runs on a schedule, or when you press Run now, and can keep its
result as a note or email it to you.

Automations are under Settings, Automations on the website, and in the
Automations tab of the [phone app](mobile-app.md).

## Making one

Choose Create manually. The form has these parts.

### Basics

A name, up to 200 characters, and an optional description.

### Tasks

Each task is one instruction in your own words. Type @ in it to point at
files, folders or a connected account such as a mailbox or Google Drive. Add
more tasks and put them in order with the arrows. Each task also has:

- Can use:
    - Your documents.
    - Your documents and the web: it can also search the web with your own
      Exa or Tavily key, and search PubMed, arXiv and medRxiv / bioRxiv.
    - The web only: the web and your connected apps, not your documents.
- Notebook: Any notebook, or one notebook, so the task reads only that
  notebook's sources. A task kept to one notebook does not search the web.
- Save results to notebook: Don't save, or a notebook. After each run, the
  result is added there as a [note](notes.md), named after the automation and
  the date.
- Advanced: Max retries, Timeout, and Model for this task, which overrides
  the automation's model for this one task.

Your connected mailboxes, Google Drive, Nextcloud and MCP servers are
available to every task.

Run without asking for approvals is on by default. With it off, anything the
assistant would need your approval for is refused, and the task may not
finish. Some tools always need a person, such as reading a web page in a
browser, so an automation never uses them.

### Schedule

Choose a Frequency: Every hour, Every day, Every weekday (Mon–Fri), Specific
days of the week or Once a month. Then set the minute, time, days or day of
the month, and the Timezone. A new automation starts as every weekday at
09:00. Schedule expression takes a five-field cron expression instead, such
as `0 9 * * 1-5`.

An automation with no schedule runs only when you press Run now.

### Model

Runs on chooses the model, Your chat model unless you pick another.

### Settings

- Timeout (seconds): how long a whole run may take. The default is 600, and
  the most is 86,400, which is a day. A run still going then is stopped and
  shown as Timed out.
- Max retries: how many times a failed task is tried again, 2 by default,
  with Retry backoff Exponential, Linear or None.
- If already running: what happens when the schedule comes round while the
  last run is still waiting or running.
    - Skip if already running, the default, skips the new run.
    - Queue the next run makes the new run wait, shown as Pending, until the
      one before it ends. Waiting runs start one at a time, oldest first.
    - Always run starts the new run at once, beside the other.
- Tags, to keep automations sorted.
- Email me the result. It can only be switched on if the server sends email.

The summary beside the form sums up what you have set. Edit as JSON shows
the same automation as JSON, to edit by hand.

## Starting on an event

Instead of a time, an automation can start when a document arrives in a
folder. This trigger is set in Edit as JSON, as a `document.entered_folder`
trigger with the folder it watches. An automation that has one opens in the
JSON editor.

## Mail in automations

A task can search and read your connected mailboxes and their attachments,
as described in [Email](email.md#what-the-assistant-can-do). Point the task
at a mailbox with @, or ask for mail in its words.

A task can also draft a message from a Stalwart mailbox. The run never sends
it. The draft waits on the Drafts waiting page for up to 24 hours, and you are
notified that it is there. That page is the only place a draft from an
automation can be sent. See
[Email](email.md#drafts-nothing-is-sent-until-you-press-send).

## Runs and results

The list shows each automation as Active, Paused or Archived. Open one to Run
now, Edit, Pause, Resume or Delete it. Its Triggers card shows each schedule
with its own switch and when it fires next.

Recent runs lists the last 20 runs, each with its status:

- Pending, waiting to start or queued behind another run;
- Running;
- Succeeded;
- Failed, with the reason;
- Needs attention;
- Timed out;
- Cancelled.

Open a run to see its output, each task's result, what the assistant said,
each tool it called, and the files it made.

A Pending or Running run has a Cancel button. The run stops within a few
seconds, and anything it already saved, such as a note, stays. Pause does the
same to the running run and to any waiting behind it. Delete removes the
automation and its whole history.

A run's result reaches you in up to four places:

- on the run's page;
- in [Notifications](notifications.md), when a run finishes, fails, needs
  attention or times out, with a push to your phone if you allow it;
- as a note, with Save results to notebook;
- by email, with Email me the result. The email goes to whoever created the
  automation. A run that fails, needs attention or times out sends a short
  email with the reason instead. A cancelled run sends nothing.
