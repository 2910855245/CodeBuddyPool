"""安全横切：bcrypt 密码哈希、JWT 签发/校验、令牌黑名单、鉴权依赖。

只做安全能力，不碰业务；被 routers 层通过 FastAPI Depends 使用。
"""

from __future__ import annotations

import hashlib
import threading
import uuid
from datetime import datetime, timedelta, timezone
from typing import Optional

import bcrypt
import jwt
from fastapi import Depends, Header, HTTPException
from loguru import logger

from config import settings

SECRET_KEY = settings.jwt_secret_key
ALGORITHM = settings.jwt_algorithm
TOKEN_EXPIRE_HOURS = settings.jwt_expire_hours

_token_blacklist_in_memory: dict = {}
_blacklist_lock = threading.Lock()


def _is_blacklisted_in_memory(token_jti: str) -> bool:
    with _blacklist_lock:
        exp = _token_blacklist_in_memory.get(token_jti)
        if exp is None:
            return False
        if datetime.now(timezone.utc).timestamp() > exp:
            del _token_blacklist_in_memory[token_jti]
            return False
        return True


def _add_to_blacklist_in_memory(token_jti: str, expires_in: int):
    with _blacklist_lock:
        exp = datetime.now(timezone.utc).timestamp() + max(expires_in, 60)
        _token_blacklist_in_memory[token_jti] = exp
        if len(_token_blacklist_in_memory) > 1000:
            now = datetime.now(timezone.utc).timestamp()
            expired = [k for k, v in _token_blacklist_in_memory.items() if now > v]
            for k in expired:
                del _token_blacklist_in_memory[k]


def blacklist_token(token: str):
    """把 JWT 加入内存黑名单（登出用），直到其自然过期。

    Args:
        token: 待拉黑的 JWT 字符串；无效 token 直接忽略。
    """
    try:
        payload = jwt.decode(token, SECRET_KEY, algorithms=[ALGORITHM], options={"verify_exp": False})
        token_jti = payload.get("jti")
        if not token_jti:
            return
        exp = payload.get("exp", 0)
        ttl = max(exp - int(datetime.now(timezone.utc).timestamp()), 0)
    except jwt.InvalidTokenError:
        return
    _add_to_blacklist_in_memory(token_jti, ttl)
    logger.info(f"Token 已加入黑名单 jti={token_jti[:16]}")


def is_token_blacklisted(token: str) -> bool:
    try:
        payload = jwt.decode(token, SECRET_KEY, algorithms=[ALGORITHM], options={"verify_exp": False})
        token_jti = payload.get("jti")
        if not token_jti:
            return False
    except jwt.InvalidTokenError:
        return False
    return _is_blacklisted_in_memory(token_jti)


def hash_password(password: str) -> str:
    """用 bcrypt 生成密码哈希。"""
    return bcrypt.hashpw(password.encode("utf-8"), bcrypt.gensalt()).decode("utf-8")


def is_legacy_sha256(password_hash: str) -> bool:
    """判断是否为旧版无盐 SHA256 哈希（64 位 hex，非 bcrypt 前缀）。"""
    return len(password_hash) == 64 and not password_hash.startswith(("$2b$", "$2a$"))


def verify_password(password: str, password_hash: str) -> bool:
    """校验密码。兼容 bcrypt 与旧版无盐 SHA256 两种存储格式。"""
    if password_hash.startswith(("$2b$", "$2a$")):
        return bcrypt.checkpw(password.encode("utf-8"), password_hash.encode("utf-8"))
    # 旧版：无盐 SHA256
    return hashlib.sha256(password.encode()).hexdigest() == password_hash


def create_token(user_id: str, username: str, role: str) -> str:
    """签发 JWT，载荷含 sub/username/role/jti/iat/exp。"""
    payload = {
        "sub": user_id,
        "username": username,
        "role": role,
        "jti": uuid.uuid4().hex,
        "iat": datetime.now(timezone.utc),
        "exp": datetime.now(timezone.utc) + timedelta(hours=TOKEN_EXPIRE_HOURS),
    }
    return jwt.encode(payload, SECRET_KEY, algorithm=ALGORITHM)


def verify_token(token: str) -> Optional[dict]:
    """校验 JWT 并返回用户信息；过期/无效/已拉黑一律返回 None。"""
    if is_token_blacklisted(token):
        return None
    try:
        payload = jwt.decode(token, SECRET_KEY, algorithms=[ALGORITHM])
        return {
            "user_id": payload["sub"],
            "username": payload["username"],
            "role": payload["role"],
        }
    except jwt.ExpiredSignatureError:
        return None
    except jwt.InvalidTokenError:
        return None


def get_current_user(authorization: str = Header(None)):
    """FastAPI 依赖：从 Authorization 头解析并校验 Bearer token。

    Raises:
        HTTPException: 401 未提供令牌或令牌无效/过期。
    """
    if not authorization:
        raise HTTPException(status_code=401, detail="未提供认证令牌")
    token = authorization[len("Bearer "):].strip() if authorization.startswith("Bearer ") else authorization.strip()
    info = verify_token(token)
    if not info:
        raise HTTPException(status_code=401, detail="令牌无效或已过期")
    return {"_token": token, **info}


def get_current_admin(user_info: dict = Depends(get_current_user)):
    """FastAPI 依赖：在 get_current_user 基础上要求 role == admin。

    Raises:
        HTTPException: 403 非管理员。
    """
    if user_info.get("role") != "admin":
        raise HTTPException(status_code=403, detail="需要管理员权限")
    return user_info
