"""分享注册路由（免登录）：发起注册发短信、提交验证码入池。"""

from __future__ import annotations

from fastapi import APIRouter

from api.schemas import ApiResponse, RegisterStartRequest, RegisterSubmitRequest
from api.services import register_service

router = APIRouter(prefix="/api/register-page", tags=["分享注册"])


@router.post("/start", response_model=ApiResponse)
async def reg_start(req: RegisterStartRequest):
    r = register_service.start_register(req.phone)
    return ApiResponse.ok(r) if r.get("ok") else ApiResponse.fail(r.get("error", "发送失败"))


@router.post("/submit", response_model=ApiResponse)
async def reg_submit(req: RegisterSubmitRequest):
    r = await register_service.submit_register(req.phone, req.code)
    return ApiResponse.ok(r) if r.get("ok") else ApiResponse.fail(r.get("error", "注册失败"), r)


@router.post("/check", response_model=ApiResponse)
async def reg_check(req: RegisterStartRequest):
    """检查手机号状态：是否已在号池（返回积分）/ 是否有进行中的注册会话。"""
    from api.services import pool_store
    phone = register_service._normalize_phone(req.phone)
    acct = pool_store.find_account_normalized(phone)
    if acct:
        return ApiResponse.ok({
            "in_pool": True, "has_pending": False,
            "credits": round(acct.get("credits_total", 0), 1),
            "message": "该手机号已在号池中",
        })
    has_pending = phone in register_service._pending_reg
    return ApiResponse.ok({
        "in_pool": False, "has_pending": has_pending,
        "credits": None,
        "message": "已有进行中的注册, 请输入验证码" if has_pending else "该手机号尚未注册",
    })
