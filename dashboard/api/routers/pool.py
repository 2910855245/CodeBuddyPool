"""号池路由：列表/删除/刷新积分/签到/代理热加载等。"""

from __future__ import annotations

import asyncio

from fastapi import APIRouter, Depends, HTTPException

from api.core.security import get_current_admin
from api.schemas import ApiResponse
from api.services import pool_store
from api.services.codebuddy_api import RefreshError, check_credits, do_checkin, refresh_token

router = APIRouter(prefix="/api", tags=["号池管理"])

# ---- 手动操作防连点（对齐 Go 端 ManualOpCooldown）----
# 面板上的签到/刷新按钮直连 CodeBuddy 账单接口，连点会触发风控。
# 全量操作全局互斥；单号操作按号 15 秒冷却。
import time

_BATCH_LOCK = asyncio.Lock()          # 全量签到/刷新互斥
_ONE_COOLDOWN: dict[str, float] = {}  # phone -> 上次操作时间戳
_ONE_COOLDOWN_SEC = 15


def _one_op_guard(phone: str) -> None:
    now = time.monotonic()
    last = _ONE_COOLDOWN.get(phone, 0.0)
    if now - last < _ONE_COOLDOWN_SEC:
        raise HTTPException(status_code=429, detail="操作过于频繁，请15秒后再试")
    _ONE_COOLDOWN[phone] = now


async def _run_batch(fn):
    """全量操作互斥执行：已有批量任务在跑时直接拒绝。"""
    if _BATCH_LOCK.locked():
        raise HTTPException(status_code=409, detail="批量操作进行中，请稍后再试")
    async with _BATCH_LOCK:
        return await fn()


@router.get("/pool", response_model=ApiResponse)
async def pool_status(admin: dict = Depends(get_current_admin)):
    accs = pool_store.read_accounts()
    stats = {"total": len(accs), "available": 0, "exhausted": 0, "dead": 0, "credits": 0.0}
    out = []
    for a in accs:
        st = pool_store.account_status(a)
        stats[st] += 1
        stats["credits"] += max(a.get("credits_total", 0), 0)
        out.append(pool_store.public_account(a))
    stats["credits"] = round(stats["credits"], 1)
    return ApiResponse.ok({**stats, "accounts": out})


@router.post("/checkin", response_model=ApiResponse)
async def checkin_all(admin: dict = Depends(get_current_admin)):
    return await _run_batch(_checkin_all_impl)


async def _checkin_all_impl():
    accs = pool_store.read_accounts()
    sem = asyncio.Semaphore(5)

    async def one(a):
        async with sem:
            # 签到前先确保令牌有效（临近过期才刷新，对齐 refresh_all 逻辑）
            try:
                await refresh_token(a)
            except RefreshError as e:
                if e.dead:
                    a["dead"] = True
            if a.get("dead"):
                return {"phone": a.get("phone", "?"), "bonus": 0,
                        "credits": a.get("credits_total", 0), "dead": True}
            token = a.get("access_token", "")
            bonus = await do_checkin(token)
            credits = await check_credits(token)
            if credits >= 0:
                a["credits_total"] = credits
            return {"phone": a.get("phone", "?"), "bonus": bonus,
                    "credits": round(a.get("credits_total", 0), 1)}

    results = await asyncio.gather(*[one(a) for a in accs]) if accs else []

    def _merge(latest):
        by_phone = {a.get("phone"): a for a in latest}
        for a in accs:
            p = a.get("phone")
            if p in by_phone:
                tgt = by_phone[p]
                tgt["credits_total"] = a["credits_total"]
                # 签到可能刷新了 token，写回新 token
                if a.get("access_token") and a.get("refresh_token"):
                    tgt["access_token"] = a["access_token"]
                    tgt["refresh_token"] = a["refresh_token"]
                    tgt["expires_at"] = a["expires_at"]
                if a.get("dead"):
                    tgt["dead"] = True
        return latest

    await pool_store.mutate_accounts(_merge)
    return ApiResponse.ok({"total_bonus": sum(r["bonus"] for r in results), "results": results})


