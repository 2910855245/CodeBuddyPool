"""数据库基础设施：engine（连接池）+ SessionLocal（会话工厂）+ init_db_safe（启动建表）。

core 层只做引擎与会话，不依赖 db 层（避免循环 import）。
"""

from __future__ import annotations

from loguru import logger
from sqlalchemy import create_engine
from sqlalchemy.orm import sessionmaker

from config import settings

USE_MYSQL = settings.mysql_url.startswith("mysql")

if USE_MYSQL:
    engine = create_engine(
        settings.mysql_url,
        echo=False,
        pool_size=5,
        max_overflow=10,
        pool_recycle=3600,
        pool_pre_ping=True,
    )
else:
    engine = create_engine(settings.mysql_url, echo=False)

SessionLocal = sessionmaker(bind=engine, autoflush=False, autocommit=False)


def init_db_safe() -> bool:
    """启动时尝试建表；MySQL 不可用时只告警不阻塞面板启动。

    Returns:
        建表成功返回 True，失败返回 False（管理员登录将不可用）。
    """
    try:
        # 延迟 import，避免 core -> db 的循环依赖
        from api.db.models import init_db
        init_db()
        logger.info("MySQL 连接正常，admins 表就绪")
        return True
    except Exception as e:
        logger.warning(f"MySQL 暂不可用（管理员登录将不可用）: {type(e).__name__}: {e}")
        return False
