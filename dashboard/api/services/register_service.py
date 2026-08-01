"""分享注册流程：手机号 → 发 CodeBuddy 短信 → 提交验证码 → 登录换 token → 签到入池。

_pending_reg 存放进行中的注册会话（phone -> state/cookies），提交后清理。
"""

from __future__ import annotations

import base64
import json
import re
import time

import httpx

from config import ROOT, settings
from api.services import pool_store
from api.services.codebuddy_api import do_checkin_sync

_pending_reg: dict = {}  # phone -> {"state","cookies","phone","created_at"}
_REG_TTL = 1800  # pending 会话过期秒数 (30 分钟, 留足用户收码时间)

UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"


def _cleanup_pending() -> None:
    """清理过期的 pending 注册会话, 避免内存泄漏。"""
    now = time.time()
    expired = [k for k, v in _pending_reg.items() if now - v.get("created_at", 0) > _REG_TTL]
    for k in expired:
        _pending_reg.pop(k, None)


def _normalize_phone(phone: str) -> str:
    """手机号归一化：去所有空白字符，无 + 前缀时补 +86（start/submit 用同一 key 存取会话）。"""
    phone = re.sub(r"\s+", "", phone)
    if phone and not phone.startswith("+"):
        phone = "+86" + phone
    return phone


def start_register(phone: str) -> dict:
    """第一步：发 CodeBuddy 短信，保存 state/cookies 等验证码。

    Args:
        phone: 手机号；无 + 前缀时自动补 +86。

    Returns:
        {"ok": True, "phone", "message"} 或 {"ok": False, "error"}。
    """
    phone = _normalize_phone(phone)
    if not phone:
        return {"ok": False, "error": "请输入手机号"}

    _cleanup_pending()

    session = httpx.Client(timeout=30, follow_redirects=True)
    session.headers.update({"User-Agent": UA})

    r = session.post("https://copilot.tencent.com/v2/plugin/auth/state?platform=agents", json={})
    state = r.json()["data"]["state"]

    session.get(f"https://copilot.tencent.com/login?platform=agents&state={state}")
    session.get("https://www.codebuddy.cn/auth/realms/copilot/protocol/openid-connect/auth", params={
        "client_id": "console", "state": state,
        "redirect_uri": "https://www.codebuddy.cn/login/?platform=agents",
        "response_type": "code", "scope": "openid profile email offline_access",
    })
    resp = session.get("https://www.codebuddy.cn/auth/realms/copilot/sms/authentication-code",
                       params={"phoneNumber": phone})
    sms_data = resp.json()

    _pending_reg[phone] = {
        "phone": phone,
        "state": state,
        "cookies": dict(session.cookies.items()),
        "created_at": time.time(),
    }
    return {"ok": True, "phone": phone,
            "message": f"短信已发送到 {phone}, 有效 {sms_data.get('expires_in', 60)} 秒"}


async def submit_register(phone: str, code: str) -> dict:
    """第二步：提交验证码，完成登录 + 签到 + 入池。

    Args:
        phone: 与 start_register 一致的手机号（用于取暂存的 state/cookies）。
        code: 短信验证码。

    Returns:
        {"ok": True, "phone", "credits", "bonus", "message"} 或 {"ok": False, "error"}。
        无论成败最后都清掉该 phone 的暂存会话。
    """
    phone = _normalize_phone(phone)
    code = code.strip()
    pending = _pending_reg.get(phone)
    if not pending:
        return {"ok": False, "error": "该号码未发起注册或已过期, 请重新开始"}

    # 重复账号拦截：按数字部分比对，避免 +86 / 裸号格式差异绕过
    if pool_store.find_account_normalized(phone):
        _pending_reg.pop(phone, None)
        return {"ok": False, "error": "该手机号已在号池中, 请勿重复入号", "already_in_pool": True}

    session = httpx.Client(timeout=30, follow_redirects=False)
    session.headers.update({"User-Agent": UA})
    for k, v in pending["cookies"].items():
        session.cookies.set(k, v)
    state = pending["state"]

    try:
        # Step 4: 提交验证码
        r4 = session.get("https://www.codebuddy.cn/auth/realms/copilot/protocol/openid-connect/auth", params={
            "client_id": "console",
            "redirect_uri": "https://www.codebuddy.cn/login/?platform=agents",
            "state": state, "response_type": "code", "scope": "openid profile email offline_access",
        })
        m = re.search(r'action="([^"]+)"', r4.text)
        if not m:
            return {"ok": False, "error": "验证页面异常, 请重新开始"}
        action = m.group(1).replace("&amp;", "&")

        r5 = session.post(action, data={
            "phoneActivated": "true", "phoneNumber": phone, "code": code,
        }, headers={"Content-Type": "application/x-www-form-urlencoded"})

        if r5.status_code not in (302, 303):
            err = re.findall(r'id="input-error"[^>]*>([^<]{3,200})', r5.text)
            return {"ok": False, "error": err[0].strip() if err else "验证码错误"}

        # Step 5: 静默登录（跟随重定向）
        s2 = httpx.Client(timeout=30, follow_redirects=True)
        s2.headers.update({"User-Agent": "Mozilla/5.0"})
        for k, v in pending["cookies"].items():
            s2.cookies.set(k, v)
        s2.cookies.update(session.cookies)
        s2.get("https://www.codebuddy.cn/console/accounts")

        # Step 6: 换取 token
        r6 = s2.post(f"https://www.codebuddy.cn/console/login/enterprise?state={state}",
                     headers={"Content-Type": "application/json",
                              "X-Product-Code": "agents", "X-From-Promotion": "1"})
        if r6.json().get("code") != 0:
            return {"ok": False, "error": f"token 交换失败: {r6.json()}"}

        token = r6.json()["data"]
        at = token["accessToken"]
        payload = at.split(".")[1]
        payload += "=" * (4 - len(payload) % 4) if len(payload) % 4 else ""
        dec = json.loads(base64.urlsafe_b64decode(payload))
        uid = dec.get("sub", "")

        # 查积分 + 签到
        # 服务器 IP 可能被账单接口风控返回 0, 导致新号被误判无积分。
        # 新号默认 2000 积分, 跳过账单查询, 只做签到。
        credits = 2000.0
        bonus = do_checkin_sync(at)
        if bonus:
            credits += bonus

        # 入池: 先写 accounts.jsonl 持久化, 再调 Go inject 注入内存
        # 只调 inject 不写文件的话, Go 30s 热加载会把新号从内存剔除
        acct = {
            "email": phone.replace("+", "") + "@codebuddy.local",
            "phone": phone, "uid": uid,
            "access_token": at, "refresh_token": token.get("refreshToken", ""),
            "expires_at": dec.get("exp", 0) * 1000,
            "credits_total": credits,
            "registered_at": time.strftime("%Y-%m-%dT%H:%M:%SZ"),
        }
        pool_store.append_account(acct)
        async with httpx.AsyncClient(timeout=10) as c:
            r = await c.post(f"{settings.proxy_base}/api/register/inject", json=acct)
            if r.status_code != 200:
                return {"ok": False, "error": f"入池失败: {r.text[:200]}"}

        _pending_reg.pop(phone, None)
        return {"ok": True, "phone": phone, "credits": round(credits, 1),
                "bonus": bonus, "message": f"注册成功! 积分: {credits:.0f}"}

    except Exception as e:
        return {"ok": False, "error": str(e)[:200]}
    finally:
        _pending_reg.pop(phone, None)
