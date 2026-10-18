"""
RBAC (Role-Based Access Control) routes for managing roles, memberships, and invites.

Endpoints:
- /workspaces/{workspace_id}/roles - CRUD for roles
- /workspaces/{workspace_id}/members - CRUD for memberships
- /workspaces/{workspace_id}/invites - CRUD for invites
- /invites/{invite_code}/info - Get invite info (public)
- /invites/accept - Accept an invite
- /permissions - List all available permissions
"""

import logging
from datetime import UTC, datetime, timedelta

from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy import func
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.future import select
from sqlalchemy.orm import selectinload

from app.auth.context import AuthContext
from app.db import (
    Permission,
    User,
    Workspace,
    WorkspaceInvite,
    WorkspaceMembership,
    WorkspaceRole,
    get_async_session,
)
from app.mcp_security.member_servers import delete_member_servers, forget_servers
from app.observability.analytics import posthog as ph_analytics
from app.push import events as push_events
from app.schemas import (
    InviteAcceptRequest,
    InviteAcceptResponse,
    InviteCreate,
    InviteInfoResponse,
    InviteRead,
    InviteUpdate,
    MembershipRead,
    MembershipUpdate,
    PermissionInfo,
    PermissionsListResponse,
    RoleCreate,
    RoleRead,
    RoleUpdate,
    UserWorkspaceAccess,
)
from app.users import get_auth_context
from app.utils.rbac import (
    check_permission,
    check_workspace_access,
    generate_invite_code,
    get_default_role,
    get_user_permissions,
)
from app.workspace_invites.addresses import (
    mask_address,
    normalize_address,
    same_address,
)
from app.workspace_invites.delivery import send_invite_email
from app.workspace_invites.message import build_invite_email
from app.workspace_invites.status import invite_status
from app.workspace_membership.last_workspace import (
    LAST_WORKSPACE_LEAVE_DETAIL,
    refuse_if_last_workspace,
)
from app.workspace_roles.grants import (
    can_grant_role,
    can_manage_member,
    grants_full_access,
    role_permissions_allowed,
)

logger = logging.getLogger(__name__)

router = APIRouter()

_ROLE_EDIT_DENIED = (
    "Only the workspace Owner can change roles, and no role can grant full access"
)
_ROLE_ABOVE_YOU = (
    "You can only give someone a role at or below your own. "
    "Ownership cannot be granted."
)
_MEMBER_ABOVE_YOU = "You can only change members whose role is at or below your own"
_RESEND_COOLDOWN = timedelta(seconds=60)


# ============ Permissions Endpoints ============

# Human-readable descriptions for each permission
PERMISSION_DESCRIPTIONS = {
    # Documents
    "documents:create": "Add new documents, files, and content to the workspace",
    "documents:read": "View and search documents in the workspace",
    "documents:update": "Edit existing documents and their metadata",
    "documents:delete": "Remove documents from the workspace",
    # Artifacts
    "artifacts:create": "Create generated artifacts in the workspace",
    "artifacts:read": "View and download artifacts in the workspace",
    "artifacts:update": "Revise existing artifacts in the workspace",
    "artifacts:delete": "Remove artifacts from the workspace",
    # Chats
    "chats:create": "Start new AI chat conversations",
    "chats:read": "View chat history and conversations",
    "chats:update": "Edit chat titles and settings",
    "chats:delete": "Delete chat conversations",
    # Comments
    "comments:create": "Add comments and annotations to documents",
    "comments:read": "View comments on documents",
    "comments:delete": "Remove comments from documents",
    # LLM Configs
    "llm_configs:create": "Add new AI model configurations",
    "llm_configs:read": "View AI model settings and configurations",
    "llm_configs:update": "Modify AI model configurations",
    "llm_configs:delete": "Remove AI model configurations",
    # Podcasts
    "podcasts:create": "Generate new AI podcasts from content",
    "podcasts:read": "Listen to and view generated podcasts",
    "podcasts:update": "Edit podcast settings and metadata",
    "podcasts:delete": "Remove generated podcasts",
    # Connectors
    "connectors:create": "Set up new data source integrations",
    "connectors:read": "View configured data sources and their status",
    "connectors:update": "Modify data source configurations",
    "connectors:delete": "Remove data source integrations",
    # Logs
    "logs:read": "View activity logs and audit trail",
    "logs:delete": "Clear activity logs",
    # Members
    "members:invite": "Send invitations to new team members",
    "members:view": "View the list of team members",
    "members:remove": "Remove members from the workspace",
    "members:manage_roles": "Assign and change member roles",
    # Roles
    "roles:create": "Create new custom roles",
    "roles:read": "View available roles and their permissions",
    "roles:update": "Modify role permissions",
    "roles:delete": "Remove custom roles",
    # Settings
    "settings:view": "View workspace settings",
    "settings:update": "Modify workspace settings",
    "settings:delete": "Delete the entire workspace",
    # API access
    "api_access:manage": "Enable or disable programmatic API access for a workspace",
    # Automations
    "automations:create": "Create automations from chat or JSON",
    "automations:read": "View automations, their triggers, and run history",
    "automations:update": "Edit automations and manage their triggers",
    "automations:delete": "Remove automations from the workspace",
    "automations:execute": "Manually fire automations",
    # Full access
    "*": "Full access to all features and settings",
}


