# Dashboard（号池管理后台）

按刷课平台后端架构分层 + Vue3 前端重写的管理后台。

## 技术栈

- 后端：FastAPI + granian（ASGI），SQLAlchemy + PyMySQL（MySQL），JWT（pyjwt），bcrypt，loguru，pydantic-settings。
- 前端：Vue 3 + TypeScript + Vite + Pinia + Vue Router（hash 模式），构建产物到 `dashboard/dist/`，由后端 SPA 托管。

## 目录结构

```
dashboard/
├── server.py              # 瘦启动器（granian 入口）
├── config.py              # pydantic-settings 读 .env（项目根级，api/* 都 from config import settings）
├── .env                   # 实际配置（勿提交；用 bash heredoc 写，Write 工具拒 .env）
├── .env.example
├── api/                   # 后端分层（依赖单向：routers → services → db → core）
│   ├── main.py            # 组装层：FastAPI 实例 + lifespan + 中间件 + 路由注册 + SPA 托管
│   ├── schemas.py         # DTO：ApiResponse + 各请求/响应模型
│   ├── core/              # 基础设施横切（无业务）
│   │   ├── database.py    # engine + SessionLocal + init_db_safe
│   │   └── security.py    # JWT + bcrypt 密码哈希 + 黑名单 + 鉴权依赖
│   ├── db/
│   │   ├── models.py      # ORM：Base + Admin（init_db 只 create_all 建表）
│   │   └── repository.py  # 数据访问 Database 类（Admin 增删改查）
│   ├── services/          # 业务逻辑：pool_store/codebuddy_api/daemon/proxy_client/register_service/settings_store
│   └── routers/           # HTTP 接口层：auth/pool/settings/daemon/proxy/register_page
├── vue-ui/                # 前端源码
│   ├── src/{main.ts,router,store,api,composables,views}
│   └── vite.config.ts     # dev 5173 proxy /api→9100
└── dist/                  # 前端 build 产物（后端托管）
```

## 启动 / 运维

- 起后端：`cd dashboard && python server.py`（端口 9100）。
- 前端改动后必须 build 才生效：`cd vue-ui && npm run build`（vue-tsc --noEmit && vite build → 产物到 `dashboard/dist`）。
- 前端联调：`cd vue-ui && npm run dev`（5173，代理 /api→9100）。

## 环境 / 凭据

- Python 解释器：使用系统 `python`（Python 3.10+，已安装 `requirements.txt` 依赖）。
- MySQL：默认 `127.0.0.1:3306`，库 `codebuddy_pool`（utf8mb4）。在 `dashboard/.env` 配置 `MYSQL_URL`（含你的数据库用户名/密码），示例见 `.env.example`。
- 默认管理员：`admin / admin`（bcrypt 存 `admins.password_hash`）。**首次部署后请立即登录改密。**
- 登录拿 token：`POST /api/admin/login`，之后请求带 `Authorization: Bearer <token>`；前端 token 存 localStorage `admin_token`。

## 注意

- 数据面：`accounts.jsonl` 未改结构，只加了 asyncio 锁 + 读-改-写合并，防并发写丢数据。
- 令牌刷新：`POST /api/refresh/{phone}` 与 `/api/refresh-all` 会先按 Go `ensureFreshToken` 逻辑刷令牌——剩余有效期 < `TOKEN_REFRESH_TTL`（3600s，`api/services/codebuddy_api.py`）才发起 OIDC 刷新并写回 `access_token/refresh_token/expires_at`；refresh_token 被服务端拒（400/401/403）时给账号打 `dead: true` 落盘，之后状态列为「失效」且不再查积分。其余刷新错误（网络/5xx）单号刷新返回 502，批量刷新跳过该号不标死。
- Go 代理未动。
