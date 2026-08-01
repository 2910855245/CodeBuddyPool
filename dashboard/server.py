#!/usr/bin/env python3
"""CodeBuddy 号池管理面板 - 瘦启动器

业务代码在 api/ 包，按分层组织：
  api/main.py        组装层（FastAPI 实例 + lifespan + 中间件 + 路由 + SPA 托管）
  api/schemas.py     DTO（请求/响应模型）
  api/core/          基础设施横切（database.py 引擎/会话；security.py JWT/密码/鉴权）
  api/db/            数据层（models.py ORM；repository.py 数据访问）
  api/services/      业务逻辑
  api/routers/       HTTP 接口层

启动:
  python server.py            # 读取 .env / 环境变量 DASHBOARD_PORT (默认 9100)
"""
from __future__ import annotations

import os
import sys
from pathlib import Path

BASE_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(BASE_DIR))

from api.main import app  # noqa: F401  (config 校验在这里完成)
from config import settings


if __name__ == "__main__":
    from granian import Granian
    from granian.constants import Interfaces

    port = int(os.environ.get("DASHBOARD_PORT", settings.port))
    Granian(
        "api.main:app",
        address=settings.host,
        port=port,
        interface=Interfaces.ASGI,
        workers=1,
    ).serve()
