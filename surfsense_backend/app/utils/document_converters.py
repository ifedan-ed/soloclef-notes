import asyncio
import hashlib
import logging
import os
import time
import warnings

import numpy as np
from litellm import get_model_info, token_counter

from app.config import config
from app.db import Chunk, DocumentType
from app.embedding_roles.document_embedding import MODEL_LOCK, embed_documents
from app.observability.domains import embedding

logger = logging.getLogger(__name__)

# HuggingFace fast tokenizers (Rust-backed) are not thread-safe — concurrent
# access from multiple threads causes "RuntimeError: Already borrowed".
# This reentrant lock serialises the tokenizer and a model loaded into this
# process so that asyncio.to_thread calls from index_batch_parallel don't
# collide. It is held only while they run: never while a batch waits for a
# document slot or a hosted model answers (app/embedding_roles).
_embedding_lock = MODEL_LOCK


def _get_embedding_max_tokens() -> int:
    """Get the max token limit for the configured embedding model.

    Checks model properties in order: max_seq_length, _max_tokens.
    Falls back to ``EMBEDDING_MAX_TOKENS`` (default 6000), deliberately below
    the common 8192: the fallback is reached when the model is served under a
    local alias, so the count here uses a generic tokeniser that disagrees
    with the model's own by a few per cent. 8192 counted here arrived as 8193
    at the provider, which refused the whole request, so every document over
    about eight thousand tokens failed to index.
    """
    model = config.embedding_model_instance
    for attr in ("max_seq_length", "_max_tokens"):
        val = getattr(model, attr, None)
        if isinstance(val, int) and val > 0:
            return val
    return int(os.getenv("EMBEDDING_MAX_TOKENS", "6000"))


def truncate_for_embedding(text: str) -> str:
    """Truncate text to fit within the embedding model's context window.

    Uses the embedding model's own tokenizer for accurate token counting,
    so the result is model-agnostic regardless of the underlying provider.
    """
    max_tokens = _get_embedding_max_tokens()
    if len(text) // 3 <= max_tokens:
        return text

    with _embedding_lock:
        tokenizer = config.embedding_model_instance.get_tokenizer()
        tokens = tokenizer.encode(text)
        if len(tokens) <= max_tokens:
            return text

        warnings.warn(
            f"Truncating text from {len(tokens)} to {max_tokens} tokens for embedding.",
            stacklevel=2,
        )
        return tokenizer.decode(tokens[:max_tokens])


def titled_for_embedding(title: str, text: str) -> str:
    """``File: <title>`` and the text, cut to the model's window: a file
    document's stored summary. Call it off the event loop: counting a long
    text's tokens takes a while and waits for the tokenizer's lock."""
    return f"File: {title}\n\n{truncate_for_embedding(text)}"


def embed_text(text: str) -> np.ndarray:
    """Embed one text that will be stored, cut to the model's context window.

    A document embedding (``EMBEDDING_DOCUMENT_MODEL``), like ``embed_texts``;
    a question goes through ``app.embedding_roles.query_embedding.embed_query``.
    """
    return embed_texts([text])[0]


def embed_texts(texts: list[str]) -> list[np.ndarray]:
    """Embed texts that will be stored, in order, with the document model.

    Each text is cut to fit the model's context window, then they go in
    batches sized for the server they are sent to, each batch in one of that
    server's host-wide slots, so an import never queues ahead of a question
    on the question model's server (``app/embedding_roles``). A model in this
    process embeds them one by one; a hosted one takes each batch in one call.
    """
    if not texts:
        return []
    model_name = config.EMBEDDING_DOCUMENT_MODEL
    t0 = time.perf_counter()
    with embedding.embedding_span(
        count=len(texts), model=model_name, extra={"embedding.role": "document"}
    ):
        try:
            # The tokenizer takes the lock for each text it counts; the
            # batches then wait for their slots without it.
            return embed_documents([truncate_for_embedding(t) for t in texts])
        finally:
            embedding.record_embedding_duration(
                (time.perf_counter() - t0) * 1000,
                model=model_name,
                count=len(texts),
            )


def get_model_context_window(model_name: str) -> int:
    """Get the total context window size for a model (input + output tokens)."""
    try:
        model_info = get_model_info(model_name)
        context_window = model_info.get("max_input_tokens")
        # Handle case where key exists but value is None
        if context_window is None:
            print(
                f"Warning: max_input_tokens is None for {model_name}, using default 4096 tokens."
            )
            return 4096  # Conservative fallback
        return context_window
    except Exception as e:
        print(
            f"Warning: Could not get model info for {model_name}, using default 4096 tokens. Error: {e}"
        )
        return 4096  # Conservative fallback


