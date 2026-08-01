from __future__ import annotations

from datetime import datetime

from sqlalchemy import DateTime, Integer, String
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column


class Base(DeclarativeBase):
    """ORM 基类。"""


class Admin(Base):
    """管理员表。password_hash 映射到旧列名 password，兼容旧表（旧值是 SHA256 hex，新写入用 bcrypt）。"""
    __tablename__ = "admins"

    id: Mapped[int] = mapped_column(Integer, primary_key=True, autoincrement=True)
    username: Mapped[str] = mapped_column(String(64), unique=True, nullable=False)
    # 兼容旧表：旧列名为 password（SHA256 hex），新写入用 bcrypt
    password_hash: Mapped[str] = mapped_column("password", String(255), nullable=False)
    created_at: Mapped[datetime] = mapped_column(DateTime, default=datetime.utcnow)


def init_db():
    """按 Base.metadata 建表（幂等）。延迟 import 避免与 core 循环依赖。"""
    from api.core.database import engine
    Base.metadata.create_all(bind=engine)
