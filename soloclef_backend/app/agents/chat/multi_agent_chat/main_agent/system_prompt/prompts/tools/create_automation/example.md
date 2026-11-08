<example>
user: "Every weekday at 9am, summarize new documents in folder 12 and keep the summary in my Weekly digests notebook."
→ create_automation(intent="Every weekday at 09:00 UTC, summarize documents added to folder_id=12 since the last run, and keep the summary as a note in the notebook 'Weekly digests'. Static inputs: folder_id=12.")
tool returns: {"status": "saved", "automation_id": 42, "name": "Daily folder 12 digest"}
(Reply briefly: "Saved as automation #42 — runs weekdays at 9am UTC; each summary lands in Weekly digests as a note.")
</example>

<example>
user: "Once a week on Mondays at 7am Paris time, write me a recap note of last week's news about Acme."
→ create_automation(intent="Every Monday at 07:00 Europe/Paris, search the web for last week's news about the company Acme and keep a recap as a note. Static inputs: company='Acme'. The user did not name a notebook.")
tool returns: {"status": "saved", "automation_id": 51, "name": "Weekly Acme news recap"}
(Reply: "Saved as automation #51 — every Monday at 7am Paris time; the recap is added to this notebook's notes.")
</example>
