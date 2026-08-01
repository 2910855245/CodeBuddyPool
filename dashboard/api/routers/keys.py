"""API 密钥管理路由：转发到 Go 代理的多 Key 管理（keymgr），管理员手动增删/启停/查用量。"""

from __future__ import annotations

from pydantic import BaseModel, Field
from fastapi import APIRouter, Depends

from api.core.security import get_current_admin
from api.schemas import ApiResponse
from api.services import proxy_client

router = APIRouter(prefix="/api/keys", tags=["API密钥"])


class KeyCreateRequest(BaseModel):
    name: str = Field(default="", max_length=50, description="密钥备注名（发给谁的标识）")


@router.get("", response_model=ApiResponse)
async def keys_list(admin: dict = Depends(get_current_admin)):
    return ApiResponse.ok(await proxy_client.keys_list())


@router.post("", response_model=ApiResponse)
async def keys_create(req: KeyCreateRequest, admin: dict = Depends(get_current_admin)):
    # 明文 key 仅在创建这一次返回，前端需提示保存
    return ApiResponse.ok(await proxy_client.keys_create(req.name))


@router.put("/{key}/toggle", response_model=ApiResponse)
async def keys_toggle(key: str, admin: dict = Depends(get_current_admin)):
    return ApiResponse.ok(await proxy_client.keys_toggle(key))


@router.delete("/{key}", response_model=ApiResponse)
async def keys_delete(key: str, admin: dict = Depends(get_current_admin)):
    return ApiResponse.ok(await proxy_client.keys_delete(key))


@router.get("/{key}/usage", response_model=ApiResponse)
async def key_usage(key: str, admin: dict = Depends(get_current_admin)):
    return ApiResponse.ok(await proxy_client.key_usage(key))
