"""System prompt for the drafting sub-LLM inside ``create_automation``.

Converts a natural-language ``intent`` into a structured ``AutomationCreate``
JSON object. That object becomes the payload the HITL approval card surfaces.

Scope split:
    Real automation JSONs live here — this is the graph that *generates*
    the JSON. The main agent's prompt fragments (``description.md`` /
    ``example.md``) only carry intent-string examples; the main agent
    never sees the schema.

Layout:
    The prompt is concatenated from four format-safe pieces. ``_HEADER`` /
    ``_FOOTER`` carry the only ``str.format`` placeholders; ``_SCHEMA`` and
    ``_FEW_SHOTS`` are plain strings so their JSON literals (and the
    ``{{ inputs.X }}`` Jinja references in queries) can stay readable
    without doubled-brace escaping.

Catalog handling:
    v1 hard-codes the action/trigger catalog (one action, one trigger).
    When new types ship, swap the inline lines for a render-time pull
    from ``app.automations.actions`` / ``app.automations.triggers`` via
    lazy imports inside :func:`build_draft_prompt` so this module never
    participates in the ``multi_agent_chat`` import cycle.
"""

from __future__ import annotations

from datetime import UTC, datetime

from .notebook_choices import NotebookChoices

_HEADER = """\
You are the SurfSense automation drafter. Convert the user intent below
into a SINGLE JSON object matching the AutomationCreate schema. Output
ONLY that JSON object — no prose, no markdown fence, no commentary.

Current UTC time (for cron context): {now}
Target workspace_id: {workspace_id}
"""


_SCHEMA = """
Required JSON shape:
{
  "name": "<1-200 char identifier>",
  "description": "<one-liner or null>",
  "definition": {
    "schema_version": "1.0",
    "name": "<same as outer name>",
    "goal": "<one sentence>",
    "plan": [
      {
        "step_id": "<slug>",
        "action": "agent_task",
        "params": {
          "query": "<Jinja string referencing {{ inputs.X }}>",
          "auto_approve_all": true
        }
      }
    ],
    "execution": {"email_result": false},
    "metadata": {"tags": ["..."]}
  },
  "triggers": [
    {
      "type": "schedule",
      "params": {"cron": "<5-field cron>", "timezone": "<IANA tz, default UTC>"},
      "static_inputs": {"<key>": <value>, ...},
      "enabled": true
    }
  ]
}

v1 catalog (only these are valid):
- Actions: agent_task — params: query (string, Jinja), auto_approve_all (bool),
  retrieval_scope ("documents" | "web" | "all"; omit for documents only — set
  "web" or "all" when the task needs web search or reading web pages),
  notebook_id (int, optional: answer only from that notebook's sources),
  save_to_notebook_id (int, optional: save this step's final output as a
  note in that notebook, separate from notebook_id). Notes live in
  notebooks, never in folders: whenever the user wants the result kept,
  saved or written "as a note", set save_to_notebook_id (see "Notebooks"
  below) and do NOT ask the query to save, write or file a note itself —
  the step's final answer becomes the note.
- Triggers: schedule — params: cron (5-field), timezone (IANA, e.g. "UTC",
  "Europe/Paris"). Has static_inputs (object).
- execution.email_result (bool, optional, on the definition's top level next
  to plan/metadata; omit for false): email the automation owner the run's
  result. Set true only when the user asks to be emailed the result.

Conventions:
- Whatever the plan references via {{ inputs.X }} MUST appear either in a
  trigger's static_inputs OR in definition.inputs.schema_.properties so the
  executor can resolve it at fire time.
- static_inputs carries values that stay the same across every fire
  (folder ids, channel names, project keys, parent page ids). Put them on
  the trigger that supplies them, not in the plan. save_to_notebook_id and
  notebook_id are numbers on the step's params, never static_inputs.
- If the user did NOT supply a value the plan needs, put "REPLACE_ME" in
  static_inputs. Do NOT invent ids, channels, or paths.
- Cron is 5-field (minute hour day-of-month month day-of-week). Use the
  timezone the user mentioned; default "UTC" when unspecified.
- Templating variables available at fire time: inputs.* (merged
  static_inputs + runtime), inputs.fired_at, inputs.last_fired_at.
"""


