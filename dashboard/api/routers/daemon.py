"""守护进程路由：状态查询、启动、停止。"""

from __future__ import annotations

from fastapi import APIRouter, Depends

from api.core.security import get_current_admin
from api.schemas import ApiResponse
from api.services import daemon as daemon_svc

router = APIRouter(prefix="/api/daemon", tags=["守护进程"])


@router.get("/status", response_model=ApiResponse)
async def daemon_status(admin: dict = Depends(get_current_admin)):
    return ApiResponse.ok(daemon_svc.daemon_status())


@router.post("/start", response_model=ApiResponse)
async def daemon_start(admin: dict = Depends(get_current_admin)):
    r = await daemon_svc.start_daemon()
    return ApiResponse.ok(r) if r.get("ok") else ApiResponse.fail(r.get("error", "启动失败"))


@router.post("/stop", response_model=ApiResponse)
async def daemon_stop(admin: dict = Depends(get_current_admin)):
    r = await daemon_svc.stop_daemon()
    return ApiResponse.ok(r) if r.get("ok") else ApiResponse.fail(r.get("error", "停止失败"))
