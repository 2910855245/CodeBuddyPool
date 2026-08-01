"""代理监控路由：Go 代理在线状态、账号、日志、签到、热加载。"""

from __future__ import annotations

from fastapi import APIRouter, Depends, Query

from api.core.security import get_current_admin
from api.schemas import ApiResponse
from api.services import proxy_client

router = APIRouter(prefix="/api", tags=["代理监控"])


@router.get("/proxy-status", response_model=ApiResponse)
async def proxy_status(admin: dict = Depends(get_current_admin)):
    return ApiResponse.ok(await proxy_client.proxy_status())


@router.get("/proxy/accounts", response_model=ApiResponse)
async def proxy_accounts(admin: dict = Depends(get_current_admin)):
    return ApiResponse.ok(await proxy_client.proxy_accounts())


@router.get("/proxy/logs", response_model=ApiResponse)
async def proxy_logs(n: int = Query(100, ge=1, le=500), admin: dict = Depends(get_current_admin)):
    return ApiResponse.ok(await proxy_client.proxy_logs(n))


@router.post("/proxy/checkin", response_model=ApiResponse)
async def proxy_checkin(admin: dict = Depends(get_current_admin)):
    return ApiResponse.ok(await proxy_client.proxy_checkin())


@router.post("/proxy/reload", response_model=ApiResponse)
async def proxy_reload(admin: dict = Depends(get_current_admin)):
    return ApiResponse.ok(await proxy_client.proxy_reload())
