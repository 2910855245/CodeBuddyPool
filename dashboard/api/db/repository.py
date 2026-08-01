"""数据访问层（repository）：Admin 表的增删改查。

表结构在 lifespan 的 init_db_safe() 建立；连接失败在首次使用时抛错。
"""

from __future__ import annotations

from contextlib import contextmanager
from typing import Optional

from loguru import logger
from sqlalchemy import select
from sqlalchemy.orm import Session

from api.core.database import SessionLocal
from api.db.models import Admin


class Database:
    """Admin 数据访问。每个方法独立开/提交/关会话，异常回滚。"""

    def __init__(self):
        self._session_factory = SessionLocal

    @contextmanager
    def _session_scope(self):
        session: Session = self._session_factory()
        try:
            yield session
            session.commit()
        except Exception:
            logger.exception("_session_scope 失败")
            session.rollback()
            raise
        finally:
            session.close()

    def get_admin_by_username(self, username: str) -> Optional[Admin]:
        """按用户名查管理员，查到即 detach 后返回；无则 None。"""
        with self._session_scope() as s:
            admin = s.scalar(select(Admin).where(Admin.username == username))
            if admin:
                s.expunge(admin)
            return admin

    def create_admin(self, username: str, password_hash: str) -> Admin:
        """新建管理员并返回（已 detach）。"""
        with self._session_scope() as s:
            admin = Admin(username=username, password_hash=password_hash)
            s.add(admin)
            s.flush()
            s.expunge(admin)
            return admin

    def update_admin_password(self, username: str, password_hash: str):
        """按用户名更新密码哈希；用户不存在则静默跳过。"""
        with self._session_scope() as s:
            admin = s.scalar(select(Admin).where(Admin.username == username))
            if admin:
                admin.password_hash = password_hash


db = Database()
