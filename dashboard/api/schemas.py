"""API 请求/响应模型（DTO）。与 db/models.py（ORM 表结构）区分，这层只描述 HTTP 报文。"""

from __future__ import annotations

from typing import Any, Optional

from pydantic import BaseModel, Field


class ApiResponse(BaseModel):
    """统一响应包络：success / message / data。"""

    success: bool = True
    message: str = "ok"
    data: Any = None

    @classmethod
    def ok(cls, data: Any = None, message: str = "ok") -> "ApiResponse":
        return cls(success=True, message=message, data=data)

    @classmethod
    def fail(cls, message: str, data: Any = None) -> "ApiResponse":
        return cls(success=False, message=message, data=data)


class AdminLoginRequest(BaseModel):
    username: str = Field(..., min_length=1, description="管理员用户名")
    password: str = Field(..., min_length=1, description="管理员密码")


class TokenRequest(BaseModel):
    token: str = Field(default="", description="JWT 令牌")


class PoolSettingsRequest(BaseModel):
    target_pool: Optional[int] = Field(default=None, ge=1, le=100)
    register_interval: Optional[int] = Field(default=None, ge=0)
    checkin_interval: Optional[int] = Field(default=None, ge=60)
    auto_daemon: Optional[bool] = None
    proxy_base: Optional[str] = None
    proxy_key: Optional[str] = None


class RegisterStartRequest(BaseModel):
    phone: str = Field(..., min_length=5, description="手机号")


class RegisterSubmitRequest(BaseModel):
    phone: str = Field(..., min_length=1)
    code: str = Field(..., min_length=4, max_length=8, description="短信验证码")


class ChangePasswordRequest(BaseModel):
    old_password: str = Field(..., min_length=1, description="旧密码")
    new_password: str = Field(..., min_length=6, description="新密码（至少6位）")
