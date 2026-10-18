"""The workspace's Studio outputs model: which model writes each Studio output.

Studio generations (video scripts, podcast transcripts, reports, decks, study
sets, mind maps, infographics, data tables) run on this setting rather than
the chat model, with an optional fallback model tried once when the first
one fails. Notebook chat and quick tasks keep their own models.

Import from the submodules: ``app.db`` imports ``model`` while it is still
loading, so this package must not pull in the LLM service on import.
"""
