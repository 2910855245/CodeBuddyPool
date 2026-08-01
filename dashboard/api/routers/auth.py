"""管理员认证路由：登录（含旧 SHA256 自动升级 bcrypt）、校验、登出（拉黑 token）。"""

from __future__ import annotations

from fastapi import APIRouter, Depends
from loguru import logger

from api.core import security as auth_mod
from api.db.repository import db
from api.schemas import AdminLoginRequest, ApiResponse, ChangePasswordRequest, TokenRequest

router = APIRouter(prefix="/api/admin", tags=["管理员认证"])


@router.post("/login", response_model=ApiResponse)
def admin_login(req: AdminLoginRequest):
    try:
        admin = db.get_admin_by_username(req.username)
    except Exception as e:
        logger.warning(f"登录查询失败 error={type(e).__name__}: {e}")
        return ApiResponse.fail("数据库连接失败，请确认 MySQL 已启动")
    if not admin or not auth_mod.verify_password(req.password, admin.password_hash):
        return ApiResponse.fail("用户名或密码错误")
    # 旧 SHA256 账号登录成功后自动重哈希为 bcrypt
    if auth_mod.is_legacy_sha256(admin.password_hash):
        try:
            db.update_admin_password(req.username, auth_mod.hash_password(req.password))
            logger.info(f"管理员密码已升级为 bcrypt username={req.username}")
        except Exception as e:
            logger.warning(f"密码重哈希失败 error={e}")
    token = auth_mod.create_token(str(admin.id), admin.username, "admin")
    return ApiResponse.ok({"token": token, "username": admin.username}, "登录成功")


@router.post("/verify", response_model=ApiResponse)
def admin_verify(admin: dict = Depends(auth_mod.get_current_admin)):
    return ApiResponse.ok({"username": admin["username"]})


@router.post("/logout", response_model=ApiResponse)
def admin_logout(admin: dict = Depends(auth_mod.get_current_admin)):
    token = admin.get("_token")
    if token:
        auth_mod.blacklist_token(token)
    return ApiResponse.ok(message="已退出")


@router.post("/change-password", response_model=ApiResponse)
def admin_change_password(req: ChangePasswordRequest, admin: dict = Depends(auth_mod.get_current_admin)):
    username = admin["username"]
    try:
        current = db.get_admin_by_username(username)
    except Exception as e:
        logger.warning(f"查询管理员失败 error={type(e).__name__}: {e}")
        return ApiResponse.fail("数据库连接失败")
    if not current or not auth_mod.verify_password(req.old_password, current.password_hash):
        return ApiResponse.fail("旧密码错误")
    if req.old_password == req.new_password:
        return ApiResponse.fail("新密码不能与旧密码相同")
    try:
        db.update_admin_password(username, auth_mod.hash_password(req.new_password))
        logger.info(f"管理员密码已修改 username={username}")
        return ApiResponse.ok(message="密码修改成功")
    except Exception as e:
        logger.warning(f"修改密码失败 error={e}")
        return ApiResponse.fail("修改密码失败，请重试")
