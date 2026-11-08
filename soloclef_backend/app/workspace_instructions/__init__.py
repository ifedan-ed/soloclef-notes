"""A workspace's System Instructions, as the AI sees them.

The Owner writes them in Workspace settings -> System Instructions
(``Workspace.qna_custom_instructions``). They reach every chat answer in that
workspace (the main agent's system prompt) and every Studio output (the
artifact prompt and the video brief), always inside one delimited block that
says what they are: the owner's style guidance, below the platform's rules.

They can shape tone, format, length, language and emphasis. They cannot
change which sources an answer may use, loosen grounding or citations, unlock
tools, or override safety and refusal rules; the block says so, and it is
placed so the platform sections keep precedence.

The text is data inside the block: angle brackets become single angle quotes
(one character each, so the length cap holds), so it can neither close the
block nor open a section that looks like the platform's own.

Import from the submodules: ``render`` (pure, safe for schemas) and ``load``
(reads the workspace row).
"""
