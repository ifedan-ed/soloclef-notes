package knowledgebase

// The published descriptions of this slice's tools, taken from the Python
// server's docstrings unchanged. A client shows them to a model, so a reworded
// one is a different tool surface even where the behaviour is the same.

const SearchKnowledgeBaseDescription = `Search the workspace's knowledge base by meaning and keywords.

        Use this FIRST when a question might be answered by content already
        stored in Soloclef Notes — notes, uploaded files, saved pages, past
        research. Do NOT use it to fetch new data from the web; use the
        scraper tools for that. Returns the most relevant documents with the
        passages that matched, ranked by relevance score.
        Example: query='pricing feedback', top_k=5.
        `

const ListDocumentsDescription = `List documents in the workspace's knowledge base, newest first.

        Use this to browse or inventory what is stored; to find documents
        about a topic, prefer soloclef_search_knowledge_base. Returns each
        document's title, id, type, and update time, plus a has_more flag —
        request the next page by increasing page.
        Example: document_types=['FILE'], page=0, page_size=20.
        `

const GetDocumentDescription = `Read one document's full content and metadata by id.

        Use this after soloclef_search_knowledge_base or
        soloclef_list_documents to open a specific document — search results
        only include the matching passages, this returns the whole text.
        The markdown form is an Open Knowledge Format (OKF) concept: a YAML
        frontmatter block (type, title, tags, resource, timestamp) followed by
        the document body.
        `

const AddDocumentDescription = `Save a text or markdown note into the workspace's knowledge base.

        Use this to store notes, summaries, or findings so they become
        searchable later — e.g. after finishing a piece of research. For files
        on disk use soloclef_upload_file instead. Indexing is asynchronous,
        so the note may take a moment to appear in search.
        Example: title='NotebookLM subreddits', content='- r/notebooklm ...'.
        `

const UploadFileDescription = `Upload a local file (PDF, docx, markdown, etc.) into the knowledge base.

        Use this to ingest a file from disk so its content becomes searchable;
        for text you already have in hand use soloclef_add_document instead.
        The file is parsed, chunked, and indexed asynchronously. Duplicate
        files are detected and skipped.
        Example: file_path='C:/Users/me/report.pdf'.
        `

const UpdateDocumentDescription = `Replace a document's stored content by id.

        Use this to correct or rewrite a document's text. The new content
        REPLACES the old entirely — to append, read the document first with
        soloclef_get_document and resend the combined text. The document
        re-indexes in the background afterwards.
        `

const DeleteDocumentDescription = `Permanently delete a document from the knowledge base by id.

        Use this only when the user explicitly asks to remove a document —
        deletion cannot be undone. The document stops appearing in searches
        immediately.
        `
