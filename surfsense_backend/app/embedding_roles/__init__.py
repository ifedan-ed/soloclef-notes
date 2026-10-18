"""Questions and documents can be embedded by two hosts of the same model.

A question (a chat search, a notebook search, the knowledge-base tool) is
embedded by ``EMBEDDING_QUERY_MODEL``, with someone waiting; a text that will
be stored (indexing, notes, imports, reverts) by ``EMBEDDING_DOCUMENT_MODEL``.
Both default to ``EMBEDDING_MODEL``, so a server that sets neither runs one
model for everything, as before. Document batches take a host-wide slot of
the server they go to and are sized for it (``settings``): larger and side by
side for a document model on its own host, small and one at a time on the
question model's server, so a busy import never queues ahead of a question
there; questions take no slot. A failing document host costs one short
timeout, then the breaker sends batches to the question model for a while.
"""
