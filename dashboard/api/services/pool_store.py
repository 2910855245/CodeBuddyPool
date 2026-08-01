"""号池存取：accounts.jsonl 的读/写/追加/状态推导/读-改-写合并。

Go 代理 30s 热加载本文件，这里用锁 + 原子写（tmp+replace）缓解竞态。
"""

from __future__ import annotations

import asyncio
import json
import os
import re
import time
from pathlib import Path
from typing import Optional

from config import ACCOUNTS_FILE

_file_lock = asyncio.Lock()


def read_accounts() -> list:
    """读全部账号，坏行跳过；文件不存在返回空列表。"""
    if not ACCOUNTS_FILE.exists():
        return []
    accs = []
    with open(ACCOUNTS_FILE, encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if line:
                try:
                    accs.append(json.loads(line))
                except Exception:
                    pass
    return accs


def write_accounts(accs: list):
    """整文件覆写：先写 tmp 再 os.replace，保证原子性（防 Go 读到半截）。"""
    ACCOUNTS_FILE.parent.mkdir(parents=True, exist_ok=True)
    tmp = ACCOUNTS_FILE.with_suffix(".jsonl.tmp")
    with open(tmp, "w", encoding="utf-8") as f:
        for a in accs:
            f.write(json.dumps(a, ensure_ascii=False) + "\n")
    os.replace(tmp, ACCOUNTS_FILE)


def append_account(acct: dict):
    """追加单条账号（新注册用），不整文件重写。"""
    ACCOUNTS_FILE.parent.mkdir(parents=True, exist_ok=True)
    with open(ACCOUNTS_FILE, "a", encoding="utf-8") as f:
        f.write(json.dumps(acct, ensure_ascii=False) + "\n")


def account_status(a: dict) -> str:
    """推导账号状态：dead（显式标记/无 token/过期无 refresh）/ exhausted（积分≤0）/ available。"""
    if a.get("dead"):
        return "dead"
    if not a.get("access_token"):
        return "dead"
    exp = a.get("expires_at", 0)
    if exp and exp < time.time() * 1000 and not a.get("refresh_token"):
        return "dead"
    if a.get("credits_total", 0) <= 0:
        return "exhausted"
    return "available"


def public_account(a: dict) -> dict:
    """投影出前端展示字段（脱敏：不带 token）。"""
    return {
        "phone": a.get("phone", "?"),
        "uid": a.get("uid", "")[:16],
        "credits": round(a.get("credits_total", 0), 1),
        "registered": a.get("registered_at", "")[:10],
        "expires_at": a.get("expires_at", 0),
        "has_refresh": bool(a.get("refresh_token")),
        "dead": bool(a.get("dead")),
        "status": account_status(a),
    }


def get_account(phone: str) -> Optional[dict]:
    """按手机号查账号，无则 None。"""
    for a in read_accounts():
        if a.get("phone") == phone:
            return a
    return None


def _digits(phone: str) -> str:
    """提取手机号数字部分，用于跨格式比对（+86xxx / 86xxx / xxx 视为同一号）。"""
    return re.sub(r"\D", "", phone or "")


def find_account_normalized(phone: str) -> Optional[dict]:
    """按归一化手机号（数字部分）查账号，无则 None。用于入号前重复检测。"""
    target = _digits(phone)
    if not target:
        return None
    for a in read_accounts():
        if _digits(a.get("phone", "")) == target:
            return a
    return None


async def mutate_accounts(mutator):
    """读-改-写合并：在锁内读最新文件、应用 mutator(accs)->accs|None、原子写回。

    缓解与 Go 代理 30s 热加载的竞态（Go 以本文件为准，这里保证写入基于最新内容）。

    Args:
        mutator: 接收账号列表，返回要写回的新列表；返回 None 表示不改。

    Returns:
        写回后的列表；mutator 返回 None 时返回原列表。
    """
    async with _file_lock:
        accs = read_accounts()
        result = mutator(accs)
        if result is not None:
            write_accounts(result)
        return result if result is not None else accs
