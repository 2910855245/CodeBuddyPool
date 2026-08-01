"""注册守护进程管理：起/停 codebuddy_register.py --daemon 子进程，并收集其 stdout 日志。"""

from __future__ import annotations

import asyncio
import collections
import os
import sys
import time

from config import ROOT
from api.services.settings_store import load_settings

_daemon = {
    "proc": None,
    "started_at": 0.0,
    "log": collections.deque(maxlen=300),
}


async def _pump_log(stream):
    """把子进程 stdout 逐行读进内存环形日志（带时间戳）。"""
    while True:
        line = await stream.readline()
        if not line:
            break
        text = line.decode(errors="replace").rstrip()
        if text:
            _daemon["log"].append(f"[{time.strftime('%H:%M:%S')}] {text}")


def daemon_running() -> bool:
    """守护进程是否存活（已拉起且未退出）。"""
    p = _daemon["proc"]
    return p is not None and p.returncode is None


async def start_daemon() -> dict:
    """拉起注册守护进程。

    Returns:
        {"ok": True, "pid": ...} 或 {"ok": False, "error": ...}（已在跑）。
    """
    if daemon_running():
        return {"ok": False, "error": "守护进程已在运行"}
    s = load_settings()
    env = dict(os.environ)
    proc = await asyncio.create_subprocess_exec(
        sys.executable, str(ROOT / "codebuddy_register.py"), "--daemon",
        "--target", str(s["target_pool"]),
        "--interval", str(s["register_interval"]),
        "--checkin-interval", str(s["checkin_interval"]),
        stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.STDOUT,
        cwd=str(ROOT), env=env)
    _daemon["proc"] = proc
    _daemon["started_at"] = time.time()
    _daemon["log"].append(f"[{time.strftime('%H:%M:%S')}] === 守护进程启动 pid={proc.pid} ===")
    asyncio.create_task(_pump_log(proc.stdout))
    return {"ok": True, "pid": proc.pid}


async def stop_daemon() -> dict:
    """停止守护进程：先 terminate，5s 未退则 kill。"""
    if not daemon_running():
        return {"ok": False, "error": "守护进程未在运行"}
    _daemon["proc"].terminate()
    try:
        await asyncio.wait_for(_daemon["proc"].wait(), timeout=5)
    except asyncio.TimeoutError:
        _daemon["proc"].kill()
    _daemon["log"].append(f"[{time.strftime('%H:%M:%S')}] === 守护进程已停止 ===")
    return {"ok": True}


def daemon_status() -> dict:
    """运行状态快照：running/pid/uptime/最近 80 行日志。"""
    uptime = int(time.time() - _daemon["started_at"]) if daemon_running() else 0
    return {"running": daemon_running(),
            "pid": _daemon["proc"].pid if daemon_running() else None,
            "uptime_sec": uptime,
            "log": list(_daemon["log"])[-80:]}