@router.post("/checkin/{phone}", response_model=ApiResponse)
async def checkin_one(phone: str, admin: dict = Depends(get_current_admin)):
    _one_op_guard(phone)
    a = pool_store.get_account(phone)
    if not a:
        raise HTTPException(status_code=404, detail="账号不存在")
    # 签到前先确保令牌有效
    token_refreshed = False
    dead = bool(a.get("dead"))
    try:
        token_refreshed = await refresh_token(a)
    except RefreshError as e:
        if e.dead:
            dead = True
        else:
            raise HTTPException(status_code=502, detail=str(e))
    if dead:
        return ApiResponse.ok({"phone": phone, "bonus": 0,
                               "credits": a.get("credits_total", 0), "dead": True})
    token = a.get("access_token", "")
    bonus = await do_checkin(token)
    credits = await check_credits(token)
    new_credits = credits if credits >= 0 else a.get("credits_total", 0)

    def _merge(latest):
        for x in latest:
            if x.get("phone") == phone:
                x["credits_total"] = new_credits
                if token_refreshed:
                    x["access_token"] = a["access_token"]
                    x["refresh_token"] = a["refresh_token"]
                    x["expires_at"] = a["expires_at"]
                if dead:
                    x["dead"] = True
        return latest

    await pool_store.mutate_accounts(_merge)
    return ApiResponse.ok({"phone": phone, "bonus": bonus, "credits": round(new_credits, 1),
                           "token_refreshed": token_refreshed, "dead": dead})


@router.post("/refresh/{phone}", response_model=ApiResponse)
async def refresh_one(phone: str, admin: dict = Depends(get_current_admin)):
    _one_op_guard(phone)
    a = pool_store.get_account(phone)
    if not a:
        raise HTTPException(status_code=404, detail="账号不存在")

    # 先确保令牌有效：临近过期才刷新（对齐 Go ensureFreshToken）
    token_refreshed = False
    dead = bool(a.get("dead"))
    try:
        token_refreshed = await refresh_token(a)
    except RefreshError as e:
        if e.dead:
            dead = True  # refresh_token 被拒，账号彻底失效，落盘标记
        else:
            raise HTTPException(status_code=502, detail=str(e))

    # 死号不再查积分（token 已失效，查了也是 401）
    new_credits = a.get("credits_total", 0)
    if not dead:
        credits = await check_credits(a.get("access_token", ""))
        if credits >= 0:
            new_credits = credits

    def _merge(latest):
        for x in latest:
            if x.get("phone") == phone:
                x["credits_total"] = new_credits
                if token_refreshed:
                    x["access_token"] = a["access_token"]
                    x["refresh_token"] = a["refresh_token"]
                    x["expires_at"] = a["expires_at"]
                if dead:
                    x["dead"] = True
        return latest

    await pool_store.mutate_accounts(_merge)
    return ApiResponse.ok({
        "phone": phone,
        "credits": round(new_credits, 1),
        "token_refreshed": token_refreshed,
        "dead": dead,
    })


@router.post("/refresh-all", response_model=ApiResponse)
async def refresh_all(admin: dict = Depends(get_current_admin)):
    return await _run_batch(_refresh_all_impl)


async def _refresh_all_impl():
    accs = pool_store.read_accounts()
    sem = asyncio.Semaphore(5)

    async def one(a):
        async with sem:
            # 临近过期先刷令牌；失败仅在 refresh_token 被拒时标死，其余错误跳过
            try:
                a["_token_refreshed"] = await refresh_token(a)
            except RefreshError as e:
                a["_token_refreshed"] = False
                if e.dead:
                    a["dead"] = True
            if not a.get("dead"):
                credits = await check_credits(a.get("access_token", ""))
                if credits >= 0:
                    a["credits_total"] = credits

    if accs:
        await asyncio.gather(*[one(a) for a in accs])

    def _merge(latest):
        by_phone = {a.get("phone"): a for a in latest}
        for a in accs:
            p = a.get("phone")
            if p in by_phone:
                tgt = by_phone[p]
                tgt["credits_total"] = a["credits_total"]
                if a.pop("_token_refreshed", False):
                    tgt["access_token"] = a["access_token"]
                    tgt["refresh_token"] = a["refresh_token"]
                    tgt["expires_at"] = a["expires_at"]
                if a.get("dead"):
                    tgt["dead"] = True
        return latest

    await pool_store.mutate_accounts(_merge)
    return ApiResponse.ok({"count": len(accs)})


@router.delete("/account/{phone}", response_model=ApiResponse)
async def delete_account(phone: str, admin: dict = Depends(get_current_admin)):
    def _remove(latest):
        remaining = [a for a in latest if a.get("phone") != phone]
        if len(remaining) == len(latest):
            return None
        return remaining

    result = await pool_store.mutate_accounts(_remove)
    if result is None:
        raise HTTPException(status_code=404, detail="账号不存在")
    return ApiResponse.ok({"removed": phone})
