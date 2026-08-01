#!/usr/bin/env python3
"""并发检测号池中所有账号的 Token 有效性和积分余额。

用法:
  python check_pool.py              # 仅检测
  python check_pool.py --refresh    # 检测到过期 token 时自动刷新并写回 accounts.jsonl
"""
import sys, os, json, time, asyncio, base64, argparse
import httpx

ACCOUNTS_FILE = os.environ.get(
    "ACCOUNTS_FILE",
    os.path.join(os.path.dirname(os.path.abspath(__file__)), "go-proxy-cto", "accounts.jsonl"),
)
CONCURRENCY = 10

TOKEN_URL = "https://www.codebuddy.cn/auth/realms/copilot/protocol/openid-connect/token"


def read_accounts():
    if not os.path.exists(ACCOUNTS_FILE):
        return []
    accs = []
    with open(ACCOUNTS_FILE, encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if line:
                try:
                    accs.append(json.loads(line))
                except Exception:
                    pass
    return accs


def write_accounts(accs: list):
    """原子写：先写 tmp 再 os.replace（防 Go 代理读到半截）。"""
    tmp = ACCOUNTS_FILE + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        for a in accs:
            f.write(json.dumps(a, ensure_ascii=False) + "\n")
    os.replace(tmp, ACCOUNTS_FILE)


def decode_jwt_exp(at: str) -> int:
    """解析 JWT 的 exp 字段，返回毫秒时间戳；失败返回 0。"""
    try:
        payload = at.split(".")[1]
        payload += "=" * (4 - len(payload) % 4) if len(payload) % 4 else ""
        dec = json.loads(base64.urlsafe_b64decode(payload))
        return dec.get("exp", 0) * 1000
    except Exception:
        return 0


async def refresh_one(client: httpx.AsyncClient, a: dict) -> bool:
    """用 refresh_token 刷新 access_token，成功返回 True 并就地更新 a。"""
    rt = a.get("refresh_token", "")
    if not rt:
        return False
    try:
        r = await client.post(
            TOKEN_URL,
            content=f"grant_type=refresh_token&refresh_token={rt}&client_id=console",
            headers={"Content-Type": "application/x-www-form-urlencoded"},
        )
        if r.status_code != 200:
            return False
        tok = r.json()
        a["access_token"] = tok.get("access_token", a["access_token"])
        if tok.get("refresh_token"):
            a["refresh_token"] = tok["refresh_token"]
        expires_in = int(tok.get("expires_in", 0))
        if expires_in:
            a["expires_at"] = int(time.time() * 1000) + expires_in * 1000
        return True
    except Exception:
        return False


async def check_one(client: httpx.AsyncClient, a: dict, sem: asyncio.Semaphore,
                    do_refresh: bool = False) -> dict:
    async with sem:
        phone = a.get("phone", "?")
        at = a.get("access_token", "")
        rt = a.get("refresh_token", "")

        if not at:
            return {"phone": phone, "status": "dead", "reason": "no token"}

        # 解析 JWT 看是否过期
        exp_ms = decode_jwt_exp(at)
        expired = time.time() * 1000 > exp_ms if exp_ms else False

        # 调 API 验证 token 是否有效
        try:
            r = await client.post(
                "https://copilot.tencent.com/v2/billing/meter/get-user-resource",
                json={
                    "PageNumber": 1, "PageSize": 100, "ProductCode": "p_tcaca",
                    "Status": [0, 3],
                    "PackageStartTimeRangeBegin": "2024-12-01 21:25:00",
                    "PackageStartTimeRangeEnd": time.strftime("%Y-%m-%d %H:%M:%S"),
                },
                headers={"Authorization": f"Bearer {at}"},
            )

            # 401 / JWT 过期 → 尝试刷新
            if r.status_code == 401 or expired:
                if rt and do_refresh:
                    ok = await refresh_one(client, a)
                    if ok:
                        # 刷新成功，用新 token 重查积分
                        new_at = a["access_token"]
                        r2 = await client.post(
                            "https://copilot.tencent.com/v2/billing/meter/get-user-resource",
                            json={
                                "PageNumber": 1, "PageSize": 100, "ProductCode": "p_tcaca",
                                "Status": [0, 3],
                                "PackageStartTimeRangeBegin": "2024-12-01 21:25:00",
                                "PackageStartTimeRangeEnd": time.strftime("%Y-%m-%d %H:%M:%S"),
                            },
                            headers={"Authorization": f"Bearer {new_at}"},
                        )
                        if r2.status_code == 200:
                            data = r2.json()
                            accounts = data.get("data", {}).get("Response", {}).get("Data", {}).get("Accounts", [])
                            total = sum(float(x.get("CycleCapacityRemainPrecise", 0)) for x in accounts)
                            return {"phone": phone, "status": "ok" if total > 0 else "exhausted",
                                    "credits": round(total, 1),
                                    "jwt_exp": "已刷新", "has_refresh": True,
                                    "refreshed": True, "expires_at": a.get("expires_at", 0)}
                    return {"phone": phone, "status": "dead", "reason": "refresh failed",
                            "jwt_exp": "过期", "has_refresh": bool(rt)}
                elif rt:
                    return {"phone": phone, "status": "expired", "has_refresh": True,
                            "jwt_exp": "过期" if expired else "有效"}
                return {"phone": phone, "status": "dead", "reason": "401 no refresh"}

            if r.status_code != 200:
                return {"phone": phone, "status": "dead", "reason": f"HTTP {r.status_code}"}

            data = r.json()
            accounts = data.get("data", {}).get("Response", {}).get("Data", {}).get("Accounts", [])
            total = sum(float(x.get("CycleCapacityRemainPrecise", 0)) for x in accounts)
            if total <= 0:
                return {"phone": phone, "status": "exhausted", "credits": 0,
                        "jwt_exp": "过期" if expired else "有效", "has_refresh": bool(rt)}

            return {"phone": phone, "status": "ok", "credits": round(total, 1),
                    "jwt_exp": "过期" if expired else "有效", "has_refresh": bool(rt),
                    "expires_at": a.get("expires_at", 0)}

        except httpx.ConnectError:
            return {"phone": phone, "status": "dead", "reason": "connect failed"}
        except Exception as e:
            return {"phone": phone, "status": "dead", "reason": str(e)[:80]}


async def main():
    parser = argparse.ArgumentParser(description="检测号池 Token 和积分")
    parser.add_argument("--refresh", action="store_true",
                        help="检测到过期 token 时自动刷新并写回 accounts.jsonl")
    args = parser.parse_args()

    accs = read_accounts()
    if not accs:
        print("号池为空")
        return

    print(f"检测 {len(accs)} 个账号 (并发={CONCURRENCY}){' [刷新模式]' if args.refresh else ''}...\n")

    async with httpx.AsyncClient(timeout=15) as client:
        sem = asyncio.Semaphore(CONCURRENCY)
        tasks = [check_one(client, a, sem, do_refresh=args.refresh) for a in accs]
        results = await asyncio.gather(*tasks)

    # --refresh 模式下，把刷新过的 token 写回 accounts.jsonl
    if args.refresh:
        refreshed = [r for r in results if r.get("refreshed")]
        if refreshed:
            by_phone = {r["phone"]: r for r in refreshed}
            for a in accs:
                p = a.get("phone")
                if p in by_phone:
                    # access_token / refresh_token / expires_at 已在 check_one 里就地更新
                    pass
            write_accounts(accs)
            print(f"已刷新 {len(refreshed)} 个账号的 token 并写回 accounts.jsonl\n")

    # 统计
    ok = [r for r in results if r["status"] == "ok"]
    exhausted = [r for r in results if r["status"] == "exhausted"]
    expired = [r for r in results if r["status"] == "expired"]
    dead = [r for r in results if r["status"] == "dead"]

    print(f"{'='*60}")
    print(f"  可用: {len(ok)}   耗尽: {len(exhausted)}   过期可刷新: {len(expired)}   死亡: {len(dead)}")
    print(f"  总积分: {sum(r.get('credits', 0) for r in ok):.0f}")
    print(f"{'='*60}")

    if ok:
        print("\n[可用账号]")
        for r in ok:
            tag = " [已刷新]" if r.get("refreshed") else ""
            print(f"  {r['phone']:16s}  {r['credits']:>7.0f} 分  jwt={r['jwt_exp']}  refresh={'有' if r['has_refresh'] else '无'}{tag}")

    if exhausted:
        print(f"\n[积分耗尽]")
        for r in exhausted:
            print(f"  {r['phone']:16s}  jwt={r['jwt_exp']}")

    if expired:
        print(f"\n[过期但可刷新]")
        for r in expired:
            print(f"  {r['phone']:16s}  has refresh")

    if dead:
        print(f"\n[已死]")
        for r in dead:
            print(f"  {r['phone']:16s}  {r.get('reason', 'unknown')}")


if __name__ == "__main__":
    asyncio.run(main())
