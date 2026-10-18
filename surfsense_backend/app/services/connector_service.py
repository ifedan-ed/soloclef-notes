import asyncio
import time
from datetime import datetime
from threading import Lock
from typing import Any

from sqlalchemy import func
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.future import select

from app.config import config
from app.db import (
    Chunk,
    Document,
    SearchSourceConnector,
    SearchSourceConnectorType,
    async_session_maker,
)
from app.retriever.chunks_hybrid_search import ChucksHybridSearchRetriever
from app.retriever.documents_hybrid_search import DocumentHybridSearchRetriever
from app.services.connector_enablement import enabled_connectors_in_workspace
from app.utils.perf import get_perf_logger


class ConnectorService:
    def __init__(self, session: AsyncSession, workspace_id: int | None = None):
        self.session = session
        self.chunk_retriever = ChucksHybridSearchRetriever(session)
        self.document_retriever = DocumentHybridSearchRetriever(session)
        self.workspace_id = workspace_id
        self.source_id_counter = (
            100000  # High starting value to avoid collisions with existing IDs
        )
        self.counter_lock = (
            asyncio.Lock()
        )  # Lock to protect counter in multithreaded environments

    async def initialize_counter(self):
        """
        Initialize the source_id_counter based on the total number of chunks for the workspace.
        This ensures unique IDs across different sessions.
        """
        if self.workspace_id:
            try:
                # Count total chunks for documents belonging to this workspace

                result = await self.session.execute(
                    select(func.count(Chunk.id))
                    .join(Document)
                    .filter(Document.workspace_id == self.workspace_id)
                )
                chunk_count = result.scalar() or 0
                self.source_id_counter = chunk_count + 1
                print(
                    f"Initialized source_id_counter to {self.source_id_counter} for workspace {self.workspace_id}"
                )
            except Exception as e:
                print(f"Error initializing source_id_counter: {e!s}")
                # Fallback to default value
                self.source_id_counter = 1

    async def search_files(
        self,
        user_query: str,
        workspace_id: int,
        top_k: int = 20,
        start_date: datetime | None = None,
        end_date: datetime | None = None,
    ) -> tuple:
        """
        Search for files and return both the source information and langchain documents.

        Uses combined chunk-level and document-level hybrid search with RRF fusion.

        Args:
            user_query: The user's query
            workspace_id: The workspace ID to search in
            top_k: Maximum number of results to return
            start_date: Optional start date for filtering documents by updated_at
            end_date: Optional end date for filtering documents by updated_at

        Returns:
            tuple: (sources_info, langchain_documents)
        """
        files_docs = await self._combined_rrf_search(
            query_text=user_query,
            workspace_id=workspace_id,
            document_type="FILE",
            top_k=top_k,
            start_date=start_date,
            end_date=end_date,
        )

        # Early return if no results
        if not files_docs:
            return {
                "id": 2,
                "name": "Files",
                "type": "FILE",
                "sources": [],
            }, []

        def _description_fn(
            chunk: dict[str, Any], _doc_info: dict[str, Any], metadata: dict[str, Any]
        ) -> str:
            return (
                metadata.get("og:description")
                or metadata.get("ogDescription")
                or self._chunk_preview(chunk.get("content", ""))
            )

        sources_list = self._build_chunk_sources_from_documents(
            files_docs,
            description_fn=_description_fn,
            url_fn=lambda _doc_info, metadata: metadata.get("url", "") or "",
        )

        # Create result object
        result_object = {
            "id": 2,
            "name": "Files",
            "type": "FILE",
            "sources": sources_list,
        }

        return result_object, files_docs

    async def _combined_rrf_search(
        self,
        query_text: str,
        workspace_id: int,
        document_type: str | list[str],
        top_k: int = 20,
        start_date: datetime | None = None,
        end_date: datetime | None = None,
        query_embedding: list[float] | None = None,
    ) -> list[dict[str, Any]]:
        """
        Perform combined search using both chunk-based and document-based hybrid search,
        then merge results using Reciprocal Rank Fusion (RRF) **at the document level**.

        Returned results are **document-grouped** objects that contain a list of chunks
        with real chunk IDs (used for downstream `[citation:<chunk_id>]`).

        This method:
        1. Runs chunk-level hybrid search (vector + keyword on chunks)
        2. Runs document-level hybrid search (vector + keyword on documents, returns chunks)
        3. Combines results using RRF based on their ranks in each result set
        4. Returns top-k deduplicated results

        Args:
            query_text: The search query text
            workspace_id: The workspace ID to search within
            document_type: Document type(s) to filter (e.g., "FILE", "CRAWLED_URL",
                           or a list for multi-type queries)
            top_k: Number of results to return
            start_date: Optional start date for filtering documents by updated_at
            end_date: Optional end date for filtering documents by updated_at

        Returns:
            List of combined and deduplicated document results
        """
        from app.embedding_roles.query_embedding import embed_query

        perf = get_perf_logger()
        t0 = time.perf_counter()

        # RRF constant
        k = 60

        # Get more results from each retriever for better fusion
        retriever_top_k = top_k * 2

        # Reuse caller-provided embedding or compute once for both retrievers.
        if query_embedding is None:
            t_embed = time.perf_counter()
            query_embedding = await asyncio.to_thread(embed_query, query_text)
            perf.info(
                "[connector_svc] _combined_rrf embedding in %.3fs type=%s",
                time.perf_counter() - t_embed,
                document_type,
            )

        search_kwargs = {
            "query_text": query_text,
            "top_k": retriever_top_k,
            "workspace_id": workspace_id,
            "document_type": document_type,
            "start_date": start_date,
            "end_date": end_date,
            "query_embedding": query_embedding,
        }

        # Run chunk and document retrievers in parallel using separate DB sessions
        # so they don't contend on a shared AsyncSession connection.
        async def _run_chunk_search() -> list[dict[str, Any]]:
            async with async_session_maker() as session:
                retriever = ChucksHybridSearchRetriever(session)
                return await retriever.hybrid_search(**search_kwargs)

        async def _run_doc_search() -> list[dict[str, Any]]:
            async with async_session_maker() as session:
                retriever = DocumentHybridSearchRetriever(session)
                return await retriever.hybrid_search(**search_kwargs)

        t_parallel = time.perf_counter()
        chunk_results, doc_results = await asyncio.gather(
            _run_chunk_search(), _run_doc_search()
        )
        perf.info(
            "[connector_svc] _combined_rrf parallel retrievers in %.3fs "
            "chunk_results=%d doc_results=%d type=%s",
            time.perf_counter() - t_parallel,
            len(chunk_results),
            len(doc_results),
            document_type,
        )

        if not chunk_results and not doc_results:
            return []

        # Helper to extract document_id from our doc-grouped result
        def _doc_id(item: dict[str, Any]) -> int | None:
            doc = item.get("document", {})
            did = doc.get("id")
            return int(did) if did is not None else None

        # Build rank maps for RRF calculation (document-level)
        chunk_ranks: dict[int, int] = {}
        for rank, result in enumerate(chunk_results, start=1):
            did = _doc_id(result)
            if did is not None and did not in chunk_ranks:
                chunk_ranks[did] = rank

        doc_ranks: dict[int, int] = {}
        for rank, result in enumerate(doc_results, start=1):
            did = _doc_id(result)
            if did is not None and did not in doc_ranks:
                doc_ranks[did] = rank

        all_doc_ids = set(chunk_ranks.keys()) | set(doc_ranks.keys())

        # Calculate RRF scores for each document
        rrf_scores: dict[int, float] = {}
        for did in all_doc_ids:
            chunk_rank = chunk_ranks.get(did)
            doc_rank = doc_ranks.get(did)
            score = 0.0
            if chunk_rank is not None:
                score += 1.0 / (k + chunk_rank)
            if doc_rank is not None:
                score += 1.0 / (k + doc_rank)
            rrf_scores[did] = score

        # Prefer chunk_results data, fallback to doc_results data
        doc_data: dict[int, dict[str, Any]] = {}
        for result in chunk_results:
            did = _doc_id(result)
            if did is not None and did not in doc_data:
                doc_data[did] = result
        for result in doc_results:
            did = _doc_id(result)
            if did is not None and did not in doc_data:
                doc_data[did] = result

        sorted_doc_ids = sorted(
            all_doc_ids, key=lambda did: rrf_scores[did], reverse=True
        )[:top_k]

        combined_results: list[dict[str, Any]] = []
        for did in sorted_doc_ids:
            if did in doc_data:
                result = doc_data[did].copy()
                result["document_id"] = did
                result["score"] = rrf_scores[did]
                # Preserve chunks list if present
                if "chunks" in doc_data[did]:
                    result["chunks"] = doc_data[did]["chunks"]
                combined_results.append(result)

        perf.info(
            "[connector_svc] _combined_rrf_search TOTAL in %.3fs results=%d type=%s space=%d",
            time.perf_counter() - t0,
            len(combined_results),
            document_type,
            workspace_id,
        )
        return combined_results

    def _get_doc_url(self, metadata: dict[str, Any]) -> str:
        return (
            metadata.get("url")
            or metadata.get("source")
            or metadata.get("page_url")
            or metadata.get("VisitedWebPageURL")
            or ""
        )

    def _chunk_preview(self, text: str, limit: int = 200) -> str:
        if not text:
            return ""
        text = str(text)
        if len(text) <= limit:
            return text
        return text[:limit] + "..."

    def _build_chunk_sources_from_documents(
        self,
        documents: list[dict[str, Any]],
        *,
        title_fn=None,
        description_fn=None,
        url_fn=None,
        extra_fields_fn=None,
    ) -> list[dict[str, Any]]:
        """
        Build a chunk-level `sources` list from document-grouped results.

        Each chunk becomes a source with `id == chunk_id` so the frontend can resolve
        citations like `[citation:<chunk_id>]`.
        """
        sources: list[dict[str, Any]] = []

        for doc in documents:
            doc_info = doc.get("document", {}) or {}
            metadata = doc_info.get("metadata", {}) or {}
            url = url_fn(doc_info, metadata) if url_fn else self._get_doc_url(metadata)
            chunks = doc.get("chunks", []) or []
            display_title = (
                title_fn(doc_info, metadata)
                if title_fn
                else doc_info.get("title", "Untitled Document")
            )
            for chunk in chunks:
                chunk_id = chunk.get("chunk_id")
                chunk_content = chunk.get("content", "")
                description = (
                    description_fn(chunk, doc_info, metadata)
                    if description_fn
                    else self._chunk_preview(chunk_content)
                )
                source = {
                    "id": chunk_id,
                    "title": display_title,
                    "description": description,
                    "url": url,
                }
                if extra_fields_fn:
                    source.update(extra_fields_fn(chunk, doc_info, metadata) or {})
                sources.append(source)
        return sources

    async def get_connector_by_type(
        self,
        connector_type: SearchSourceConnectorType,
        workspace_id: int,
    ) -> SearchSourceConnector | None:
        """
        Get a connector of this type enabled in this workspace (any owner for
        account-level types; MCP stays workspace-owned).

        Args:
            connector_type: The connector type to retrieve
            workspace_id: The workspace ID to filter by

        Returns:
            Optional[SearchSourceConnector]: The connector if found, None otherwise
        """
        if connector_type == SearchSourceConnectorType.MCP_CONNECTOR:
            query = select(SearchSourceConnector).filter(
                SearchSourceConnector.workspace_id == workspace_id,
                SearchSourceConnector.connector_type == connector_type,
            )
            result = await self.session.execute(query)
            return result.scalars().first()

        candidates = await enabled_connectors_in_workspace(
            self.session, workspace_id, connector_type
        )
        return candidates[0] if candidates else None

    async def search_extension(
        self,
        user_query: str,
        workspace_id: int,
        top_k: int = 20,
        start_date: datetime | None = None,
        end_date: datetime | None = None,
    ) -> tuple:
        """
        Search for extension data and return both the source information and langchain documents.

        Uses combined chunk-level and document-level hybrid search with RRF fusion.

        Args:
            user_query: The user's query
            workspace_id: The workspace ID to search in
            top_k: Maximum number of results to return
            start_date: Optional start date for filtering documents by updated_at
            end_date: Optional end date for filtering documents by updated_at

        Returns:
            tuple: (sources_info, langchain_documents)
        """
        extension_docs = await self._combined_rrf_search(
            query_text=user_query,
            workspace_id=workspace_id,
            document_type="EXTENSION",
            top_k=top_k,
            start_date=start_date,
            end_date=end_date,
        )

        # Early return if no results
        if not extension_docs:
            return {
                "id": 6,
                "name": "Extension",
                "type": "EXTENSION",
                "sources": [],
            }, []

        def _title_fn(_doc_info: dict[str, Any], metadata: dict[str, Any]) -> str:
            webpage_title = metadata.get("VisitedWebPageTitle", "Untitled Page")
            visit_date = metadata.get("VisitedWebPageDateWithTimeInISOString", "")
            title = webpage_title
            if visit_date:
                try:
                    formatted_date = (
                        visit_date.split("T")[0] if "T" in visit_date else visit_date
                    )
                    title += f" (visited: {formatted_date})"
                except Exception:
                    title += f" (visited: {visit_date})"
            return title

        def _url_fn(_doc_info: dict[str, Any], metadata: dict[str, Any]) -> str:
            return metadata.get("VisitedWebPageURL", "") or ""

        def _description_fn(
            chunk: dict[str, Any], _doc_info: dict[str, Any], metadata: dict[str, Any]
        ) -> str:
            description = chunk.get("content", "")
            visit_duration = metadata.get(
                "VisitedWebPageVisitDurationInMilliseconds", ""
            )
            if visit_duration:
                try:
                    duration_seconds = int(visit_duration) / 1000
                    duration_text = (
                        f"{duration_seconds:.1f} seconds"
                        if duration_seconds < 60
                        else f"{duration_seconds / 60:.1f} minutes"
                    )
                    description = (description + f" | Duration: {duration_text}").strip(
                        " |"
                    )
                except Exception:
                    pass
            return description

        sources_list = self._build_chunk_sources_from_documents(
            extension_docs,
            title_fn=_title_fn,
            url_fn=_url_fn,
            description_fn=_description_fn,
        )

        # Create result object
        result_object = {
            "id": 6,
            "name": "Extension",
            "type": "EXTENSION",
            "sources": sources_list,
        }

        return result_object, extension_docs

    async def search_youtube(
        self,
        user_query: str,
        workspace_id: int,
        top_k: int = 20,
        start_date: datetime | None = None,
        end_date: datetime | None = None,
    ) -> tuple:
        """
        Search for YouTube videos and return both the source information and langchain documents.

        Uses combined chunk-level and document-level hybrid search with RRF fusion.

        Args:
            user_query: The user's query
            workspace_id: The workspace ID to search in
            top_k: Maximum number of results to return
            start_date: Optional start date for filtering documents by updated_at
            end_date: Optional end date for filtering documents by updated_at

        Returns:
            tuple: (sources_info, langchain_documents)
        """
        youtube_docs = await self._combined_rrf_search(
            query_text=user_query,
            workspace_id=workspace_id,
            document_type="YOUTUBE_VIDEO",
            top_k=top_k,
            start_date=start_date,
            end_date=end_date,
        )

        # Early return if no results
        if not youtube_docs:
            return {
                "id": 7,
                "name": "YouTube Videos",
                "type": "YOUTUBE_VIDEO",
                "sources": [],
            }, []

        def _title_fn(_doc_info: dict[str, Any], metadata: dict[str, Any]) -> str:
            video_title = metadata.get("video_title", "Untitled Video")
            channel_name = metadata.get("channel_name", "")
            return f"{video_title} - {channel_name}" if channel_name else video_title

        def _url_fn(_doc_info: dict[str, Any], metadata: dict[str, Any]) -> str:
            video_id = metadata.get("video_id", "")
            return f"https://www.youtube.com/watch?v={video_id}" if video_id else ""

        def _description_fn(
            chunk: dict[str, Any], _doc_info: dict[str, Any], metadata: dict[str, Any]
        ) -> str:
            return metadata.get("description") or chunk.get("content", "")

        def _extra_fields_fn(
            _chunk: dict[str, Any], _doc_info: dict[str, Any], metadata: dict[str, Any]
        ) -> dict[str, Any]:
            return {
                "video_id": metadata.get("video_id", ""),
                "channel_name": metadata.get("channel_name", ""),
            }

        sources_list = self._build_chunk_sources_from_documents(
            youtube_docs,
            title_fn=_title_fn,
            url_fn=_url_fn,
            description_fn=_description_fn,
            extra_fields_fn=_extra_fields_fn,
        )

        # Create result object
        result_object = {
            "id": 7,  # Assign a unique ID for the YouTube connector
            "name": "YouTube Videos",
            "type": "YOUTUBE_VIDEO",
            "sources": sources_list,
        }

        return result_object, youtube_docs

    async def search_google_drive(
        self,
        user_query: str,
        workspace_id: int,
        top_k: int = 20,
        start_date: datetime | None = None,
        end_date: datetime | None = None,
    ) -> tuple:
        """
        Search for Google Drive files and return both the source information and langchain documents.

        Uses combined chunk-level and document-level hybrid search with RRF fusion.

        Args:
            user_query: The user's query
            workspace_id: The workspace ID to search in
            top_k: Maximum number of results to return
            start_date: Optional start date for filtering documents by updated_at
            end_date: Optional end date for filtering documents by updated_at

        Returns:
            tuple: (sources_info, langchain_documents)
        """
        drive_docs = await self._combined_rrf_search(
            query_text=user_query,
            workspace_id=workspace_id,
            document_type="GOOGLE_DRIVE_FILE",
            top_k=top_k,
            start_date=start_date,
            end_date=end_date,
        )

        # Early return if no results
        if not drive_docs:
            return {
                "id": 33,
                "name": "Google Drive Files",
                "type": "GOOGLE_DRIVE_FILE",
                "sources": [],
            }, []

        def _title_fn(doc_info: dict[str, Any], metadata: dict[str, Any]) -> str:
            return (
                doc_info.get("title")
                or metadata.get("google_drive_file_name")
                or metadata.get("FILE_NAME")
                or "Untitled File"
            )

        def _url_fn(_doc_info: dict[str, Any], metadata: dict[str, Any]) -> str:
            file_id = metadata.get("google_drive_file_id", "")
            return f"https://drive.google.com/file/d/{file_id}/view" if file_id else ""

        def _description_fn(
            chunk: dict[str, Any], _doc_info: dict[str, Any], metadata: dict[str, Any]
        ) -> str:
            description = self._chunk_preview(chunk.get("content", ""))
            info_parts = []
            mime_type = metadata.get("google_drive_mime_type", "")
            modified_time = metadata.get("modified_time", "")
            if mime_type:
                # Simplify mime type for display
                if "google-apps" in mime_type:
                    file_type = mime_type.split(".")[-1].title()
                else:
                    file_type = mime_type.split("/")[-1].upper()
                info_parts.append(f"Type: {file_type}")
            if modified_time:
                info_parts.append(f"Modified: {modified_time}")
            if info_parts:
                description = (description + " | " + " | ".join(info_parts)).strip(" |")
            return description

        def _extra_fields_fn(
            _chunk: dict[str, Any], _doc_info: dict[str, Any], metadata: dict[str, Any]
        ) -> dict[str, Any]:
            return {
                "google_drive_file_id": metadata.get("google_drive_file_id", ""),
                "google_drive_mime_type": metadata.get("google_drive_mime_type", ""),
                "modified_time": metadata.get("modified_time", ""),
            }

        sources_list = self._build_chunk_sources_from_documents(
            drive_docs,
            title_fn=_title_fn,
            url_fn=_url_fn,
            description_fn=_description_fn,
            extra_fields_fn=_extra_fields_fn,
        )

        # Create result object
        result_object = {
            "id": 33,  # Assign a unique ID for the Google Drive connector
            "name": "Google Drive Files",
            "type": "GOOGLE_DRIVE_FILE",
            "sources": sources_list,
        }

        return result_object, drive_docs

    async def search_notes(
        self,
        user_query: str,
        workspace_id: int,
        top_k: int = 20,
        start_date: datetime | None = None,
        end_date: datetime | None = None,
    ) -> tuple:
        """
        Search for Notes and return both the source information and langchain documents.

        Uses combined chunk-level and document-level hybrid search with RRF fusion.

        Args:
            user_query: The user's query
            workspace_id: The workspace ID to search in
            top_k: Maximum number of results to return
            start_date: Optional start date for filtering documents by updated_at
            end_date: Optional end date for filtering documents by updated_at

        Returns:
            tuple: (sources_info, langchain_documents)
        """
        notes_docs = await self._combined_rrf_search(
            query_text=user_query,
            workspace_id=workspace_id,
            document_type="NOTE",
            top_k=top_k,
            start_date=start_date,
            end_date=end_date,
        )

        # Early return if no results
        if not notes_docs:
            return {
                "id": 51,
                "name": "Notes",
                "type": "NOTE",
                "sources": [],
            }, []

        def _title_fn(doc_info: dict[str, Any], metadata: dict[str, Any]) -> str:
            return doc_info.get("title", "Untitled Note")

        def _url_fn(_doc_info: dict[str, Any], _metadata: dict[str, Any]) -> str:
            return ""  # Notes don't have URLs

        def _description_fn(
            chunk: dict[str, Any], _doc_info: dict[str, Any], _metadata: dict[str, Any]
        ) -> str:
            return self._chunk_preview(chunk.get("content", ""), limit=200)

        sources_list = self._build_chunk_sources_from_documents(
            notes_docs,
            title_fn=_title_fn,
            url_fn=_url_fn,
            description_fn=_description_fn,
        )

        # Create result object
        result_object = {
            "id": 51,
            "name": "Notes",
            "type": "NOTE",
            "sources": sources_list,
        }

        return result_object, notes_docs

    async def get_available_connectors(
        self,
        workspace_id: int,
    ) -> list[SearchSourceConnectorType]:
        """
        Get all available (enabled) connector types for a workspace.

        Account connections (everything but ``MCP_CONNECTOR``) are enabled
        in a workspace whenever their owner belongs to it, unless the owner
        switched it off here — see ``app/services/connector_enablement.py``.
        ``MCP_CONNECTOR`` keeps its own workspace-scoped row, queried as
        before, untouched by that module.

        Phase 1.4: results are cached per ``workspace_id`` for
        :data:`_DISCOVERY_TTL_SECONDS`. Cache key is independent of session
        identity — the cached value is plain data, safe to share across
        requests. Invalidate on connector add/update/delete via
        :func:`invalidate_connector_discovery_cache`.

        Args:
            workspace_id: The workspace ID

        Returns:
            List of SearchSourceConnectorType enums for enabled connectors
        """
        cached = _get_cached_connectors(workspace_id)
        if cached is not None:
            return list(cached)

        account_connectors = await enabled_connectors_in_workspace(
            self.session, workspace_id
        )
        connector_types = {c.connector_type for c in account_connectors}

        mcp_query = select(SearchSourceConnector.connector_type).filter(
            SearchSourceConnector.workspace_id == workspace_id,
            SearchSourceConnector.connector_type
            == SearchSourceConnectorType.MCP_CONNECTOR,
        )
        result = await self.session.execute(mcp_query)
        connector_types.update(result.scalars().all())

        connector_types = list(connector_types)
        _set_cached_connectors(workspace_id, connector_types)
        return connector_types

    async def get_available_document_types(
        self,
        workspace_id: int,
    ) -> list[str]:
        """
        Get all document types that have at least one document in the workspace.

        Phase 1.4: cached per ``workspace_id`` for
        :data:`_DISCOVERY_TTL_SECONDS`. Invalidate via
        :func:`invalidate_connector_discovery_cache` when a connector
        finishes indexing new documents (or document types are otherwise
        added/removed).

        Args:
            workspace_id: The workspace ID

        Returns:
            List of document type strings that have documents indexed
        """
        cached = _get_cached_doc_types(workspace_id)
        if cached is not None:
            return list(cached)

        from sqlalchemy import distinct

        from app.db import Document

        query = select(distinct(Document.document_type)).filter(
            Document.workspace_id == workspace_id,
        )

        result = await self.session.execute(query)
        doc_types = [str(dt) for dt in result.scalars().all()]
        _set_cached_doc_types(workspace_id, doc_types)
        return doc_types


