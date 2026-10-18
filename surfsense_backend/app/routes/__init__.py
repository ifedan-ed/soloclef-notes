from fastapi import APIRouter

# Imported for their registration side effects before the door builds: verb
# namespaces.
import app.capabilities.amazon
import app.capabilities.google_maps
import app.capabilities.google_search
import app.capabilities.indeed
import app.capabilities.instagram
import app.capabilities.reddit
import app.capabilities.tiktok
import app.capabilities.walmart
import app.capabilities.web
import app.capabilities.youtube  # noqa: F401
from app.agent_sign_in.routes import router as agent_sign_in_router
from app.app_handoff.routes import router as app_handoff_router
from app.artifacts.access.authenticated import build_authenticated_artifact_router
from app.artifacts.media_links.routes import router as artifact_media_links_router
from app.automations.api import router as automations_router
from app.billing.routes import router as billing_router
from app.capabilities.core.access.rest import build_capabilities_router
from app.connectors.email_imap.microsoft.routes import (
    router as microsoft_mail_router,
)
from app.connectors.email_imap.routes import router as email_imap_router
from app.connectors.google_drive.connect.routes import (
    router as google_drive_connect_router,
)
from app.connectors.google_drive.notebook_import.routes import (
    router as google_drive_import_router,
)
from app.connectors.nextcloud.login_flow.routes import (
    router as nextcloud_connect_router,
)
from app.connectors.nextcloud.routes import router as nextcloud_files_router
from app.file_storage.api import router as file_storage_router
from app.knowledge_store.remote.api import router as git_remotes_router
from app.mcp_security.routes import router as mcp_security_router
from app.notebooks.routes import router as notebooks_router
from app.notifications.api import router as notifications_router
from app.podcasts.api import router as podcasts_router
from app.public_sharing.notebook_links.routes import (
    router as notebook_links_router,
)
from app.push.routes import router as push_router
from app.quick_tasks.routes import router as quick_tasks_router
from app.sign_in_options.routes import router as sign_in_options_router
from app.site_announcements.routes import router as site_announcements_router
from app.studio_models.routes import router as studio_models_router
from app.support_contact.routes import router as support_contact_router
from app.workspace_instructions.routes import router as workspace_instructions_router
from app.workspace_notes.routes import router as workspace_notes_router

from .agent_action_log_route import router as agent_action_log_router
from .agent_flags_route import router as agent_flags_router
from .agent_permissions_route import router as agent_permissions_router
from .agent_revert_route import router as agent_revert_router
from .artifacts_routes import router as artifacts_router
from .chat_comments_routes import router as chat_comments_router
from .deliverable_jobs_routes import router as deliverable_jobs_router
from .document_files_routes import router as document_files_router
from .documents_routes import router as documents_router
from .editor_routes import router as editor_router
from .etl_routes import router as etl_router
from .export_routes import router as export_router
from .folders_routes import router as folders_router
from .logs_routes import router as logs_router
from .memory_routes import router as memory_router
from .model_connections_routes import router as model_connections_router
from .model_list_routes import router as model_list_router
from .new_chat_routes import router as new_chat_router
from .notes_routes import router as notes_router
from .personal_access_tokens_routes import router as personal_access_tokens_router
from .prompts_routes import router as prompts_router
from .public_chat_routes import router as public_chat_router
from .rbac_routes import router as rbac_router
from .sandbox_routes import router as sandbox_router
from .search_source_connectors_routes import router as search_source_connectors_router
from .team_memory_routes import router as team_memory_router
from .workspaces_routes import router as workspaces_router
from .youtube_routes import router as youtube_router

router = APIRouter()

router.include_router(workspaces_router)
router.include_router(workspace_instructions_router)
router.include_router(git_remotes_router)
router.include_router(rbac_router)  # RBAC routes for roles, members, invites
router.include_router(editor_router)
router.include_router(export_router)
router.include_router(documents_router)
router.include_router(document_files_router)
router.include_router(artifacts_router)
router.include_router(artifact_media_links_router)
router.include_router(deliverable_jobs_router)
router.include_router(folders_router)
router.include_router(notes_router)
router.include_router(notebooks_router)
router.include_router(workspace_notes_router)  # the Notes page
router.include_router(nextcloud_files_router)
router.include_router(email_imap_router)
router.include_router(microsoft_mail_router)  # Email (IMAP): Microsoft sign-in
router.include_router(nextcloud_connect_router)
router.include_router(new_chat_router)  # Chat with assistant-ui persistence
router.include_router(agent_revert_router)  # POST /threads/{id}/revert/{action_id}
router.include_router(agent_action_log_router)  # GET /threads/{id}/actions
router.include_router(
    agent_permissions_router
)  # CRUD for /workspaces/{id}/agent/permissions/rules
router.include_router(agent_flags_router)  # GET /agent/flags
router.include_router(etl_router)  # GET /etl/capabilities
router.include_router(sandbox_router)  # Sandbox file downloads
router.include_router(chat_comments_router)
router.include_router(podcasts_router)  # Podcast task status and audio
# Before the connectors router so /connectors/mcp/policy is not read as an id.
router.include_router(mcp_security_router)
router.include_router(agent_sign_in_router)
router.include_router(search_source_connectors_router)
router.include_router(google_drive_connect_router)
router.include_router(google_drive_import_router)
# The phone app's browser hand-off: the Drive Picker (B2, B6), Drive connect
# and Microsoft mail (B10).
router.include_router(app_handoff_router)
router.include_router(personal_access_tokens_router)  # Personal access token manager
router.include_router(model_connections_router)  # Connection-centric model catalog
router.include_router(quick_tasks_router)  # Quick-tasks model (Settings -> Models)
router.include_router(studio_models_router)  # Studio outputs model (Settings -> Models)
router.include_router(model_list_router)  # Dynamic model catalogue from OpenRouter
router.include_router(logs_router)
router.include_router(notifications_router)  # Notifications with Zero sync
router.include_router(site_announcements_router)  # Admin announcements to everyone
router.include_router(push_router)  # The phone app's push tokens and choices
router.include_router(public_chat_router)  # Public chat sharing and cloning
router.include_router(notebook_links_router)  # Live, read-only notebook links
router.include_router(billing_router)  # Plans, credits, Stripe and crypto payments
router.include_router(support_contact_router)  # The Report Issue address (public)
router.include_router(sign_in_options_router)  # How to sign in here (public)
router.include_router(youtube_router)  # YouTube playlist resolution
router.include_router(prompts_router)
router.include_router(memory_router)  # User personal memory (memory.md style)
router.include_router(team_memory_router)  # Workspace team memory
router.include_router(automations_router)  # Automations CRUD + run history
router.include_router(file_storage_router)  # Original file metadata + download
router.include_router(build_capabilities_router())  # Scraper-API capability doors (05)
router.include_router(
    build_authenticated_artifact_router()
)  # Authenticated artifact generation (dev API)