@router.get("/permissions", response_model=PermissionsListResponse)
async def list_all_permissions(
    auth: AuthContext = Depends(get_auth_context),
):
    """
    List all available permissions that can be assigned to roles.
    """
    permissions = []
    for perm in Permission:
        # Extract category from permission value (e.g., "documents:read" -> "documents")
        category = perm.value.split(":")[0] if ":" in perm.value else "general"
        description = PERMISSION_DESCRIPTIONS.get(
            perm.value, f"Permission for {perm.value}"
        )

        permissions.append(
            PermissionInfo(
                value=perm.value,
                name=perm.name,
                category=category,
                description=description,
            )
        )

    return PermissionsListResponse(permissions=permissions)


# ============ Role Endpoints ============


@router.post(
    "/workspaces/{workspace_id}/roles",
    response_model=RoleRead,
)
async def create_role(
    workspace_id: int,
    role_data: RoleCreate,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """
    Create a new custom role in a workspace.
    Requires ROLES_CREATE permission.
    """
    try:
        actor = await check_permission(
            session,
            auth,
            workspace_id,
            Permission.ROLES_CREATE.value,
            "You don't have permission to create roles",
        )
        if not role_permissions_allowed(actor, role_data.permissions):
            raise HTTPException(status_code=403, detail=_ROLE_EDIT_DENIED)

        # Check if role with same name already exists
        result = await session.execute(
            select(WorkspaceRole).filter(
                WorkspaceRole.workspace_id == workspace_id,
                WorkspaceRole.name == role_data.name,
            )
        )
        if result.scalars().first():
            raise HTTPException(
                status_code=409,
                detail=f"A role with name '{role_data.name}' already exists in this workspace",
            )

        # Validate permissions
        valid_permissions = {p.value for p in Permission}
        for perm in role_data.permissions:
            if perm not in valid_permissions:
                raise HTTPException(
                    status_code=400,
                    detail=f"Invalid permission: {perm}",
                )

        # If setting is_default to True, unset any existing default
        if role_data.is_default:
            await session.execute(
                select(WorkspaceRole).filter(
                    WorkspaceRole.workspace_id == workspace_id,
                    WorkspaceRole.is_default == True,  # noqa: E712
                )
            )
            existing_defaults = await session.execute(
                select(WorkspaceRole).filter(
                    WorkspaceRole.workspace_id == workspace_id,
                    WorkspaceRole.is_default == True,  # noqa: E712
                )
            )
            for existing in existing_defaults.scalars().all():
                existing.is_default = False

        db_role = WorkspaceRole(
            **role_data.model_dump(),
            workspace_id=workspace_id,
            is_system_role=False,
        )
        session.add(db_role)
        await session.commit()
        await session.refresh(db_role)
        return db_role

    except HTTPException:
        raise
    except Exception as e:
        await session.rollback()
        logger.error(f"Failed to create role: {e!s}", exc_info=True)
        raise HTTPException(
            status_code=500, detail=f"Failed to create role: {e!s}"
        ) from e


@router.get(
    "/workspaces/{workspace_id}/roles",
    response_model=list[RoleRead],
)
async def list_roles(
    workspace_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """
    List all roles in a workspace.
    Requires ROLES_READ permission.
    """
    try:
        await check_permission(
            session,
            auth,
            workspace_id,
            Permission.ROLES_READ.value,
            "You don't have permission to view roles",
        )

        result = await session.execute(
            select(WorkspaceRole).filter(WorkspaceRole.workspace_id == workspace_id)
        )
        return result.scalars().all()

    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(
            status_code=500, detail=f"Failed to fetch roles: {e!s}"
        ) from e


@router.get(
    "/workspaces/{workspace_id}/roles/{role_id}",
    response_model=RoleRead,
)
async def get_role(
    workspace_id: int,
    role_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """
    Get a specific role by ID.
    Requires ROLES_READ permission.
    """
    try:
        await check_permission(
            session,
            auth,
            workspace_id,
            Permission.ROLES_READ.value,
            "You don't have permission to view roles",
        )

        result = await session.execute(
            select(WorkspaceRole).filter(
                WorkspaceRole.id == role_id,
                WorkspaceRole.workspace_id == workspace_id,
            )
        )
        role = result.scalars().first()

        if not role:
            raise HTTPException(status_code=404, detail="Role not found")

        return role

    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(
            status_code=500, detail=f"Failed to fetch role: {e!s}"
        ) from e


@router.put(
    "/workspaces/{workspace_id}/roles/{role_id}",
    response_model=RoleRead,
)
async def update_role(
    workspace_id: int,
    role_id: int,
    role_update: RoleUpdate,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """
    Update a role.
    Requires ROLES_UPDATE permission.
    System roles can only have their permissions updated, not name/description.
    """
    try:
        actor = await check_permission(
            session,
            auth,
            workspace_id,
            Permission.ROLES_UPDATE.value,
            "You don't have permission to update roles",
        )
        if not role_permissions_allowed(actor, role_update.permissions or []):
            raise HTTPException(status_code=403, detail=_ROLE_EDIT_DENIED)

        result = await session.execute(
            select(WorkspaceRole).filter(
                WorkspaceRole.id == role_id,
                WorkspaceRole.workspace_id == workspace_id,
            )
        )
        db_role = result.scalars().first()

        if not db_role:
            raise HTTPException(status_code=404, detail="Role not found")

        update_data = role_update.model_dump(exclude_unset=True)

        if grants_full_access(db_role):
            raise HTTPException(
                status_code=400, detail="The Owner role cannot be changed"
            )

        # System roles have restrictions on what can be updated
        if db_role.is_system_role:
            # Can only update permissions for system roles
            restricted_fields = {"name", "description", "is_default"}
            if any(field in update_data for field in restricted_fields):
                raise HTTPException(
                    status_code=400,
                    detail="Cannot modify name, description, or default status of system roles",
                )

        # Check for name conflict if updating name
        if "name" in update_data and update_data["name"] != db_role.name:
            existing = await session.execute(
                select(WorkspaceRole).filter(
                    WorkspaceRole.workspace_id == workspace_id,
                    WorkspaceRole.name == update_data["name"],
                )
            )
            if existing.scalars().first():
                raise HTTPException(
                    status_code=409,
                    detail=f"A role with name '{update_data['name']}' already exists",
                )

        # Validate permissions if provided
        if "permissions" in update_data:
            valid_permissions = {p.value for p in Permission}
            for perm in update_data["permissions"]:
                if perm not in valid_permissions:
                    raise HTTPException(
                        status_code=400,
                        detail=f"Invalid permission: {perm}",
                    )

        # Handle is_default change
        if update_data.get("is_default") and not db_role.is_default:
            # Unset existing default
            existing_defaults = await session.execute(
                select(WorkspaceRole).filter(
                    WorkspaceRole.workspace_id == workspace_id,
                    WorkspaceRole.is_default == True,  # noqa: E712
                )
            )
            for existing in existing_defaults.scalars().all():
                existing.is_default = False

        for key, value in update_data.items():
            setattr(db_role, key, value)

        await session.commit()
        await session.refresh(db_role)
        return db_role

    except HTTPException:
        raise
    except Exception as e:
        await session.rollback()
        logger.error(f"Failed to update role: {e!s}", exc_info=True)
        raise HTTPException(
            status_code=500, detail=f"Failed to update role: {e!s}"
        ) from e


@router.delete("/workspaces/{workspace_id}/roles/{role_id}")
async def delete_role(
    workspace_id: int,
    role_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """
    Delete a custom role.
    Requires ROLES_DELETE permission.
    System roles cannot be deleted.
    """
    try:
        actor = await check_permission(
            session,
            auth,
            workspace_id,
            Permission.ROLES_DELETE.value,
            "You don't have permission to delete roles",
        )
        if not actor.is_owner:
            raise HTTPException(status_code=403, detail=_ROLE_EDIT_DENIED)

        result = await session.execute(
            select(WorkspaceRole).filter(
                WorkspaceRole.id == role_id,
                WorkspaceRole.workspace_id == workspace_id,
            )
        )
        db_role = result.scalars().first()

        if not db_role:
            raise HTTPException(status_code=404, detail="Role not found")

        if db_role.is_system_role:
            raise HTTPException(
                status_code=400,
                detail="System roles cannot be deleted",
            )

        await session.delete(db_role)
        await session.commit()
        return {"message": "Role deleted successfully"}

    except HTTPException:
        raise
    except Exception as e:
        await session.rollback()
        logger.error(f"Failed to delete role: {e!s}", exc_info=True)
        raise HTTPException(
            status_code=500, detail=f"Failed to delete role: {e!s}"
        ) from e


# ============ Membership Endpoints ============


@router.get(
    "/workspaces/{workspace_id}/members",
    response_model=list[MembershipRead],
)
async def list_members(
    workspace_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """
    List all members of a workspace.
    Requires MEMBERS_VIEW permission.
    """
    try:
        await check_permission(
            session,
            auth,
            workspace_id,
            Permission.MEMBERS_VIEW.value,
            "You don't have permission to view members",
        )

        result = await session.execute(
            select(WorkspaceMembership)
            .options(selectinload(WorkspaceMembership.role))
            .filter(WorkspaceMembership.workspace_id == workspace_id)
        )
        memberships = result.scalars().all()

        # Fetch user emails for each membership
        response = []
        for membership in memberships:
            user_result = await session.execute(
                select(User).filter(User.id == membership.user_id)
            )
            member_user = user_result.scalars().first()

            membership_dict = {
                "id": membership.id,
                "user_id": membership.user_id,
                "workspace_id": membership.workspace_id,
                "role_id": membership.role_id,
                "is_owner": membership.is_owner,
                "joined_at": membership.joined_at,
                "created_at": membership.created_at,
                "role": membership.role,
                "user_email": member_user.email if member_user else None,
                "user_display_name": member_user.display_name if member_user else None,
                "user_avatar_url": member_user.avatar_url if member_user else None,
                "user_last_login": member_user.last_login if member_user else None,
            }
            response.append(membership_dict)

        return response

    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(
            status_code=500, detail=f"Failed to fetch members: {e!s}"
        ) from e


@router.put(
    "/workspaces/{workspace_id}/members/{membership_id}",
    response_model=MembershipRead,
)
async def update_member_role(
    workspace_id: int,
    membership_id: int,
    membership_update: MembershipUpdate,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """
    Update a member's role.
    Requires MEMBERS_MANAGE_ROLES permission.
    Cannot change owner's role.
    """
    try:
        actor = await check_permission(
            session,
            auth,
            workspace_id,
            Permission.MEMBERS_MANAGE_ROLES.value,
            "You don't have permission to manage member roles",
        )

        result = await session.execute(
            select(WorkspaceMembership)
            .options(selectinload(WorkspaceMembership.role))
            .filter(
                WorkspaceMembership.id == membership_id,
                WorkspaceMembership.workspace_id == workspace_id,
            )
        )
        db_membership = result.scalars().first()

        if not db_membership:
            raise HTTPException(status_code=404, detail="Membership not found")

        # Cannot change owner's role
        if db_membership.is_owner:
            raise HTTPException(
                status_code=400,
                detail="Cannot change the owner's role",
            )

        if not can_manage_member(actor, db_membership):
            raise HTTPException(status_code=403, detail=_MEMBER_ABOVE_YOU)

        if not membership_update.role_id:
            raise HTTPException(status_code=400, detail="Choose a role")
        role_result = await session.execute(
            select(WorkspaceRole).filter(
                WorkspaceRole.id == membership_update.role_id,
                WorkspaceRole.workspace_id == workspace_id,
            )
        )
        new_role = role_result.scalars().first()
        if not new_role:
            raise HTTPException(
                status_code=404,
                detail="Role not found in this workspace",
            )
        if not can_grant_role(actor, new_role):
            raise HTTPException(status_code=403, detail=_ROLE_ABOVE_YOU)

        db_membership.role_id = new_role.id
        await session.commit()
        await session.refresh(db_membership)

        # Fetch user email
        user_result = await session.execute(
            select(User).filter(User.id == db_membership.user_id)
        )
        member_user = user_result.scalars().first()

        return {
            "id": db_membership.id,
            "user_id": db_membership.user_id,
            "workspace_id": db_membership.workspace_id,
            "role_id": db_membership.role_id,
            "is_owner": db_membership.is_owner,
            "joined_at": db_membership.joined_at,
            "created_at": db_membership.created_at,
            "role": db_membership.role,
            "user_email": member_user.email if member_user else None,
            "user_display_name": member_user.display_name if member_user else None,
            "user_avatar_url": member_user.avatar_url if member_user else None,
            "user_last_login": member_user.last_login if member_user else None,
        }

    except HTTPException:
        raise
    except Exception as e:
        await session.rollback()
        logger.error(f"Failed to update member role: {e!s}", exc_info=True)
        raise HTTPException(
            status_code=500, detail=f"Failed to update member role: {e!s}"
        ) from e


# NOTE: /members/me must be defined BEFORE /members/{membership_id}
# because FastAPI matches routes in order, and "me" would otherwise
# be interpreted as a membership_id (causing a 422 validation error)
@router.delete("/workspaces/{workspace_id}/members/me")
async def leave_workspace(
    workspace_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    """
    Leave a workspace (remove own membership).
    Owners cannot leave their workspace.
    """
    try:
        result = await session.execute(
            select(WorkspaceMembership).filter(
                WorkspaceMembership.user_id == user.id,
                WorkspaceMembership.workspace_id == workspace_id,
            )
        )
        db_membership = result.scalars().first()

        if not db_membership:
            raise HTTPException(
                status_code=404,
                detail="You are not a member of this workspace",
            )

        if db_membership.is_owner:
            raise HTTPException(
                status_code=400,
                detail="Owners cannot leave their workspace. Transfer ownership first or delete the workspace.",
            )

        await refuse_if_last_workspace(
            session, user.id, workspace_id, detail=LAST_WORKSPACE_LEAVE_DETAIL
        )

        # The MCP servers this person added on their own key go with them
        # (app/mcp_security/member_servers.py).
        removed = await delete_member_servers(
            session, workspace_id=workspace_id, user_id=user.id
        )
        await session.delete(db_membership)
        await session.commit()
        if removed:
            forget_servers(workspace_id)
        return {"message": "Successfully left the workspace"}

    except HTTPException:
        raise
    except Exception as e:
        await session.rollback()
        logger.error(f"Failed to leave workspace: {e!s}", exc_info=True)
        raise HTTPException(
            status_code=500, detail=f"Failed to leave workspace: {e!s}"
        ) from e


@router.delete("/workspaces/{workspace_id}/members/{membership_id}")
async def remove_member(
    workspace_id: int,
    membership_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """
    Remove a member from a workspace.
    Requires MEMBERS_REMOVE permission.
    Cannot remove the owner.
    """
    try:
        actor = await check_permission(
            session,
            auth,
            workspace_id,
            Permission.MEMBERS_REMOVE.value,
            "You don't have permission to remove members",
        )

        result = await session.execute(
            select(WorkspaceMembership)
            .options(selectinload(WorkspaceMembership.role))
            .filter(
                WorkspaceMembership.id == membership_id,
                WorkspaceMembership.workspace_id == workspace_id,
            )
        )
        db_membership = result.scalars().first()

        if not db_membership:
            raise HTTPException(status_code=404, detail="Membership not found")

        if db_membership.is_owner:
            raise HTTPException(
                status_code=400,
                detail="Cannot remove the owner from the workspace",
            )
        if not can_manage_member(actor, db_membership):
            raise HTTPException(status_code=403, detail=_MEMBER_ABOVE_YOU)

        # The MCP servers they added on their own key go with them
        # (app/mcp_security/member_servers.py).
        removed = await delete_member_servers(
            session, workspace_id=workspace_id, user_id=db_membership.user_id
        )
        await session.delete(db_membership)
        await session.commit()
        if removed:
            forget_servers(workspace_id)
        return {"message": "Member removed successfully"}

    except HTTPException:
        raise
    except Exception as e:
        await session.rollback()
        logger.error(f"Failed to remove member: {e!s}", exc_info=True)
        raise HTTPException(
            status_code=500, detail=f"Failed to remove member: {e!s}"
        ) from e


# ============ Invite Endpoints ============


def _with_status(invite: WorkspaceInvite, email_sent: bool | None = None):
    """Attach the computed fields ``InviteRead`` reports."""
    invite.status = invite_status(invite)
    invite.email_sent = email_sent
    return invite


async def _load_invite(session: AsyncSession, invite_id: int) -> WorkspaceInvite:
    result = await session.execute(
        select(WorkspaceInvite)
        .options(
            selectinload(WorkspaceInvite.role),
            selectinload(WorkspaceInvite.workspace),
            selectinload(WorkspaceInvite.created_by),
        )
        .filter(WorkspaceInvite.id == invite_id)
    )
    return result.scalars().first()


def _inviter_name(user: User | None) -> str:
    if user is None:
        return "A workspace member"
    return user.display_name or user.email


async def _email_invite(invite: WorkspaceInvite, inviter: User | None) -> bool:
    message = build_invite_email(
        to=invite.email,
        inviter=_inviter_name(inviter),
        workspace_name=invite.workspace.name if invite.workspace else "a workspace",
        role_name=invite.role.name if invite.role else "a member",
        invite_code=invite.invite_code,
        expires_at=invite.expires_at,
    )
    return await send_invite_email(invite.id, message)


async def _resolve_invite_role(
    session: AsyncSession, workspace_id: int, role_id: int | None
) -> WorkspaceRole:
    if role_id:
        result = await session.execute(
            select(WorkspaceRole).filter(
                WorkspaceRole.id == role_id,
                WorkspaceRole.workspace_id == workspace_id,
            )
        )
        role = result.scalars().first()
    else:
        role = await get_default_role(session, workspace_id)
    if not role:
        raise HTTPException(status_code=404, detail="Role not found in this workspace")
    return role


@router.post(
    "/workspaces/{workspace_id}/invites",
    response_model=InviteRead,
)
async def create_invite(
    workspace_id: int,
    invite_data: InviteCreate,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Create an invite link, optionally bound to one registered address.

    Requires MEMBERS_INVITE, and the role must be at or below the inviter's own.
    With an address the link is for that account only, single-use and emailed
    to it; sign-ups are closed, so an address without an account is refused
    with a message saying so. Without one, any signed-in account may use the
    link within its expiry and maximum uses.
    """
    actor = await check_permission(
        session,
        auth,
        workspace_id,
        Permission.MEMBERS_INVITE.value,
        "You don't have permission to create invites",
    )
    role = await _resolve_invite_role(session, workspace_id, invite_data.role_id)
    if not can_grant_role(actor, role):
        raise HTTPException(status_code=403, detail=_ROLE_ABOVE_YOU)

    address = normalize_address(invite_data.email) if invite_data.email else None
    if address is not None:
        invitee = (
            (
                await session.execute(
                    select(User).filter(func.lower(User.email) == address)
                )
            )
            .scalars()
            .first()
        )
        if invitee is None:
            raise HTTPException(
                status_code=422,
                detail=(
                    f"There is no account for {address}. Sign-ups are closed, so an "
                    "administrator has to create the account before you can invite it."
                ),
            )

        already_member = (
            await session.execute(
                select(WorkspaceMembership.id).filter(
                    WorkspaceMembership.workspace_id == workspace_id,
                    WorkspaceMembership.user_id == invitee.id,
                )
            )
        ).first()
        if already_member:
            raise HTTPException(
                status_code=409,
                detail=f"{address} is already a member of this workspace",
            )

        existing = (
            (
                await session.execute(
                    select(WorkspaceInvite).filter(
                        WorkspaceInvite.workspace_id == workspace_id,
                        func.lower(WorkspaceInvite.email) == address,
                    )
                )
            )
            .scalars()
            .all()
        )
        if any(invite_status(invite) == "pending" for invite in existing):
            raise HTTPException(
                status_code=409,
                detail=f"{address} already has a pending invite. Resend it from the list.",
            )

    try:
        db_invite = WorkspaceInvite(
            name=invite_data.name,
            role_id=role.id,
            expires_at=invite_data.expires_at,
            # An invite for one address is used once; a link keeps the caller's cap.
            max_uses=1 if address else invite_data.max_uses,
            email=address,
            invite_code=generate_invite_code(),
            workspace_id=workspace_id,
            created_by_id=auth.user.id,
            last_sent_at=datetime.now(UTC) if address else None,
        )
        session.add(db_invite)
        await session.commit()
    except Exception as e:
        await session.rollback()
        logger.error(f"Failed to create invite: {e!s}", exc_info=True)
        raise HTTPException(status_code=500, detail="Failed to create invite") from e

    db_invite = await _load_invite(session, db_invite.id)
    sent = await _email_invite(db_invite, auth.user) if address else None

    ph_analytics.capture_for(
        auth,
        "workspace_invite_sent",
        {
            "workspace_id": workspace_id,
            "role_name": db_invite.role.name if db_invite.role else None,
            "has_expiry": db_invite.expires_at is not None,
            "has_max_uses": db_invite.max_uses is not None,
            "email_sent": sent,
            "bound_to_email": address is not None,
        },
        groups={"workspace": str(workspace_id)},
    )
    return _with_status(db_invite, email_sent=sent)


@router.post(
    "/workspaces/{workspace_id}/invites/{invite_id}/resend",
    response_model=InviteRead,
)
async def resend_invite(
    workspace_id: int,
    invite_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Email a pending invite again (at most once a minute)."""
    await check_permission(
        session,
        auth,
        workspace_id,
        Permission.MEMBERS_INVITE.value,
        "You don't have permission to send invites",
    )
    invite = await _load_invite(session, invite_id)
    if not invite or invite.workspace_id != workspace_id:
        raise HTTPException(status_code=404, detail="Invite not found")
    if not invite.email:
        raise HTTPException(
            status_code=400, detail="A link invite has no address to email"
        )
    if invite_status(invite) != "pending":
        raise HTTPException(
            status_code=400, detail="Only a pending invite can be resent"
        )
    now = datetime.now(UTC)
    if invite.last_sent_at and now - invite.last_sent_at < _RESEND_COOLDOWN:
        raise HTTPException(
            status_code=429, detail="The invite was just sent. Try again in a minute."
        )
    invite.last_sent_at = now
    await session.commit()
    invite = await _load_invite(session, invite_id)
    sent = await _email_invite(invite, auth.user)
    return _with_status(invite, email_sent=sent)


@router.get(
    "/workspaces/{workspace_id}/invites",
    response_model=list[InviteRead],
)
async def list_invites(
    workspace_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """
    List all invites for a workspace.
    Requires MEMBERS_INVITE permission.
    """
    await check_permission(
        session,
        auth,
        workspace_id,
        Permission.MEMBERS_INVITE.value,
        "You don't have permission to view invites",
    )
    result = await session.execute(
        select(WorkspaceInvite)
        .options(selectinload(WorkspaceInvite.role))
        .filter(WorkspaceInvite.workspace_id == workspace_id)
        .order_by(WorkspaceInvite.created_at.desc())
    )
    return [_with_status(invite) for invite in result.scalars().all()]


@router.put(
    "/workspaces/{workspace_id}/invites/{invite_id}",
    response_model=InviteRead,
)
async def update_invite(
    workspace_id: int,
    invite_id: int,
    invite_update: InviteUpdate,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """
    Update an invite.
    Requires MEMBERS_INVITE permission; a new role must be at or below the caller's.
    """
    actor = await check_permission(
        session,
        auth,
        workspace_id,
        Permission.MEMBERS_INVITE.value,
        "You don't have permission to update invites",
    )

    result = await session.execute(
        select(WorkspaceInvite)
        .options(selectinload(WorkspaceInvite.role))
        .filter(
            WorkspaceInvite.id == invite_id,
            WorkspaceInvite.workspace_id == workspace_id,
        )
    )
    db_invite = result.scalars().first()
    if not db_invite:
        raise HTTPException(status_code=404, detail="Invite not found")

    update_data = invite_update.model_dump(exclude_unset=True)
    if "role_id" in update_data:
        role = await _resolve_invite_role(session, workspace_id, update_data["role_id"])
        if not can_grant_role(actor, role):
            raise HTTPException(status_code=403, detail=_ROLE_ABOVE_YOU)
        update_data["role_id"] = role.id
    elif db_invite.role is not None and not can_grant_role(actor, db_invite.role):
        raise HTTPException(status_code=403, detail=_ROLE_ABOVE_YOU)

    try:
        for key, value in update_data.items():
            setattr(db_invite, key, value)
        await session.commit()
    except Exception as e:
        await session.rollback()
        logger.error(f"Failed to update invite: {e!s}", exc_info=True)
        raise HTTPException(status_code=500, detail="Failed to update invite") from e
    return _with_status(await _load_invite(session, invite_id))


@router.delete("/workspaces/{workspace_id}/invites/{invite_id}")
async def revoke_invite(
    workspace_id: int,
    invite_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """
    Revoke (delete) an invite.
    Requires MEMBERS_INVITE permission.
    """
    try:
        await check_permission(
            session,
            auth,
            workspace_id,
            Permission.MEMBERS_INVITE.value,
            "You don't have permission to revoke invites",
        )

        result = await session.execute(
            select(WorkspaceInvite).filter(
                WorkspaceInvite.id == invite_id,
                WorkspaceInvite.workspace_id == workspace_id,
            )
        )
        db_invite = result.scalars().first()

        if not db_invite:
            raise HTTPException(status_code=404, detail="Invite not found")

        await session.delete(db_invite)
        await session.commit()
        return {"message": "Invite revoked successfully"}

    except HTTPException:
        raise
    except Exception as e:
        await session.rollback()
        logger.error(f"Failed to revoke invite: {e!s}", exc_info=True)
        raise HTTPException(
            status_code=500, detail=f"Failed to revoke invite: {e!s}"
        ) from e


# ============ Public Invite Endpoints ============

_INVITE_UNUSABLE = {
    "used_up": "This invite has already been used",
    "revoked": "This invite is no longer active",
    "expired": "This invite has expired",
}


@router.get("/invites/{invite_code}/info", response_model=InviteInfoResponse)
async def get_invite_info(
    invite_code: str,
    session: AsyncSession = Depends(get_async_session),
):
    """
    Get information about an invite (public endpoint, no auth required).
    Returns minimal info for displaying on invite acceptance page: the
    workspace, the role, who sent it and the invited address, masked.
    """
    result = await session.execute(
        select(WorkspaceInvite)
        .options(
            selectinload(WorkspaceInvite.role),
            selectinload(WorkspaceInvite.workspace),
            selectinload(WorkspaceInvite.created_by),
        )
        .filter(WorkspaceInvite.invite_code == invite_code)
    )
    invite = result.scalars().first()
    if not invite:
        return InviteInfoResponse(
            workspace_name="",
            role_name=None,
            is_valid=False,
            message="Invite not found",
        )

    status = invite_status(invite)
    return InviteInfoResponse(
        workspace_name=invite.workspace.name if invite.workspace else "",
        role_name=invite.role.name if invite.role else None,
        is_valid=status == "pending",
        message=_INVITE_UNUSABLE.get(status),
        invited_email_hint=mask_address(invite.email),
        inviter_name=_inviter_name(invite.created_by) if invite.created_by else None,
    )


@router.post("/invites/accept", response_model=InviteAcceptResponse)
async def accept_invite(
    request: InviteAcceptRequest,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """
    Accept an invite and join a workspace. Only the invited account can.
    """
    user = auth.user
    result = await session.execute(
        select(WorkspaceInvite)
        .options(
            selectinload(WorkspaceInvite.role),
            selectinload(WorkspaceInvite.workspace),
        )
        .filter(WorkspaceInvite.invite_code == request.invite_code)
        .with_for_update(of=WorkspaceInvite)
    )
    invite = result.scalars().first()
    if not invite:
        raise HTTPException(status_code=404, detail="Invite not found")

    status = invite_status(invite)
    if status != "pending":
        raise HTTPException(status_code=400, detail=_INVITE_UNUSABLE[status])

    if invite.email and not same_address(invite.email, user.email):
        raise HTTPException(
            status_code=403,
            detail=(
                "This invite is for another account. Sign in as "
                f"{mask_address(invite.email)} to accept it."
            ),
        )

    existing_membership = await session.execute(
        select(WorkspaceMembership).filter(
            WorkspaceMembership.user_id == user.id,
            WorkspaceMembership.workspace_id == invite.workspace_id,
        )
    )
    if existing_membership.scalars().first():
        raise HTTPException(
            status_code=400,
            detail="You are already a member of this workspace",
        )

    role = invite.role or await get_default_role(session, invite.workspace_id)
    if role is None or grants_full_access(role):
        raise HTTPException(
            status_code=400,
            detail="This invite is no longer valid. Ask for a new invite.",
        )

    try:
        session.add(
            WorkspaceMembership(
                user_id=user.id,
                workspace_id=invite.workspace_id,
                role_id=role.id,
                is_owner=False,
                invited_by_invite_id=invite.id,
            )
        )
        invite.uses_count += 1
        await session.commit()
    except Exception as e:
        await session.rollback()
        logger.error(f"Failed to accept invite: {e!s}", exc_info=True)
        raise HTTPException(status_code=500, detail="Failed to accept invite") from e

    workspace_name = invite.workspace.name if invite.workspace else ""
    # The phone: "Invite accepted", to the person who sent it (app/push).
    push_events.invite_accepted(
        inviter_user_id=invite.created_by_id, workspace_id=invite.workspace_id
    )
    for _evt in ("workspace_invite_accepted", "workspace_user_added"):
        ph_analytics.capture_for(
            auth,
            _evt,
            {"workspace_id": invite.workspace_id, "role_name": role.name},
            groups={"workspace": str(invite.workspace_id)},
        )

    return InviteAcceptResponse(
        message="Successfully joined the workspace",
        workspace_id=invite.workspace_id,
        workspace_name=workspace_name,
        role_name=role.name,
    )


# ============ User Access Info ============


@router.get(
    "/workspaces/{workspace_id}/my-access",
    response_model=UserWorkspaceAccess,
)
async def get_my_access(
    workspace_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    """
    Get the current user's access info for a workspace.
    """
    try:
        membership = await check_workspace_access(session, auth, workspace_id)

        # Get workspace name
        result = await session.execute(
            select(Workspace).filter(Workspace.id == workspace_id)
        )
        workspace = result.scalars().first()

        # Get permissions
        permissions = await get_user_permissions(session, user.id, workspace_id)

        return UserWorkspaceAccess(
            workspace_id=workspace_id,
            workspace_name=workspace.name if workspace else "",
            is_owner=membership.is_owner,
            role_name=membership.role.name if membership.role else None,
            permissions=permissions,
        )

    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(
            status_code=500, detail=f"Failed to get access info: {e!s}"
        ) from e
