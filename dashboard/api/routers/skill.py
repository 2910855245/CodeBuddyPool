"""一键部署 Skill 文档路由：动态生成 skill.md，供其他 Agent 调用部署。"""

from __future__ import annotations

import ipaddress
import os
import re
import socket
from urllib.parse import urlparse

from fastapi import APIRouter, Depends

from api.core.security import get_current_admin
from api.schemas import ApiResponse
from api.services.settings_store import load_settings
from config import settings

router = APIRouter(prefix="/api/skill", tags=["部署文档"])


def _mask_key(key: str) -> str:
    """脱敏展示 API Key：sk-xxxx...xxxx"""
    if not key or len(key) < 12:
        return key or ""
    return key[:8] + "..." + key[-4:]


def _resolve_display_host(url: str) -> str:
    """从 URL 提取主机；若主机是 IP 且做了域名绑定（PTR 反解），则返回域名。

    规则：
    - URL 主机本就是域名：原样返回
    - URL 主机是公网 IP 且能反解出 FQDN：返回域名（更友好，便于对外展示）
    - 私网/回环 IP 或反解失败：返回原 IP
    """
    try:
        host = urlparse(url).hostname or ""
    except Exception:
        return url
    if not host:
        return url
    # 已是域名（含字母且不是合法 IP）：直接用
    try:
        ip = ipaddress.ip_address(host)
    except ValueError:
        return host  # 域名，原样返回
    # 是 IP：私网/回环不做反解
    if ip.is_private or ip.is_loopback or ip.is_link_local:
        return host
    # 公网 IP 尝试 PTR 反解出域名
    try:
        fqdn = socket.gethostbyaddr(host)[0].rstrip(".")
        # 反解出的必须是合法域名且非 IP，才采用
        if fqdn and fqdn != host and re.fullmatch(r"[A-Za-z0-9.-]+\.[A-Za-z]{2,}", fqdn):
            return fqdn
    except (socket.herror, socket.gaierror, OSError):
        pass
    return host


def _with_host(url: str, host: str) -> str:
    """把 URL 里的主机替换成 host（保留 scheme 与端口）。"""
    try:
        p = urlparse(url)
        netloc = host if not p.port else f"{host}:{p.port}"
        return p._replace(netloc=netloc).geturl()
    except Exception:
        return url


def _detect_public_ip() -> str:
    """探测本机公网出口 IP。

    优先用 UDP 连接公网地址的方式取本机出口 IP（不产生实际流量、无需出网 HTTP），
    失败再回退到公网 IP 查询服务。取不到返回空串。
    """
    # 方式一：UDP 连接公网地址，取本机出口 IP（最可靠，无需 HTTP 出网）
    for target in (("8.8.8.8", 80), ("114.114.114.114", 80), ("223.5.5.5", 80)):
        try:
            with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as s:
                s.settimeout(3)
                s.connect(target)
                ip = s.getsockname()[0]
            addr = ipaddress.ip_address(ip)
            if not (addr.is_private or addr.is_loopback or addr.is_link_local):
                return ip
        except Exception:
            continue
    # 方式二：回退到公网 IP 查询服务
    for url in ("https://api.ipify.org", "https://ifconfig.me/ip", "https://ip.sb/ip"):
        try:
            import urllib.request

            with urllib.request.urlopen(url, timeout=4) as resp:
                ip = resp.read().decode("utf-8", "ignore").strip()
            addr = ipaddress.ip_address(ip)
            if not (addr.is_private or addr.is_loopback or addr.is_link_local):
                return ip
        except Exception:
            continue
    return ""