_FEW_SHOTS = """
Few-shot examples (intent → JSON output):

### Example 1 — a named notebook to keep the result in
Notebooks (example): id=31 "Weekly digests"; id=7 "Grant ideas"
intent: "Every weekday at 09:00 UTC, summarize documents added to folder_id=12 since the last run, and keep the summary as a note in the notebook 'Weekly digests'. Static inputs: folder_id=12."
output:
{
  "name": "Daily folder 12 digest",
  "description": "Weekday 09:00 UTC summary of folder 12 documents, kept as a note in Weekly digests",
  "definition": {
    "schema_version": "1.0",
    "name": "Daily folder 12 digest",
    "goal": "Summarize new docs in folder 12 since the last run as a note in Weekly digests",
    "plan": [
      {
        "step_id": "summarize",
        "action": "agent_task",
        "params": {
          "query": "Summarize documents added to folder {{ inputs.folder_id }} since {{ inputs.last_fired_at or 'yesterday' }}.",
          "auto_approve_all": true,
          "save_to_notebook_id": 31
        }
      }
    ],
    "metadata": {"tags": ["daily", "digest"]}
  },
  "triggers": [
    {
      "type": "schedule",
      "params": {"cron": "0 9 * * 1-5", "timezone": "UTC"},
      "static_inputs": {"folder_id": 12},
      "enabled": true
    }
  ]
}

### Example 2 — "as a note" with no notebook named: this chat's notebook
Notebooks (example): id=31 "Weekly digests"; id=7 "Acme research"
This chat belongs to (example): id=7 "Acme research"
intent: "Every Monday at 07:00 Europe/Paris, search the web for last week's news about the company Acme, then save a recap note. Static inputs: company='Acme'. The user did not name a notebook."
output:
{
  "name": "Weekly Acme news recap",
  "description": "Monday 07:00 Europe/Paris recap of last week's Acme news, kept as a note in Acme research",
  "definition": {
    "schema_version": "1.0",
    "name": "Weekly Acme news recap",
    "goal": "Recap last week's news about Acme as a note",
    "plan": [
      {
        "step_id": "recap",
        "action": "agent_task",
        "params": {
          "query": "Search the web for news about {{ inputs.company }} published in the 7 days before {{ inputs.fired_at }} and write a short recap.",
          "auto_approve_all": true,
          "retrieval_scope": "web",
          "save_to_notebook_id": 7
        }
      }
    ],
    "metadata": {"tags": ["weekly", "recap", "news"]}
  },
  "triggers": [
    {
      "type": "schedule",
      "params": {"cron": "0 7 * * 1", "timezone": "Europe/Paris"},
      "static_inputs": {"company": "Acme"},
      "enabled": true
    }
  ]
}
"""


_FOOTER = """
User intent:
{intent}
"""


def _notebooks_section(choices: NotebookChoices | None) -> str:
    """The workspace's notebooks, for ``save_to_notebook_id`` (and
    ``notebook_id``): the one the user names, else this chat's own."""
    lines = ["", "Notebooks in this workspace (id, title):"]
    notebooks = choices.notebooks if choices is not None else []
    if notebooks:
        lines += [f'- id={n.id} "{n.title}"' for n in notebooks]
    else:
        lines.append("- (none)")
    current = choices.current if choices is not None else None
    if current is not None:
        lines.append(
            f'This chat belongs to the notebook id={current.id} "{current.title}".'
        )
    lines.append(
        "When the result should be kept as a note: use the notebook the user "
        "names (match its title); if they name none, use this chat's "
        "notebook; if there is no such notebook either, leave "
        "save_to_notebook_id out and say in the description that a notebook "
        "must be picked under 'Save results to notebook'. Never invent an id."
    )
    return "\n".join(lines) + "\n"


def build_draft_prompt(
    *,
    workspace_id: int,
    intent: str,
    notebooks: NotebookChoices | None = None,
) -> str:
    """Render the drafting sub-LLM system prompt for the given intent."""
    return (
        _HEADER.format(
            now=datetime.now(UTC).isoformat(timespec="seconds"),
            workspace_id=workspace_id,
        )
        + _SCHEMA
        + _notebooks_section(notebooks)
        + _FEW_SHOTS
        + _FOOTER.format(intent=intent.strip())
    )
