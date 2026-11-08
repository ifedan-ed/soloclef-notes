"""The workspace's quick-tasks model: small one-shot helper calls.

Suggestion chips, follow-up questions, source guides and thread titles are a
few dozen words each. They run on their own model so a slow, thinking chat
model (or Auto landing on one) does not make a three-chip suggestion take 20 s.

Import from the submodules: ``app.db`` imports ``model`` while it is still
loading, so this package must not pull in the LLM service on import.
"""