def _resolve_public_display_host(url: str) -> str:
    """生成对外展示的主机：优先域名，否则公网 IP。

    规则：
    - proxy_base 主机已是域名：原样返回
    - proxy_base 主机是公网 IP：走 PTR 反解出域名则用域名，否则用原 IP
    - proxy_base 主机是回环/私网/空：探测本机公网 IP，再对其反解域名，取不到则直接给公网 IP
    """
    host = _resolve_display_host(url)
    try:
        ip = ipaddress.ip_address(host)
        is_public_ip = not (ip.is_private or ip.is_loopback or ip.is_link_local)
        is_ip = True
    except ValueError:
        is_ip = False
        is_public_ip = False
    # 已是域名，或本来就是公网 IP：直接用
    if not is_ip or is_public_ip:
        return host
    # 回环/私网/空：探测公网 IP，再反解域名
    public_ip = _detect_public_ip()
    if not public_ip:
        return host
    return _resolve_display_host(f"http://{public_ip}")


def _resolve_dashboard_base(proxy_base: str, display_host: str) -> str:
    """推导对外展示的面板地址（scheme + display_host + 面板端口）。

    面板端口优先级：DASHBOARD_PORT 环境变量 > settings.port > 9091->9100 映射兜底。
    这样任意 proxy/dashboard 端口组合都能对得上，不再写死 9100。
    """
    dash_port = os.environ.get("DASHBOARD_PORT") or str(settings.port or "")
    if not dash_port:
        dash_port = "9200" if urlparse(proxy_base).port == 9191 else "9100"
    scheme = urlparse(proxy_base).scheme or "http"
    return f"{scheme}://{display_host}:{dash_port}"


