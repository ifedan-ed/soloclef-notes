<example>
user: "Save these meeting notes to my KB: …"
→ task(subagent_type="knowledge_base", description="Save the notes below to
  a new document under /documents/notes/. Pick a sensible title and folder;
  tell me the path you used.\n\n<notes>…</notes>")
</example>

<example>
user: "Use my Exa server to find recent papers on retrieval-augmented generation."
→ task(subagent_type="mcp_discovery", description="Using the Exa MCP server,
  search for papers on retrieval-augmented generation from the past year.
  Return the most relevant results with title, date, and URL.")
</example>

<example>
user: "Find my Q2 roadmap and summarise the milestones."
→ task(subagent_type="knowledge_base", description="Locate the Q2 roadmap
  document under /documents and summarise its milestones. Use glob or grep
  if the path isn't obvious from the workspace tree.")
</example>
