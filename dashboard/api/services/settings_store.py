"""自动化设置存取：pool_settings.json 的读写。默认值从 .env 取，可在面板覆盖。"""

from __future__ import annotations

import json
import os

from config import POOL_SETTINGS_FILE, settings

DEFAULT_SETTINGS = {
    "target_pool": 5,
    "register_interval": 60,
    "checkin_interval": 3600,
    "auto_daemon": False,
    "proxy_base": settings.proxy_base,
    "proxy_key": settings.proxy_key,
}


def load_settings() -> dict:
    """读设置：默认值 + 文件覆盖；文件损坏时静默回退默认值。"""
    s = dict(DEFAULT_SETTINGS)
    if POOL_SETTINGS_FILE.exists():
        try:
            s.update(json.loads(POOL_SETTINGS_FILE.read_text(encoding="utf-8")))
        except Exception:
            pass
    return s


def save_settings(s: dict):
    """原子写设置（tmp + replace）。"""
    tmp = POOL_SETTINGS_FILE.with_suffix(".json.tmp")
    tmp.write_text(json.dumps(s, ensure_ascii=False, indent=2), encoding="utf-8")
    os.replace(tmp, POOL_SETTINGS_FILE)
