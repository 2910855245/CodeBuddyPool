# CodeBuddy Pool — AI API 号池中转站

## 一句话说清楚

**注册 CodeBuddy 号 → 拿 2000+ 积分 → Go 代理封装成 OpenAI / Anthropic 兼容 API 对外提供服务。**
可对接任意 OpenAI / Anthropic 兼容客户端，低成本跑 Kimi、DeepSeek、GLM 等模型。

## 架构设计

```
     ┌───────────────────────────────────────────┐
     │  accounts.jsonl (共享数据)                 │
     │             /              \               │
     │  Python (养号)              Go (卖号) :9091 │
     │  ─────────────              ─────────────  │
     │  dashboard :9100           go-proxy-cto    │
     │    - 分享注册页             - 冷热分档加权调度 │
     │    - 每日签到               - Token 自动刷新 │
     │    - API 密钥管理           - OpenAI/Anthropic│
     │    - Web 管理面板           - 多 Key 鉴权限流 │
     └───────────────────────────────────────────┘
```

两层分工（**两个进程独立运行，互不依赖**）：

- **Go proxy（渠道层）**：高性能请求代理、冷热分档加权随机调度（冷号优先 + 失败换号退避）、Token 自动刷新、每日签到、多 Key 管理 + 限流 + 用量追踪
- **Python（养号层）**：分享注册（手机号+验证码）、账号生命周期管理、Web 管理面板

共享 `accounts.jsonl`，Go 每 30 秒热加载，Python 写入后自动生效。

## 项目结构

```
codebuddy-pool/
├── start.bat / stop.bat        Windows 一键启停
├── pool_settings.json          面板设置持久化（含 proxy_key，请自行修改）
├── _server_ops.py              服务器运维 CLI (status/deploy/rollback/backup...)
├── check_pool.py               号池健康检测脚本
├── codebuddy_login.py          纯 API 登录模块 (6 步 HTTP, 无需浏览器)
├── go-proxy-cto/               Go API 代理
│   ├── main.go                 启动入口 + 路由 + 鉴权中间件
│   ├── pool.go                 号池调度 (accounts.jsonl 热加载/加权分配/持久化/冷却)
│   ├── engine.go               CodeBuddy API 请求代理 + Token 自动刷新
│   ├── handler_openai.go       OpenAI (/v1/chat/completions) + Anthropic (/v1/messages)
│   ├── account.go              账号状态 (token/积分/过期/耗尽/失效)
│   ├── keymgr.go               多 API Key 管理 (增删/启停/脱敏存储)
│   ├── ratelimit.go            Key 级限流
│   ├── usagetracker.go         按 Key 用量追踪
│   ├── ringlog.go              内存环形日志
│   ├── config.go               模型映射 + 常量
│   ├── accounts.jsonl          号池数据（空模板，自行注册填充）
│   └── api_keys.jsonl          API 密钥（空模板，自行创建）
├── dashboard/                  可视化管理面板
│   ├── server.py               启动入口（granian）
│   ├── config.py               pydantic-settings 读 .env
│   ├── .env.example            配置模板（JWT/数据库/代理 Key）
│   ├── api/                    后端分层：routers / services / db / core
│   ├── vue-ui/                 Vue3+TS+Vite 前端源码
│   └── dist/                   前端 build 产物（后端 SPA 托管，开箱即用）
└── 部署指南.md                 从零部署到自己服务器的完整步骤
```

## 已实现功能

### Go 代理 (go-proxy-cto)
- OpenAI 兼容 `/v1/chat/completions` + `/v1/models`
- Anthropic 兼容 `/v1/messages`（含完整流式事件序列）
- 真流式 SSE 转发（边读边发，首 token 即时到达；非流式聚合成标准 OpenAI 响应）
- 冷热分档账号调度：冷号(闲置>5min)优先 + 成功数加权随机，失败自动换号指数退避重试
- Token 过期自动刷新（refresh_token 续期，续期被拒才判死）
- 内置每日签到定时器（`ENABLE_CHECKIN=false` 可关）
- 配额耗尽自动标记（12h 冷却），429/连续错误冷却自动恢复
- dead 号 keepAlive 定期探测，成功自动复活
- accounts.jsonl 热加载（30s 间隔，删号同步剔除）
- 多 API Key 管理（`/api/admin/keys`）：增删/启停/按 Key 限流/用量追踪
- `/stats` 号池状态端点（含总积分/低可用告警）

### Python 管理面板 (dashboard, :9100)
- **号池**：统计卡 + 账号卡片（积分进度条/Token 剩余天数/状态徽章）
- **号池**：单号签到 / 刷新积分 / 删除，防连点（后端 15s 冷却 + 批量互斥）
- **代理**：Go 内存视角（成功/失败计数/低可用告警）/ 日志查看 / 手动签到与热加载
- **API 密钥**：面板直连 Go keymgr：创建/启停/删除/用量查询，明文 Key 仅创建时返回一次
- **分享注册页**（公开，无鉴权）：手机号 + 短信验证码 + 邀请人(可选) 三步领取账号，注册成功自动签到入池
- **部署文档**：一键生成 skill.md（服务器地址/API 协议/管理接口/部署命令），支持复制/下载

