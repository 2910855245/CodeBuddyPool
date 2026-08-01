#!/usr/bin/env python3
"""
CodeBuddy 纯 API 登录 + Token 获取
==================================
6 步纯 HTTP API，无需浏览器。

用法:
  python codebuddy_login.py
  python codebuddy_login.py --phone +86xxxxxxxxxxx
  python codebuddy_login.py --phone +86xxxxxxxxxxx --code 123456
"""

import requests, re, json, time, sys, os, argparse
from urllib.parse import urlparse, parse_qs

# 默认手机号，从环境变量 PHONE 读取，未设置时用占位符
CODES = {"PHONE": os.environ.get("PHONE", "+86xxxxxxxxxxx")}

s = requests.Session()
s.headers.update({"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"})

def step1_create_state():
    """Web agents 路径使用 state='0'，不调 plugin API"""
    return "0", "https://www.codebuddy.cn/login/?platform=agents"

def step2_register_state(state):
    """访问 copilot 入口注册 state"""
    s.get(f"https://copilot.tencent.com/login?platform=agents&state={state}", allow_redirects=True, timeout=10)

def step3_send_sms(phone):
    """获取 Keycloak 表单 + 发送短信"""
    s.get("https://www.codebuddy.cn/auth/realms/copilot/protocol/openid-connect/auth", params={
        "client_id": "console", "state": state,
        "redirect_uri": "https://www.codebuddy.cn/login/?platform=agents",
        "response_type": "code", "scope": "openid profile email offline_access",
    }, timeout=15)
    r = s.get("https://www.codebuddy.cn/auth/realms/copilot/sms/authentication-code",
        params={"phoneNumber": phone}, timeout=10)
    return r.json()

def step4_verify_code(phone, code):
    """提交验证码 → 获取 auth code"""
    r = s.get("https://www.codebuddy.cn/auth/realms/copilot/protocol/openid-connect/auth", params={
        "client_id": "console",
        "redirect_uri": "https://www.codebuddy.cn/login/?platform=agents",
        "state": state, "response_type": "code", "scope": "openid profile email offline_access",
    }, timeout=15)
    action = re.search(r'action="([^"]+)"', r.text).group(1).replace("&amp;", "&")
    r2 = s.post(action, data={
        "phoneActivated": "true", "phoneNumber": phone, "code": code,
    }, headers={"Content-Type": "application/x-www-form-urlencoded", "Origin": "https://www.codebuddy.cn"},
        allow_redirects=False, timeout=15)
    if r2.status_code not in (302, 303):
        err = re.findall(r'id="input-error"[^>]*>([^<]{3,200})', r2.text)
        raise Exception(err[0].strip() if err else f"验证失败: {r2.status_code}")
    loc = r2.headers["Location"]
    return parse_qs(urlparse(loc).query)["code"][0]

def step5_silent_auth():
    """Keycloak Silent Auth → 获取 console session"""
    s.get("https://www.codebuddy.cn/console/accounts", allow_redirects=True, timeout=15)

def step6_exchange_token(state):
    """交换 code → 直接返回 accessToken"""
    r = s.post(f"https://www.codebuddy.cn/console/login/enterprise?state={state}",
        headers={"Content-Type": "application/json", "Origin": "https://www.codebuddy.cn",
                 "X-Product-Code": "agents", "X-From-Promotion": "1"}, timeout=10)
    data = r.json()
    if data.get("code") != 0:
        raise Exception(f"交换失败: {data}")
    return data["data"]

def login(phone, code=None):
    """完整登录流程，返回 {accessToken, refreshToken, ...}"""
    global state
    print("=" * 50)
    print("① 创建 state...")
    state, _ = step1_create_state()
    print(f"   state: {state[:20]}...")

    print("② 注册 state...")
    step2_register_state(state)

    print(f"③ 发送短信到 {phone}...")
    step3_send_sms(phone)
    print("   短信已发送")

    if not code:
        code = input("   验证码: ").strip()

    print(f"④ 验证 {code}...")
    step4_verify_code(phone, code)
    print("   验证通过")

    print("⑤ Silent Auth...")
    step5_silent_auth()

    print("⑥ 交换 token...")
    token = step6_exchange_token(state)
    at = token["accessToken"]
    print(f"   accessToken: {at[:50]}...")
    print(f"   refreshToken: {token.get('refreshToken', '')[:50]}...")
    print(f"   expiresIn: {token.get('expiresIn', '?')}s")

    return token

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="CodeBuddy 纯 API 登录")
    parser.add_argument("--phone", default=CODES["PHONE"])
    parser.add_argument("--code", help="验证码")
    parser.add_argument("--output", default="codebuddy_token.json")
    parser.add_argument("--save-auth", action="store_true", help="写入 WorkBuddy auth 文件")
    args = parser.parse_args()

    try:
        token = login(args.phone, args.code)
        json.dump(token, open(args.output, "w"), ensure_ascii=False, indent=2)
        print(f"\nToken 已保存到 {args.output}")

        if args.save_auth:
            import base64
            at = token["accessToken"]
            payload = at.split(".")[1]
            payload += "=" * (4 - len(payload) % 4)
            dec = json.loads(base64.urlsafe_b64decode(payload))
            uid = dec.get("sub", "")
            now = int(time.time() * 1000)
            exp = now + token.get("expiresIn", 3600) * 1000
            auth = {
                "account": {"uid": uid, "nickname": args.phone, "type": "personal", "lastLogin": True},
                "auth": {
                    "accessToken": at,
                    "refreshToken": token.get("refreshToken", ""),
                    "expiresIn": token.get("expiresIn", 3600),
                    "tokenType": "Bearer", "domain": "www.codebuddy.cn",
                    "lastRefreshTime": now, "expiresAt": exp, "refreshExpiresAt": exp,
                },
                "accounts": [{"uid": uid, "nickname": args.phone, "type": "personal", "lastLogin": True}],
            }
            auth_dir = os.path.join(os.environ["USERPROFILE"], "AppData", "Local",
                                     "CodeBuddyExtension", "Data", "Public", "auth")
            os.makedirs(auth_dir, exist_ok=True)
            auth_path = os.path.join(auth_dir, "workbuddy-desktop.info")
            json.dump(auth, open(auth_path, "w"), ensure_ascii=False)
            print(f"Auth 已写入 {auth_path}")
    except Exception as e:
        print(f"\n[错误] {e}")
        sys.exit(1)
