"""自动化设置路由：读取、保存。"""

from __future__ import annotations

from fastapi import APIRouter, Depends

from api.core.security import get_current_admin
from api.schemas import ApiResponse, PoolSettingsRequest
from api.services.settings_store import load_settings, save_settings

router = APIRouter(prefix="/api/settings", tags=["自动化设置"])


@router.get("", response_model=ApiResponse)
async def get_settings(admin: dict = Depends(get_current_admin)):
    s = load_settings()
    return ApiResponse.ok(s)


@router.post("", response_model=ApiResponse)
async def update_settings(req: PoolSettingsRequest, admin: dict = Depends(get_current_admin)):
    s = load_settings()
    for k in ("target_pool", "register_interval",
              "checkin_interval", "auto_daemon", "proxy_base", "proxy_key"):
        v = getattr(req, k, None)
        if v is not None:
            s[k] = v
    save_settings(s)
    return ApiResponse.ok(message="已保存")
