"""SSE 实时推送：面板 → 浏览器。

后台单循环轮询 Go 代理（status/accounts/logs）+ 本地号池，有变化才广播；
N 个浏览器共享同一份轮询，不再各自 setInterval 打后端。
EventSource 无法自定义请求头，鉴权走 ?token= 查询参数。
"""

from __future__ import annotations

import asyncio
import json
from typing import Any

from fastapi import APIRouter, HTTPException, Query
from fastapi.responses import StreamingResponse
from loguru import logger

from api.core.security import verify_token
from api.services import pool_store, proxy_client

router = APIRouter(prefix="/api", tags=["实时推送"])

_POLL_SEC = 3          # 面板 → Go 代理 轮询间隔
_KEEPALIVE_SEC = 20    # 无数据时的心跳间隔，防代理/浏览器断连

_subs: set[asyncio.Queue] = set()
_last_frame: str | None = None
_poll_task: asyncio.Task | None = None


def _collect() -> dict[str, Any]:
    """组装一帧监控数据（与 /proxy-status、/proxy/accounts、/proxy/logs、/pool 同源）。"""
    accs = pool_store.read_accounts()
    pool: dict[str, Any] = {"total": len(accs), "available": 0, "exhausted": 0, "dead": 0, "credits": 0.0}
    out = []
    for a in accs:
        st = pool_store.account_status(a)
        pool[st] += 1
        pool["credits"] += max(a.get("credits_total", 0), 0)
        out.append(pool_store.public_account(a))
    pool["credits"] = round(pool["credits"], 1)
    pool["accounts"] = out
    return {"pool": pool}


async def _collect_async() -> dict[str, Any]:
    status, accounts, logs, pool = await asyncio.gather(
        proxy_client.proxy_status(),
        proxy_client.proxy_accounts(),
        proxy_client.proxy_logs(100),
        asyncio.to_thread(_collect),
    )
    return {"status": status, "accounts": accounts, "logs": logs, "pool": pool["pool"]}


async def _poll_loop() -> None:
    """唯一后台轮询：有变化才广播给所有订阅者；异常不退出。"""
    global _last_frame
    while True:
        try:
            snap = await _collect_async()
            frame = json.dumps(snap, ensure_ascii=False, default=str)
            if frame != _last_frame:
                _last_frame = frame
                dead = []
                for q in _subs:
                    try:
                        q.put_nowait(frame)
                    except asyncio.QueueFull:
                        dead.append(q)  # 客户端消费太慢，踢掉让其重连
                for q in dead:
                    _subs.discard(q)
        except Exception as e:
            logger.warning(f"SSE 轮询异常: {e}")
        await asyncio.sleep(_POLL_SEC)


def _ensure_poll_task() -> None:
    global _poll_task
    if _poll_task is None or _poll_task.done():
        _poll_task = asyncio.create_task(_poll_loop())


@router.get("/stream/monitor")
async def stream_monitor(token: str = Query(...)):
    """监控数据 SSE 流：登录后实时推送代理状态/账号/日志/号池，有变化才推。"""
    if not verify_token(token):
        raise HTTPException(status_code=401, detail="令牌无效或已过期")
    _ensure_poll_task()

    queue: asyncio.Queue = asyncio.Queue(maxsize=8)
    _subs.add(queue)

    async def gen():
        try:
            # 连接即推当前快照（可能为 None：代理尚未首轮返回，等下一轮）
            if _last_frame is not None:
                yield f"data: {_last_frame}\n\n"
            while True:
                try:
                    frame = await asyncio.wait_for(queue.get(), timeout=_KEEPALIVE_SEC)
                    yield f"data: {frame}\n\n"
                except asyncio.TimeoutError:
                    yield ": keepalive\n\n"
        finally:
            _subs.discard(queue)

    return StreamingResponse(
        gen(),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )
