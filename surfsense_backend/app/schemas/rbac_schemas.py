"""
Pydantic schemas for RBAC (Role-Based Access Control) endpoints.
"""

from datetime import datetime
from typing import Literal
from uuid import UUID

from pydantic import BaseModel, EmailStr, Field

# ============ Role Schemas ============


class RoleBase(BaseModel):
    """Base schema for roles."""

    name: str = Field(..., min_length=1, max_length=100)
    description: str | None = Field(None, max_length=500)
    permissions: list[str] = Field(default_factory=list)
    is_default: bool = False


class RoleCreate(RoleBase):
    """Schema for creating a new role."""

    pass


class RoleUpdate(BaseModel):
    """Schema for updating a role (partial update)."""

    name: str | None = Field(None, min_length=1, max_length=100)
    description: str | None = Field(None, max_length=500)
    permissions: list[str] | None = None
    is_default: bool | None = None


class RoleRead(RoleBase):
    """Schema for reading a role."""

    id: int
    workspace_id: int
    is_system_role: bool
    created_at: datetime

    class Config:
        from_attributes = True


# ============ Membership Schemas ============


class MembershipBase(BaseModel):
    """Base schema for memberships."""

    pass


class MembershipUpdate(BaseModel):
    """Schema for updating a membership (change role)."""

    role_id: int | None = None


class MembershipRead(BaseModel):
    """Schema for reading a membership."""

    id: int
    user_id: UUID
    workspace_id: int
    role_id: int | None
    is_owner: bool
    joined_at: datetime
    created_at: datetime
    # Nested role info
    role: RoleRead | None = None
    # User details (populated separately)
    user_email: str | None = None
    user_display_name: str | None = None
    user_avatar_url: str | None = None
    user_last_login: datetime | None = None

    class Config:
        from_attributes = True


class MembershipReadWithUser(MembershipRead):
    """Schema for reading a membership with user details."""

    user_email: str | None = None
    user_is_active: bool | None = None


# ============ Invite Schemas ============


class InviteBase(BaseModel):
    """Base schema for invites."""

    name: str | None = Field(None, max_length=100)
    role_id: int | None = None
    expires_at: datetime | None = None
    max_uses: int | None = Field(None, ge=1)


class InviteCreate(InviteBase):
    """An invite link. With ``email`` it is for that registered account only,
    is emailed to it and is single-use; without, any signed-in account may use
    the link (within its expiry and maximum uses)."""

    email: EmailStr | None = None


class InviteUpdate(BaseModel):
    """Schema for updating an invite (partial update). The address is fixed."""

    name: str | None = Field(None, max_length=100)
    role_id: int | None = None
    expires_at: datetime | None = None
    max_uses: int | None = Field(None, ge=1)
    is_active: bool | None = None


class InviteRead(InviteBase):
    """Schema for reading an invite."""

    id: int
    invite_code: str
    workspace_id: int
    created_by_id: UUID | None
    uses_count: int
    is_active: bool
    created_at: datetime
    email: str | None = None
    last_sent_at: datetime | None = None
    status: Literal["pending", "used_up", "expired", "revoked"] = "pending"
    # Set on create and resend: whether the invitation email left.
    email_sent: bool | None = None
    # Nested role info
    role: RoleRead | None = None

    class Config:
        from_attributes = True


class InviteAcceptRequest(BaseModel):
    """Schema for accepting an invite."""

    invite_code: str = Field(..., min_length=1)


class InviteAcceptResponse(BaseModel):
    """Response schema for accepting an invite."""

    message: str
    workspace_id: int
    workspace_name: str
    role_name: str | None


class InviteInfoResponse(BaseModel):
    """Response schema for getting invite info (public endpoint)."""

    workspace_name: str
    role_name: str | None
    is_valid: bool
    message: str | None = None
    # The bound address, masked (d****@example.com), and who sent it.
    invited_email_hint: str | None = None
    inviter_name: str | None = None


# ============ Permission Schemas ============


class PermissionInfo(BaseModel):
    """Schema for permission information."""

    value: str
    name: str
    category: str
    description: str


class PermissionsListResponse(BaseModel):
    """Response schema for listing all available permissions."""

    permissions: list[PermissionInfo]


# ============ User Access Info ============


class UserWorkspaceAccess(BaseModel):
    """Schema for user's access info in a workspace."""

    workspace_id: int
    workspace_name: str
    is_owner: bool
    role_name: str | None
    permissions: list[str]