@router.get("/deploy-doc", response_model=ApiResponse)
async def deploy_doc(admin: dict = Depends(get_current_admin)):
    """生成一键部署 skill.md 文档。"""
    s = load_settings()
    proxy_base = s.get("proxy_base", "http://127.0.0.1:9091")
    proxy_key = s.get("proxy_key", "")

    # 从 proxy_base 推导外部地址：域名优先，回环/私网则探测公网 IP 再反解域名
    display_host = _resolve_public_display_host(proxy_base)
    proxy_base = _with_host(proxy_base, display_host)
    dashboard_base = _resolve_dashboard_base(proxy_base, display_host)

    # 若最终仍是回环地址（公网 IP 也探测失败），提示用户替换
    external_hint = ""
    if "127.0.0.1" in proxy_base or "localhost" in proxy_base:
        external_hint = "\n> 注意：未能探测到公网 IP/域名，请手动替换为服务器实际 IP/域名。\n"

    doc = f"""# CodeBuddy Pool 一键部署 Skill

> 本文档由 CodeBuddy Pool Dashboard 自动生成，供其他 Agent 直接调用完成 API 对接。
> 仅包含调用方所需信息（地址/协议/管理接口），不含内部部署与源码细节。
> 生成时间：{__import__('time').strftime('%Y-%m-%d %H:%M:%S')}
{external_hint}
---

## 一、服务器信息

| 服务 | 地址 | 说明 |
|------|------|------|
| Go Proxy (OpenAI/Anthropic) | `{proxy_base}` | API 入口 |
| Dashboard 管理面板 | `{dashboard_base}` | 号池/Key/监控管理 |
| 分享注册页面 | `{dashboard_base}/#/register` | 好友注册入池 |

---

## 二、API 协议

### 2.1 OpenAI 兼容格式

```bash
curl -X POST "{proxy_base}/v1/chat/completions" \\
  -H "Authorization: Bearer {{API_KEY}}" \\
  -H "Content-Type: application/json" \\
  -d '{{
    "model": "kimi-k3",
    "messages": [{{"role": "user", "content": "你好"}}],
    "stream": true
  }}'
```

### 2.2 Anthropic 兼容格式

端点：`POST /v1/messages`

必需请求头（二选一鉴权）：

| 头 | 值 | 说明 |
|----|----|------|
| `anthropic-version` | `2023-06-01` | 必填，Anthropic 协议版本 |
| `x-api-key` | `{{API_KEY}}` | 鉴权方式一（Anthropic 官方风格） |
| `Authorization` | `Bearer {{API_KEY}}` | 鉴权方式二（OpenAI 风格，任选其一） |

```bash
curl -X POST "{proxy_base}/v1/messages" \\
  -H "x-api-key: {{API_KEY}}" \\
  -H "anthropic-version: 2023-06-01" \\
  -H "Content-Type: application/json" \\
  -d '{{
    "model": "kimi-k3",
    "max_tokens": 1024,
    "messages": [{{"role": "user", "content": "你好"}}]
  }}'
```

也支持 `Authorization: Bearer {{API_KEY}}` 替代 `x-api-key` 头：

```bash
curl -X POST "{proxy_base}/v1/messages" \\
  -H "Authorization: Bearer {{API_KEY}}" \\
  -H "anthropic-version: 2023-06-01" \\
  -H "Content-Type: application/json" \\
  -d '{{
    "model": "kimi-k3",
    "max_tokens": 1024,
    "messages": [{{"role": "user", "content": "你好"}}]
  }}'
```

流式（SSE）请求加 `"stream": true`，返回标准 Anthropic 事件序列
（`message_start` / `content_block_start` / `content_block_delta` / `content_block_stop` / `message_delta` / `message_stop`）。

### 2.3 Codex (OpenAI Responses API)

Codex CLI 使用 OpenAI Responses 协议，请求体为 `instructions` + `input[]`，
流式响应为 `response.*` 事件序列。代理内部转换为 CodeBuddy 上游格式。

```bash
curl -X POST "{proxy_base}/v1/responses" \\
  -H "Authorization: Bearer {{API_KEY}}" \\
  -H "Content-Type: application/json" \\
  -d '{{
    "model": "kimi-k3",
    "instructions": "You are a helpful coding assistant.",
    "input": [
      {{
        "type": "message",
        "role": "user",
        "content": [{{"type": "input_text", "text": "你好"}}]
      }}
    ],
    "stream": true
  }}'
```

Codex CLI 对接配置（`~/.codex/config.toml`）：

```toml
model_provider = "codebuddy"
model = "kimi-k3"

[model_providers.codebuddy]
name = "CodeBuddy Pool"
base_url = "{proxy_base}/v1"
wire_api = "responses"
env_key = "CODEBUDDY_API_KEY"
```

然后设置环境变量 `CODEBUDDY_API_KEY` 为面板签发的 API Key。

### 2.4 模型列表

```bash
curl "{proxy_base}/v1/models" \\
  -H "Authorization: Bearer {{API_KEY}}"
```

**可用模型**：`kimi-k3`、`kimi-k3-1`、`kimi-k2.7`、`kimi-k2.6`、`kimi-k2-thinking`、`deepseek-v4-pro`、`deepseek-v4-flash`、`deepseek-v3`、`hunyuan-2.0`、`glm-5.2`、`glm-5.1`、`glm-5.0`、`glm-5v-turbo`、`minimax-m3`、`minimax-m2.7`、`hy3`

---

## 三、故障排查

| 症状 | 可能原因 | 处理 |
|------|----------|------|
| `/v1/models` 外部不通 | 代理未对外监听 | 确认代理监听在 `0.0.0.0:9091` |
| `provider.connection_error` | 代理挂了或重启中 | `curl {proxy_base}/stats` 检查 |
| 返回 `rate_limited` | 触发限速 | 降低并发或联系管理员调额度 |
| 号全 unavailable | 号池暂时不可用 | 稍后重试，或联系管理员补号 |

---

## 四、Agent 调用示例（Python）

```python
import httpx

PROXY = "{proxy_base}"
API_KEY = "sk-xxxxxxxx"

async def chat(prompt: str) -> str:
    async with httpx.AsyncClient(timeout=60) as c:
        r = await c.post(
            f"{{PROXY}}/v1/chat/completions",
            headers={{"Authorization": f"Bearer {{API_KEY}}"}},
            json={{
                "model": "kimi-k3",
                "messages": [{{"role": "user", "content": prompt}}],
                "stream": False,
            }},
        )
        return r.json()["choices"][0]["message"]["content"]
```

---

*本文档由 CodeBuddy Pool Dashboard 自动生成，版本：2026-08-01*
"""

    return ApiResponse.ok({"markdown": doc, "proxy_base": proxy_base})