### 账号注册（分享注册流程）
- 用户自带手机号 + 官方短信验证码（无需接码平台）
- 纯 API 登录（6 步 HTTP，不依赖浏览器）
- 自动签到领取 100 积分
- 邀请人字段（可选，记录来源）
- 写入 accounts.jsonl + Go `/api/register/inject` 即时入池

## 纯 API 登录原理（核心逆向成果）

CodeBuddy 仅支持手机号 + 短信验证码登录，但整个流程完全基于 HTTP API：

```
1. POST /v2/plugin/auth/state?platform=workbuddy    创建 state -> 获取 authUrl
2. GET  copilot.tencent.com/login?state=xxx          注册 state (必须)
3. GET  /auth/realms/copilot/sms/authentication-code 发送短信验证码
4. POST /auth/realms/copilot/login-actions/authenticate 提交验证码 -> code
5. GET  /console/accounts                            Keycloak Silent Auth -> session
6. POST /console/login/enterprise?state=xxx          交换 token -> 直接返回
```

**关键发现**：code 交换不在 `copilot.tencent.com` 上，在 `www.codebuddy.cn/console/login/enterprise`。

## 快速开始

> 完整的服务器部署步骤见 **[部署指南.md](部署指南.md)**。下面是最小本地跑通流程。

### 0. 环境准备

- **Go 1.22+**：编译 Go proxy（仅首次）
- **Python 3.10+**：管理面板和注册脚本

### 1. 配置

```bash
# 面板配置（必填：JWT 密钥 + 数据库 + Go 代理 Key）
cp dashboard/.env.example dashboard/.env
# 编辑 dashboard/.env，至少填 JWT_SECRET_KEY / MYSQL_URL / PROXY_KEY

# 面板设置里的 Go 代理 Key（与 dashboard/.env 的 PROXY_KEY 保持一致）
# 编辑 pool_settings.json，把 proxy_key 改成你自己的随机串
```

### 2. 编译并启动 Go 代理

```bash
cd go-proxy-cto
go build -o cto-proxy .
PROXY_API_KEY=你的随机Key ./cto-proxy        # Linux
# Windows: set PROXY_API_KEY=你的随机Key && cto-proxy.exe
# 代理监听 http://127.0.0.1:9091
```

### 3. 启动管理面板

```bash
pip install -r dashboard/requirements.txt
python dashboard/server.py
# 浏览器打开 http://localhost:9100 ，默认管理员 admin / admin（请立即改密）
```

### 4. 注册账号入池

- 把分享注册页链接 `http://<host>:9100/#/register` 发给朋友，对方填手机号 + 验证码注册，自动入池并完成首次签到

### 5. 签发 API Key 并调用

面板「API 密钥」页创建 Key（可备注发给谁），明文仅显示一次：

```bash
curl http://localhost:9091/v1/chat/completions \
  -H "Authorization: Bearer <签发的Key>" \
  -H "Content-Type: application/json" \
  -d '{"model":"kimi-k3","messages":[{"role":"user","content":"hello"}]}'
```

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `ACCOUNTS_FILE` | `go-proxy-cto/accounts.jsonl` | 号池文件路径 (Go + Python 共享) |
| `PROXY_API_KEY` | -- (必填) | Go 代理主鉴权 Key，启动时强制校验 |
| `PROXY_ADMIN_KEY` | 同 `PROXY_API_KEY` | Go admin API (`/api/admin/*`) 鉴权 Key |
| `LISTEN_ADDR` | `127.0.0.1:9091` | Go 代理监听地址（对外服务用 `0.0.0.0:9091`） |
| `ENABLE_CHECKIN` | `true` | Go 内置每日签到开关 |
| `DASHBOARD_PORT` | `9100` | 管理面板端口 |

## 技术栈

| 层 | 技术 |
|----|------|
| API 代理 | Go + Gin + Zap + Ring Buffer Log |
| 管理后端 | Python + FastAPI + Granian + httpx |
| 管理前端 | Vue3 + Vite + TypeScript (SPA 构建产物托管) |
| 数据接口 | accounts.jsonl (JSON Lines) + MySQL/SQLite (Dashboard) |

## 已知问题与风险

1. **模型映射**：CodeBuddy 侧部分模型 ID 需映射（如 `kimi-k3` → `kimi-k3-1`），`config.go` 的 ModelMap 已处理
2. **上游审核过滤**：Claude Code 发出的计费头与身份声明会被上游审核误判。`handler_openai.go` 会在转发前静默丢弃含 `billing-header` 的 system 段，并通过 `sanitizeIdentity()` 替换身份声明
3. **账单接口风控**：部分 IP 的 `get-user-resource` 返回 0 积分，但 chat 与签到正常；新号默认 2000 积分
4. **不要双开代理**：多处同时活跃同批账号会导致 token 互顶（Keycloak refresh 轮换机制）
5. **Token 有效期约 365 天**，到期需重新注册
6. **同一 IP 大量注册可能触发风控**
7. 本项目仅供学习研究，请勿用于商业用途