# ---------------------------------------------------------------------------
# Connector / document-type discovery TTL cache (Phase 1.4)
# ---------------------------------------------------------------------------
#
# Both ``get_available_connectors`` and ``get_available_document_types`` are
# called on EVERY chat turn from ``create_surfsense_deep_agent``. Each query
# hits Postgres and contributes to per-turn agent build latency. Their
# results change infrequently — only when the user adds/edits/removes a
# connector, or when an indexer commits a new document type. A short TTL
# cache (default 30s, env-tunable) collapses N concurrent calls into one
# DB roundtrip with bounded staleness.
#
# Invalidation: connector mutation routes (create / update / delete) call
# ``invalidate_connector_discovery_cache(workspace_id)`` to clear the
# entry for the affected space. Multi-replica deployments still pay one
# DB roundtrip per replica per TTL window, which is fine — staleness is
# bounded and the alternative (cross-replica fanout) is not worth the
# coupling here.

_DISCOVERY_TTL_SECONDS: float = config.CONNECTOR_DISCOVERY_TTL_SECONDS

# Per-workspace caches. Keyed by ``workspace_id``; value is
# ``(expires_at_monotonic, payload)``. Plain dicts protected by a lock —
# read-mostly workload, sub-microsecond contention.
_connectors_cache: dict[int, tuple[float, list[SearchSourceConnectorType]]] = {}
_doc_types_cache: dict[int, tuple[float, list[str]]] = {}
_cache_lock = Lock()


