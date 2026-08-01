"""Go 代理 admin API 转发：面板 → Go proxy（/api/admin/*）+ /stats 在线状态。"""

from __future__ import annotations

from urllib.parse import quote

import httpx

from api.services.settings_store import load_settings


async def proxy_call(method: str, path: str, json: dict | None = None) -> dict:
    """转发到 Go 代理 admin API（Go 挂在 /api/admin/* 下），带 Bearer proxy_key。

    Returns:
        Go 返回的 JSON；连接失败/异常返回 {"online": False, "error": ...}。
    """
    s = load_settings()
    try:
        async with httpx.AsyncClient(timeout=8) as c:
            r = await c.request(method, f"{s['proxy_base']}{path}",
                                headers={"Authorization": f"Bearer {s['proxy_key']}"},
                                json=json)
            return r.json()
    except httpx.ConnectError:
        return {"online": False, "error": "proxy unreachable"}
    except Exception as e:
        return {"online": False, "error": str(e)[:120]}


async def proxy_status() -> dict:
    """Go 代理在线状态（无鉴权 /stats），3s 超时。"""
    s = load_settings()
    try:
        async with httpx.AsyncClient(timeout=3) as c:
            r = await c.get(f"{s['proxy_base']}/stats")
            return {"online": True, **r.json()}
    except Exception:
        return {"online": False}


async def proxy_accounts() -> dict:
    return await proxy_call("GET", "/api/admin/accounts")


async def proxy_logs(n: int = 100) -> dict:
    return await proxy_call("GET", f"/api/admin/logs?n={n}")


async def proxy_checkin() -> dict:
    return await proxy_call("POST", "/api/admin/checkin")


async def proxy_reload() -> dict:
    return await proxy_call("POST", "/api/admin/reload")


# ---------- API 密钥管理（转发到 Go 代理 keymgr） ----------

async def keys_list() -> dict:
    return await proxy_call("GET", "/api/admin/keys")


async def keys_create(name: str) -> dict:
    return await proxy_call("POST", "/api/admin/keys", json={"name": name})


async def keys_toggle(masked_key: str) -> dict:
    # 脱敏 key 含 "."，必须 URL 编码
    return await proxy_call("PUT", f"/api/admin/keys/{quote(masked_key, safe='')}/toggle")


async def keys_delete(masked_key: str) -> dict:
    return await proxy_call("DELETE", f"/api/admin/keys/{quote(masked_key, safe='')}")


async def key_usage(masked_key: str) -> dict:
    return await proxy_call("GET", f"/api/admin/usage/{quote(masked_key, safe='')}")