def optimize_content_for_context_window(
    content: str, document_metadata: dict | None, model_name: str
) -> str:
    """
    Optimize content length to fit within model context window using binary search.

    Args:
        content: Original document content
        document_metadata: Optional metadata dictionary
        model_name: Model name for token counting

    Returns:
        Optimized content that fits within context window
    """
    if not content:
        return content

    # Get model context window
    context_window = get_model_context_window(model_name)

    # Reserve tokens for: system prompt, metadata, template overhead, and output
    # Conservative estimate: 2000 tokens for prompt + metadata + output buffer
    # TODO: Calculate Summary System Prompt Token Count Here
    reserved_tokens = 2000

    # Add metadata token cost if present
    if document_metadata:
        metadata_text = (
            f"<DOCUMENT_METADATA>\n\n{document_metadata}\n\n</DOCUMENT_METADATA>"
        )
        metadata_tokens = token_counter(
            messages=[{"role": "user", "content": metadata_text}], model=model_name
        )
        reserved_tokens += metadata_tokens

    available_tokens = context_window - reserved_tokens

    if available_tokens <= 100:  # Minimum viable content
        print(f"Warning: Very limited tokens available for content: {available_tokens}")
        return content[:500]  # Fallback to first 500 chars

    # Binary search to find optimal content length
    left, right = 0, len(content)
    optimal_length = 0

    while left <= right:
        mid = (left + right) // 2
        test_content = content[:mid]

        # Test token count for this content length
        test_document = f"<DOCUMENT_CONTENT>\n\n{test_content}\n\n</DOCUMENT_CONTENT>"
        test_tokens = token_counter(
            messages=[{"role": "user", "content": test_document}], model=model_name
        )

        if test_tokens <= available_tokens:
            optimal_length = mid
            left = mid + 1
        else:
            right = mid - 1

    optimized_content = (
        content[:optimal_length] if optimal_length > 0 else content[:500]
    )

    if optimal_length < len(content):
        print(
            f"Content optimized: {len(content)} -> {optimal_length} chars "
            f"to fit in {available_tokens} available tokens"
        )

    return optimized_content


async def create_document_chunks(content: str) -> list[Chunk]:
    """
    Create chunks from document content.

    Args:
        content: Document content to chunk

    Returns:
        List of Chunk objects with embeddings
    """
    chunk_texts = [c.text for c in config.chunker_instance.chunk(content)]
    chunk_embeddings = await asyncio.to_thread(embed_texts, chunk_texts)
    return [
        Chunk(content=text, embedding=emb, position=i)
        for i, (text, emb) in enumerate(
            zip(chunk_texts, chunk_embeddings, strict=False)
        )
    ]


async def convert_element_to_markdown(element) -> str:
    """
    Convert an Unstructured element to markdown format based on its category.

    Args:
        element: The Unstructured API element object

    Returns:
        str: Markdown formatted string
    """
    element_category = element.metadata["category"]
    content = element.page_content

    if not content:
        return ""

    markdown_mapping = {
        "Formula": lambda x: f"```math\n{x}\n```",
        "FigureCaption": lambda x: f"*Figure: {x}*",
        "NarrativeText": lambda x: f"{x}\n\n",
        "ListItem": lambda x: f"- {x}\n",
        "Title": lambda x: f"# {x}\n\n",
        "Address": lambda x: f"> {x}\n\n",
        "EmailAddress": lambda x: f"`{x}`",
        "Image": lambda x: f"![{x}]({x})",
        "PageBreak": lambda x: "\n---\n",
        "Table": lambda x: (
            f"```html\n{element.metadata['text_as_html']}\n```"
            if element.metadata.get("text_as_html")
            else x
        ),
        "Header": lambda x: f"## {x}\n\n",
        "Footer": lambda x: f"*{x}*\n\n",
        "CodeSnippet": lambda x: f"```\n{x}\n```",
        "PageNumber": lambda x: f"*Page {x}*\n\n",
        "UncategorizedText": lambda x: f"{x}\n\n",
    }

    converter = markdown_mapping.get(element_category, lambda x: x)
    return converter(content)


async def convert_document_to_markdown(elements):
    """
    Convert all document elements to markdown.

    Args:
        elements: List of Unstructured API elements

    Returns:
        str: Complete markdown document
    """
    markdown_parts = []

    for element in elements:
        markdown_text = await convert_element_to_markdown(element)
        if markdown_text:
            markdown_parts.append(markdown_text)

    return "".join(markdown_parts)


def generate_content_hash(content: str, workspace_id: int) -> str:
    """Generate SHA-256 hash for the given content combined with workspace ID."""
    combined_data = f"{workspace_id}:{content}"
    return hashlib.sha256(combined_data.encode("utf-8")).hexdigest()


def generate_unique_identifier_hash(
    document_type: DocumentType,
    unique_identifier: str | int | float,
    workspace_id: int,
) -> str:
    """
    Generate SHA-256 hash for a unique document identifier from connector sources.

    This function creates a consistent hash based on the document type, its unique
    identifier from the source system, and the workspace ID. This helps prevent
    duplicate documents when syncing from connectors such as Google Drive.

    Args:
        document_type: The type of document (e.g., GOOGLE_DRIVE_FILE)
        unique_identifier: The unique ID from the source system (e.g., file ID)
        workspace_id: The workspace this document belongs to

    Returns:
        str: SHA-256 hash string representing the unique document identifier

    Example:
        >>> generate_unique_identifier_hash(
        ...     DocumentType.GOOGLE_DRIVE_FILE,
        ...     "1a2b3c4d5e",
        ...     42
        ... )
        'a1b2c3d4e5f6...'
    """
    # Convert unique_identifier to string to handle different types
    identifier_str = str(unique_identifier)

    # Combine document type value, unique identifier, and workspace ID
    combined_data = f"{document_type.value}:{identifier_str}:{workspace_id}"

    return hashlib.sha256(combined_data.encode("utf-8")).hexdigest()