def _get_cached_connectors(
    workspace_id: int,
) -> list[SearchSourceConnectorType] | None:
    if _DISCOVERY_TTL_SECONDS <= 0:
        return None
    with _cache_lock:
        entry = _connectors_cache.get(workspace_id)
        if entry is None:
            return None
        expires_at, payload = entry
        if time.monotonic() >= expires_at:
            _connectors_cache.pop(workspace_id, None)
            return None
        return payload


def _set_cached_connectors(
    workspace_id: int, payload: list[SearchSourceConnectorType]
) -> None:
    if _DISCOVERY_TTL_SECONDS <= 0:
        return
    expires_at = time.monotonic() + _DISCOVERY_TTL_SECONDS
    with _cache_lock:
        _connectors_cache[workspace_id] = (expires_at, list(payload))


def _get_cached_doc_types(workspace_id: int) -> list[str] | None:
    if _DISCOVERY_TTL_SECONDS <= 0:
        return None
    with _cache_lock:
        entry = _doc_types_cache.get(workspace_id)
        if entry is None:
            return None
        expires_at, payload = entry
        if time.monotonic() >= expires_at:
            _doc_types_cache.pop(workspace_id, None)
            return None
        return payload


def _set_cached_doc_types(workspace_id: int, payload: list[str]) -> None:
    if _DISCOVERY_TTL_SECONDS <= 0:
        return
    expires_at = time.monotonic() + _DISCOVERY_TTL_SECONDS
    with _cache_lock:
        _doc_types_cache[workspace_id] = (expires_at, list(payload))


