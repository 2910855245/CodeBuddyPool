"""CodeBuddy 开放接口封装：令牌刷新、查积分、每日签到。异步/同步各一套（同步供注册流程用）。"""

from __future__ import annotations

import time

import httpx

COPILOT_BASE = "https://copilot.tencent.com"

# CodeBuddy 令牌端点（OIDC 协议，与 Go 代理 ensureFreshToken 一致）
TOKEN_URL = "https://www.codebuddy.cn/auth/realms/copilot/protocol/openid-connect/token"

# 令牌剩余有效期低于该阈值（秒）时触发刷新，对齐 Go 的 TokenRefreshTTL
TOKEN_REFRESH_TTL = 3600


class RefreshError(Exception):
    """令牌刷新失败。

    Attributes:
        dead: True 表示 refresh_token 被服务端拒绝（400/401/403），账号彻底失效。
    """

    def __init__(self, message: str, dead: bool = False):
        super().__init__(message)
        self.dead = dead


def _apply_token(account: dict, payload: dict) -> None:
    """把刷新结果写回账号字典（就地修改）。

    refresh_token 仅在服务端下发新值时覆盖；expires_at 按 expires_in 换算成毫秒时间戳。
    """
    account["access_token"] = payload.get("access_token", account.get("access_token", ""))
    if payload.get("refresh_token"):
        account["refresh_token"] = payload["refresh_token"]
    expires_in = int(payload.get("expires_in", 0))
    account["expires_at"] = int(time.time() * 1000) + expires_in * 1000


def needs_refresh(account: dict) -> bool:
    """判断令牌是否临近过期（剩余不足 TOKEN_REFRESH_TTL 秒）需要刷新。"""
    exp = account.get("expires_at", 0)
    return not (exp > 0 and int(time.time() * 1000) + TOKEN_REFRESH_TTL * 1000 < exp)


async def refresh_token(account: dict) -> bool:
    """刷新单个账号的访问令牌（对齐 Go 代理 ensureFreshToken）。

    令牌仍充足时直接返回不发起请求；否则用 refresh_token 走 OIDC 刷新流程，
    成功后把 access_token/refresh_token/expires_at 就地写回 account。

    Args:
        account: 账号字典，需含 access_token/refresh_token/expires_at 字段。

    Returns:
        True 表示发生了刷新；False 表示令牌仍有效未刷新。

    Raises:
        RefreshError: 无 refresh_token，或刷新请求失败；其 dead 标记账号是否彻底失效。
    """
    if not needs_refresh(account):
        return False

    rt = account.get("refresh_token", "")
    if not rt:
        raise RefreshError("no refresh token")

    async with httpx.AsyncClient(timeout=15) as c:
        r = await c.post(
            TOKEN_URL,
            content=f"grant_type=refresh_token&refresh_token={rt}&client_id=console",
            headers={"Content-Type": "application/x-www-form-urlencoded"})

    if r.status_code != 200:
        dead = r.status_code in (400, 401, 403)
        raise RefreshError(f"refresh failed: {r.status_code} {r.text[:200]}", dead=dead)

    _apply_token(account, r.json())
    return True


async def check_credits(access_token: str) -> float:
    """查询账号剩余积分。

    Returns:
        剩余积分；失败返回 -1。
    """
    try:
        async with httpx.AsyncClient(timeout=15) as c:
            r = await c.post(
                f"{COPILOT_BASE}/v2/billing/meter/get-user-resource",
                json={"PageNumber": 1, "PageSize": 100, "ProductCode": "p_tcaca",
                      "Status": [0, 3], "PackageStartTimeRangeBegin": "2024-12-01 21:25:00",
                      "PackageStartTimeRangeEnd": time.strftime("%Y-%m-%d %H:%M:%S")},
                headers={"Authorization": f"Bearer {access_token}"})
            accounts = r.json().get("data", {}).get("Response", {}).get("Data", {}).get("Accounts", [])
            return sum(float(a.get("CycleCapacityRemainPrecise", 0)) for a in accounts)
    except Exception:
        return -1


async def do_checkin(access_token: str) -> int:
    """每日签到。

    Returns:
        获得积分；失败/已签到返回 0。
    """
    try:
        async with httpx.AsyncClient(timeout=15) as c:
            r = await c.post(f"{COPILOT_BASE}/v2/billing/meter/daily-checkin",
                             json={}, headers={"Authorization": f"Bearer {access_token}"})
            d = r.json()
            return d["data"]["credit"] if d.get("code") == 0 else 0
    except Exception:
        return 0


def check_credits_sync(access_token: str) -> float:
    """check_credits 的同步版（注册流程在同步上下文里用）。"""
    try:
        r = httpx.post(
            f"{COPILOT_BASE}/v2/billing/meter/get-user-resource",
            json={"PageNumber": 1, "PageSize": 100, "ProductCode": "p_tcaca",
                  "Status": [0, 3], "PackageStartTimeRangeBegin": "2024-12-01 21:25:00",
                  "PackageStartTimeRangeEnd": time.strftime("%Y-%m-%d %H:%M:%S")},
            headers={"Authorization": f"Bearer {access_token}"}, timeout=10)
        accounts = r.json().get("data", {}).get("Response", {}).get("Data", {}).get("Accounts", [])
        return sum(float(a.get("CycleCapacityRemainPrecise", 0)) for a in accounts)
    except Exception:
        return -1


def do_checkin_sync(access_token: str) -> int:
    """do_checkin 的同步版。"""
    try:
        r = httpx.post(f"{COPILOT_BASE}/v2/billing/meter/daily-checkin",
                       json={}, headers={"Authorization": f"Bearer {access_token}"}, timeout=10)
        d = r.json()
        return d["data"]["credit"] if d.get("code") == 0 else 0
    except Exception:
        return 0
