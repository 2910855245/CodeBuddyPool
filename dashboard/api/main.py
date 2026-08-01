"""组装层：FastAPI 实例 + lifespan + 中间件（CORS/限流/禁缓存）+ 路由注册 + SPA 托管。"""

from __future__ import annotations

import os
import sys
import time
from collections import defaultdict, deque
from contextlib import asynccontextmanager
from pathlib import Path

from fastapi import FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import FileResponse, JSONResponse
from loguru import logger

from config import DIST_DIR, LOGS_DIR, settings

# ---------- 日志配置 ----------
logger.remove()
logger.add(
    sys.stderr,
    level="DEBUG" if settings.debug else "INFO",
    format="<green>{time:YYYY-MM-DD HH:mm:ss}</green> | <level>{level: <8}</level> | <cyan>{name}</cyan>:<cyan>{function}</cyan>:<cyan>{line}</cyan> - <level>{message}</level>",
)
logger.add(
    LOGS_DIR / "app_{time:YYYY-MM-DD}.log",
    rotation="10 MB",
    retention="30 days",
    level="INFO",
    encoding="utf-8",
)
logger.add(
    LOGS_DIR / "error_{time:YYYY-MM-DD}.log",
    rotation="10 MB",
    retention="30 days",
    level="ERROR",
    encoding="utf-8",
)

# ---------- 内存滑动窗口限流（每 IP） ----------
_rate_buckets: dict[str, deque] = defaultdict(deque)


def _rate_limited(ip: str) -> bool:
    now = time.time()
    window = settings.rate_limit_window_seconds
    q = _rate_buckets[ip]
    while q and now - q[0] > window:
        q.popleft()
    if len(q) >= settings.rate_limit_requests:
        return True
    q.append(now)
    return False


@asynccontextmanager
async def lifespan(app: FastAPI):
    logger.info(f"dashboard 启动 host={settings.host} port={settings.port}")
    from api.core.database import init_db_safe
    init_db_safe()

    from api.services import daemon as daemon_svc
    from api.services.settings_store import load_settings

    if load_settings().get("auto_daemon"):
        logger.info("auto_daemon=true，自动拉起注册守护进程")
        await daemon_svc.start_daemon()
    yield
    logger.info("dashboard 关闭")


app = FastAPI(title="CodeBuddy 号池管理面板", lifespan=lifespan)

# ---------- CORS ----------
origins = [o.strip() for o in settings.cors_origins.split(",") if o.strip()]
app.add_middleware(
    CORSMiddleware,
    allow_origins=origins,
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)


# ---------- 限流中间件 ----------
@app.middleware("http")
async def rate_limit_middleware(request: Request, call_next):
    if request.url.path.startswith("/api/"):
        ip = request.client.host if request.client else "unknown"
        if _rate_limited(ip):
            return JSONResponse(
                status_code=429,
                content={"success": False, "message": "请求过于频繁，请稍后再试", "data": None},
            )
    return await call_next(request)


# ---------- 禁用 API 缓存 ----------
@app.middleware("http")
async def no_cache_middleware(request: Request, call_next):
    response = await call_next(request)
    if request.url.path.startswith("/api/"):
        response.headers["Cache-Control"] = "no-store, no-cache, must-revalidate"
        response.headers["Pragma"] = "no-cache"
    return response


# ---------- 路由 ----------
from api.routers import auth, daemon, keys, pool, proxy, register_page, settings as settings_router, skill, stream

app.include_router(auth.router)
app.include_router(pool.router)
app.include_router(settings_router.router)
app.include_router(daemon.router)
app.include_router(proxy.router)
app.include_router(keys.router)
app.include_router(register_page.router)
app.include_router(skill.router)
app.include_router(stream.router)


# ---------- 静态托管（Vue 构建产物 + SPA fallback） ----------
@app.get("/", include_in_schema=False)
async def index():
    index_file = DIST_DIR / "index.html"
    if index_file.exists():
        return FileResponse(index_file)
    return JSONResponse(
        status_code=200,
        content={"success": True, "message": "dashboard API 运行中（前端未构建，见 dashboard/vue-ui）", "data": None},
    )


@app.get("/{full_path:path}", include_in_schema=False)
async def spa_fallback(full_path: str):
    if full_path.startswith("api/"):
        return JSONResponse(status_code=404, content={"success": False, "message": "接口不存在", "data": None})
    # 路径穿越防护
    base = os.path.normpath(str(DIST_DIR))
    target = os.path.normpath(os.path.join(base, full_path))
    if not target.startswith(base):
        return JSONResponse(status_code=403, content={"success": False, "message": "禁止访问", "data": None})
    if os.path.isfile(target):
        return FileResponse(target)
    index_file = DIST_DIR / "index.html"
    if index_file.exists():
        return FileResponse(index_file)
    return JSONResponse(status_code=404, content={"success": False, "message": "前端未构建", "data": None})