def invalidate_connector_discovery_cache(workspace_id: int | None = None) -> None:
    """Drop cached discovery results for ``workspace_id`` (or all spaces).

    Connector CRUD routes / indexer pipelines call this when they mutate
    the rows backing :func:`ConnectorService.get_available_connectors` /
    :func:`get_available_document_types`. ``None`` clears every space —
    useful in tests and on bulk imports.
    """
    with _cache_lock:
        if workspace_id is None:
            _connectors_cache.clear()
            _doc_types_cache.clear()
        else:
            _connectors_cache.pop(workspace_id, None)
            _doc_types_cache.pop(workspace_id, None)


def _invalidate_connectors_only(workspace_id: int | None = None) -> None:
    with _cache_lock:
        if workspace_id is None:
            _connectors_cache.clear()
        else:
            _connectors_cache.pop(workspace_id, None)


def _invalidate_doc_types_only(workspace_id: int | None = None) -> None:
    with _cache_lock:
        if workspace_id is None:
            _doc_types_cache.clear()
        else:
            _doc_types_cache.pop(workspace_id, None)


def _register_invalidation_listeners() -> None:
    """Wire SQLAlchemy ORM events so cache stays consistent automatically.

    Listening on ``after_insert`` / ``after_update`` / ``after_delete``
    means every successful INSERT/UPDATE/DELETE that goes through the ORM
    invalidates the affected workspace's cached discovery payload —
    no need to sprinkle ``invalidate_*`` calls across 30+ connector
    routes. Bulk operations that bypass the ORM (e.g.
    ``session.execute(insert(...))`` without a mapped object) still need
    explicit invalidation; document indexers already commit through the
    ORM so document-type discovery is covered.
    """
    from sqlalchemy import event

    # Imported here (not at module top) to avoid a circular import:
    # app.services.connector_service is itself imported from app.db's
    # ecosystem indirectly via several CRUD modules.
    from app.db import Document, SearchSourceConnector

    def _connector_changed(_mapper, _connection, target) -> None:
        sid = getattr(target, "workspace_id", None)
        if sid is not None:
            _invalidate_connectors_only(int(sid))

    def _document_changed(_mapper, _connection, target) -> None:
        sid = getattr(target, "workspace_id", None)
        if sid is not None:
            _invalidate_doc_types_only(int(sid))

    for evt in ("after_insert", "after_update", "after_delete"):
        event.listen(SearchSourceConnector, evt, _connector_changed)
        event.listen(Document, evt, _document_changed)


try:
    _register_invalidation_listeners()
except Exception:  # pragma: no cover - defensive; never block module import
    import logging as _logging

    _logging.getLogger(__name__).exception(
        "Failed to register connector discovery cache invalidation listeners; "
        "stale cache risk: explicit invalidate_connector_discovery_cache calls "
        "may be required."
    )
