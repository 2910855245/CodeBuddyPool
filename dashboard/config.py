from __future__ import annotations

import os
from functools import lru_cache
from pathlib import Path

from dotenv import load_dotenv
from pydantic import Field
from pydantic_settings import BaseSettings

BASE_DIR = Path(__file__).resolve().parent
ROOT = BASE_DIR.parent

load_dotenv(BASE_DIR / ".env")


class Settings(BaseSettings):
    # 必填：JWT 签名密钥（.env 里配置）
    jwt_secret_key: str = Field(default="", alias="JWT_SECRET_KEY")
    jwt_algorithm: str = Field(default="HS256", alias="JWT_ALGORITHM")
    jwt_expire_hours: int = Field(default=72, alias="JWT_EXPIRE_HOURS")

    # 服务
    host: str = Field(default="0.0.0.0", alias="DASHBOARD_HOST")
    port: int = Field(default=9100, alias="DASHBOARD_PORT")
    debug: bool = Field(default=False, alias="DEBUG")
    cors_origins: str = Field(default="http://localhost:5173,http://127.0.0.1:5173", alias="CORS_ORIGINS")

    # 数据库（默认沿用现有 MySQL codebuddy_pool）
    mysql_url: str = Field(default="mysql+pymysql://root:@127.0.0.1:3306/codebuddy_pool", alias="MYSQL_URL")

    # 限流
    rate_limit_requests: int = Field(default=600, alias="RATE_LIMIT_REQUESTS")
    rate_limit_window_seconds: int = Field(default=60, alias="RATE_LIMIT_WINDOW_SECONDS")

    # Go 代理
    proxy_base: str = Field(default="http://127.0.0.1:9091", alias="PROXY_BASE")
    proxy_key: str = Field(default="", alias="PROXY_KEY")

    model_config = {"env_file": str(BASE_DIR / ".env"), "extra": "ignore"}


@lru_cache
def get_settings() -> Settings:
    return Settings()


settings = get_settings()

# 数据文件路径
ACCOUNTS_FILE = Path(os.environ.get("ACCOUNTS_FILE", ROOT / "go-proxy-cto" / "accounts.jsonl"))
POOL_SETTINGS_FILE = ROOT / "pool_settings.json"
DIST_DIR = BASE_DIR / "dist"
LOGS_DIR = BASE_DIR / "logs"
LOGS_DIR.mkdir(exist_ok=True)

# 启动校验必填项
_missing = []
if not settings.jwt_secret_key:
    _missing.append("JWT_SECRET_KEY")
if _missing:
    import sys

    from loguru import logger
    logger.error(f"缺少必填环境变量 missing={_missing}，请在 dashboard/.env 中配置（参考 .env.example）")
    sys.exit(1)
