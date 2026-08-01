#!/usr/bin/env python3
"""
_server_ops.py — CodeBuddy Pool 服务器运维 CLI

用法:
    python _server_ops.py status      查看服务器+号池状态
    python _server_ops.py deploy      部署新二进制 (codebuddy-pool-linux-new)
    python _server_ops.py rollback    回滚到上一个版本
    python _server_ops.py restart     重启 Go Proxy
    python _server_ops.py logs [N]    查看最近 N 行日志 (默认 50)
    python _server_ops.py pool        查看号池详情
    python _server_ops.py sync        同步本地 accounts.jsonl 到服务器
    python _server_ops.py backup      备份服务器 accounts.jsonl
    python _server_ops.py clean       清理服务器旧备份文件
    python _server_ops.py ssh <cmd>   在服务器上执行任意命令
    python _server_ops.py upload <local> <remote>   上传文件
    python _server_ops.py download <remote> <local> 下载文件
"""

import sys
import os
import time
import json

import paramiko

# ── 配置 ──────────────────────────────────────────────
# 所有敏感凭据从环境变量读取，绝不硬编码。
# SSH_HOST 必填：指向你自己的服务器 IP（本工具不内置任何默认地址）。
SSH_HOST = os.environ.get("SSH_HOST")  # 必须设置，无默认值
SSH_USER = os.environ.get("SSH_USER", "root")
SSH_PASS = os.environ.get("SSH_PASSWORD")  # 必须设置，无默认值
REMOTE_BASE = os.environ.get("REMOTE_BASE", "/opt/codebuddy-pool")
REMOTE_GO = f"{REMOTE_BASE}/go-proxy-cto"
LOCAL_BASE = os.path.dirname(os.path.abspath(__file__))
BINARY_NAME = "codebuddy-pool-linux"
NEW_BINARY = "codebuddy-pool-linux-new"
API_KEY = os.environ.get("PROXY_API_KEY")       # 聊天 key，必须设置
ADMIN_KEY = os.environ.get("PROXY_ADMIN_KEY")   # /api/admin/* 管理 key，必须设置
PROXY_PORT = 9091


def _require_env(var, name):
    """敏感变量必须从环境变量读取，未设置则拒绝执行。"""
    if not var:
        sys.exit(
            f"[错误] 未设环境变量 {name}，拒绝执行。\n"
            f"       请先设置: $env:{name}='你的值' (PowerShell) "
            f"或 export {name}=xxx (Bash)"
        )
    return var


def get_ssh():
    _require_env(SSH_HOST, "SSH_HOST")
    _require_env(SSH_PASS, "SSH_PASSWORD")
    ssh = paramiko.SSHClient()
    ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    ssh.connect(SSH_HOST, username=SSH_USER, password=SSH_PASS, timeout=15)
    return ssh


def run(ssh, cmd, timeout=30):
    """执行远程命令，返回 (stdout, stderr, exit_code)"""
    stdin, stdout, stderr = ssh.exec_command(cmd, timeout=timeout)
    out = stdout.read().decode(errors="replace").strip()
    err = stderr.read().decode(errors="replace").strip()
    code = stdout.channel.recv_exit_status()
    return out, err, code


def run_print(ssh, cmd, timeout=30):
    """执行远程命令并打印结果"""
    out, err, code = run(ssh, cmd, timeout)
    if out:
        print(out)
    if err:
        print(f"[stderr] {err}", file=sys.stderr)
    return code


# ── 命令实现 ──────────────────────────────────────────

def cmd_status():
    """查看服务器整体状态"""
    ssh = get_ssh()
    print("=== 系统 ===")
    run_print(ssh, "uname -a && uptime")
    print("\n=== CPU/内存 ===")
    run_print(ssh, "top -bn1 | head -5 && echo && free -h")
    print("\n=== 磁盘 ===")
    run_print(ssh, "df -h /")
    print("\n=== Go Proxy ===")
    run_print(ssh, f"ps aux | grep {BINARY_NAME} | grep -v grep")
    print("\n=== Dashboard ===")
    run_print(ssh, "ps aux | grep -E 'server.py|granian' | grep -v grep")
    print("\n=== 端口 ===")
    run_print(ssh, "ss -tlnp | grep -E '9091|9100'")
    print("\n=== 号池 ===")
    run_print(ssh, f"curl -s http://127.0.0.1:{PROXY_PORT}/stats")
    print("\n=== 最近日志(5行) ===")
    run_print(ssh, f"tail -5 {REMOTE_GO}/log")
    ssh.close()


def cmd_deploy():
    """部署新二进制"""
    local_new = os.path.join(LOCAL_BASE, "go-proxy-cto", NEW_BINARY)
    if not os.path.exists(local_new):
        print(f"错误: 本地文件不存在 {local_new}")
        print("先编译: cd go-proxy-cto && GOOS=linux GOARCH=amd64 go build -o codebuddy-pool-linux-new .")
        sys.exit(1)

    ssh = get_ssh()

    # 1. 上传
    print("=== 上传二进制 ===")
    sftp = ssh.open_sftp()
    remote_path = f"{REMOTE_GO}/{NEW_BINARY}"
    sftp.put(local_new, remote_path)
    sftp.chmod(remote_path, 0o755)
    sftp.close()
    size = os.path.getsize(local_new) / 1024 / 1024
    print(f"上传完成 ({size:.1f}MB)")

    # 2. 部署脚本
    deploy_script = f"""
set -e
cd {REMOTE_GO}

echo "=== 备份当前版本 ==="
if [ -f {BINARY_NAME} ]; then
    cp {BINARY_NAME} {BINARY_NAME}.prev
    echo "已备份为 .prev"
fi

echo "=== 停旧进程 ==="
PID=$(ps aux | grep './{BINARY_NAME}' | grep -v grep | awk '{{print $2}}')
if [ -n "$PID" ]; then
    kill $PID
    sleep 2
    # force kill if still alive
    if ps -p $PID > /dev/null 2>&1; then
        kill -9 $PID
        sleep 1
    fi
    echo "已停止 PID=$PID"
else
    echo "(无运行中进程)"
fi

echo "=== 替换二进制 ==="
mv {NEW_BINARY} {BINARY_NAME}
chmod +x {BINARY_NAME}
echo "已替换"

echo "=== 启动 ==="
LISTEN_ADDR=0.0.0.0:{PROXY_PORT} setsid ./{BINARY_NAME} < /dev/null > log 2>&1 &
sleep 3

echo "=== 健康检查 ==="
PID=$(ps aux | grep './{BINARY_NAME}' | grep -v grep | awk '{{print $2}}')
if [ -z "$PID" ]; then
    echo "启动失败! 回滚..."
    if [ -f {BINARY_NAME}.prev ]; then
        mv {BINARY_NAME}.prev {BINARY_NAME}
        LISTEN_ADDR=0.0.0.0:{PROXY_PORT} setsid ./{BINARY_NAME} < /dev/null > log 2>&1 &
        echo "已回滚"
    fi
    exit 1
fi
echo "新进程 PID=$PID"

curl -s -o /dev/null -w "HTTP %{{http_code}}" http://127.0.0.1:{PROXY_PORT}/v1/models
echo

echo "=== 号池状态 ==="
curl -s http://127.0.0.1:{PROXY_PORT}/stats
echo
echo "=== 部署完成 ==="
"""
    run_print(ssh, deploy_script, timeout=60)
    ssh.close()


def cmd_rollback():
    """回滚到上一个版本"""
    ssh = get_ssh()

    # 列出可用备份
    print("=== 可用备份 ===")
    run_print(ssh, f"ls -lt {REMOTE_GO}/{BINARY_NAME}* | grep -v '\\.jsonl'")

    rollback_script = f"""
cd {REMOTE_GO}

echo "=== 停当前进程 ==="
PID=$(ps aux | grep './{BINARY_NAME}' | grep -v grep | awk '{{print $2}}')
if [ -n "$PID" ]; then
    kill $PID
    sleep 2
    echo "已停止 PID=$PID"
fi

echo "=== 回滚到 .prev ==="
if [ -f {BINARY_NAME}.prev ]; then
    cp {BINARY_NAME} {BINARY_NAME}.broken
    cp {BINARY_NAME}.prev {BINARY_NAME}
    chmod +x {BINARY_NAME}
    echo "已回滚"
else
    echo "错误: 没有 .prev 备份"
    exit 1
fi

echo "=== 启动 ==="
LISTEN_ADDR=0.0.0.0:{PROXY_PORT} setsid ./{BINARY_NAME} < /dev/null > log 2>&1 &
sleep 3

PID=$(ps aux | grep './{BINARY_NAME}' | grep -v grep | awk '{{print $2}}')
echo "新进程 PID=$PID"
curl -s http://127.0.0.1:{PROXY_PORT}/stats
echo
echo "=== 回滚完成 ==="
"""
    run_print(ssh, rollback_script, timeout=30)
    ssh.close()


def cmd_restart():
    """重启 Go Proxy"""
    ssh = get_ssh()
    restart_script = f"""
cd {REMOTE_GO}

echo "=== 停止 ==="
PID=$(ps aux | grep './{BINARY_NAME}' | grep -v grep | awk '{{print $2}}')
if [ -n "$PID" ]; then
    kill $PID
    sleep 2
    echo "已停止 PID=$PID"
else
    echo "(无运行中进程)"
fi

echo "=== 启动 ==="
LISTEN_ADDR=0.0.0.0:{PROXY_PORT} setsid ./{BINARY_NAME} < /dev/null > log 2>&1 &
sleep 3

PID=$(ps aux | grep './{BINARY_NAME}' | grep -v grep | awk '{{print $2}}')
echo "新进程 PID=$PID"
curl -s http://127.0.0.1:{PROXY_PORT}/stats
"""
    run_print(ssh, restart_script, timeout=30)
    ssh.close()


def cmd_logs(n=50):
    """查看日志"""
    ssh = get_ssh()
    print(f"=== 最近 {n} 行 log ===")
    run_print(ssh, f"tail -{n} {REMOTE_GO}/log")
    ssh.close()


def cmd_pool():
    """查看号池详情"""
    ssh = get_ssh()
    print("=== 号池统计 ===")
    run_print(ssh, f"curl -s http://127.0.0.1:{PROXY_PORT}/stats | python3 -m json.tool 2>/dev/null || curl -s http://127.0.0.1:{PROXY_PORT}/stats")
    print("\n=== 账号列表(前20) ===")
    run_print(ssh, f"head -20 {REMOTE_GO}/accounts.jsonl | python3 -c \"import sys,json; [print(json.loads(l).get('phone','?'), json.loads(l).get('status','?'), json.loads(l).get('credits','?')) for l in sys.stdin]\" 2>/dev/null || head -20 {REMOTE_GO}/accounts.jsonl")
    ssh.close()


def cmd_sync():
    """同步本地 accounts.jsonl 到服务器"""
    local_file = os.path.join(LOCAL_BASE, "go-proxy-cto", "accounts.jsonl")
    if not os.path.exists(local_file):
        print(f"错误: 本地文件不存在 {local_file}")
        sys.exit(1)

    ssh = get_ssh()

    # 先备份服务器上的
    print("=== 备份服务器号池 ===")
    ts = time.strftime("%m%d_%H%M")
    run_print(ssh, f"cp {REMOTE_GO}/accounts.jsonl {REMOTE_GO}/accounts.jsonl.bak.{ts}")

    # 上传
    print("=== 上传本地号池 ===")
    sftp = ssh.open_sftp()
    sftp.put(local_file, f"{REMOTE_GO}/accounts.jsonl")
    sftp.close()

    # 热加载
    print("=== 热加载 ===")
    run_print(ssh, f"curl -s -X POST http://127.0.0.1:{PROXY_PORT}/api/admin/reload -H 'Authorization: Bearer {ADMIN_KEY}'")

    # 验证
    print("\n=== 验证 ===")
    run_print(ssh, f"curl -s http://127.0.0.1:{PROXY_PORT}/stats")
    ssh.close()


def cmd_backup():
    """备份服务器 accounts.jsonl 到本地"""
    ssh = get_ssh()
    sftp = ssh.open_sftp()
    ts = time.strftime("%Y%m%d_%H%M%S")
    local_backup = os.path.join(LOCAL_BASE, f"accounts_backup_{ts}.jsonl")
    sftp.get(f"{REMOTE_GO}/accounts.jsonl", local_backup)
    sftp.close()
    print(f"已备份到 {local_backup}")
    ssh.close()


def cmd_clean():
    """清理服务器旧备份"""
    ssh = get_ssh()
    clean_script = f"""
cd {REMOTE_GO}

echo "=== 清理前 ==="
du -sh .
echo "文件数: $(ls -1 | wc -l)"

echo "=== 删除旧二进制备份 (保留 .prev 和当前) ==="
ls -t {BINARY_NAME}.bak.* {BINARY_NAME}.prev2 {BINARY_NAME}.prev3 {BINARY_NAME}.prev4 {BINARY_NAME}.prev-rollback {BINARY_NAME}.old-backup {BINARY_NAME}.broken {BINARY_NAME}.new-p0 2>/dev/null | xargs rm -f
echo "已清理"

echo "=== 删除旧号池备份 (保留最近3个) ==="
ls -t accounts.jsonl.bak.* 2>/dev/null | tail -n +4 | xargs rm -f
echo "已清理"

echo "=== 清理后 ==="
du -sh .
echo "文件数: $(ls -1 | wc -l)"
echo "剩余备份:"
ls -lt {BINARY_NAME}* accounts.jsonl* 2>/dev/null
"""
    run_print(ssh, clean_script, timeout=30)
    ssh.close()


def cmd_ssh(cmd):
    """执行任意命令"""
    ssh = get_ssh()
    run_print(ssh, cmd, timeout=60)
    ssh.close()


def cmd_upload(local, remote):
    """上传文件"""
    if not os.path.exists(local):
        print(f"错误: 本地文件不存在 {local}")
        sys.exit(1)
    ssh = get_ssh()
    sftp = ssh.open_sftp()
    sftp.put(local, remote)
    sftp.close()
    size = os.path.getsize(local) / 1024 / 1024
    print(f"已上传 {local} -> {remote} ({size:.1f}MB)")
    ssh.close()


def cmd_download(remote, local):
    """下载文件"""
    ssh = get_ssh()
    sftp = ssh.open_sftp()
    sftp.get(remote, local)
    sftp.close()
    size = os.path.getsize(local) / 1024 / 1024
    print(f"已下载 {remote} -> {local} ({size:.1f}MB)")
    ssh.close()


# ── 入口 ──────────────────────────────────────────────

COMMANDS = {
    "status":   (cmd_status,   "查看服务器+号池状态"),
    "deploy":   (cmd_deploy,   "部署新二进制"),
    "rollback": (cmd_rollback, "回滚到上一个版本"),
    "restart":  (cmd_restart,  "重启 Go Proxy"),
    "logs":     (cmd_logs,     "查看日志 [N行, 默认50]"),
    "pool":     (cmd_pool,     "查看号池详情"),
    "sync":     (cmd_sync,     "同步本地号池到服务器"),
    "backup":   (cmd_backup,   "备份服务器号池到本地"),
    "clean":    (cmd_clean,    "清理服务器旧备份"),
    "ssh":      (cmd_ssh,      "执行任意命令"),
    "upload":   (cmd_upload,   "上传文件"),
    "download": (cmd_download, "下载文件"),
}


def main():
    if len(sys.argv) < 2 or sys.argv[1] in ("-h", "--help", "help"):
        print(__doc__)
        print("命令列表:")
        for name, (fn, desc) in COMMANDS.items():
            print(f"  {name:12s} {desc}")
        sys.exit(0)

    cmd = sys.argv[1]
    if cmd not in COMMANDS:
        print(f"未知命令: {cmd}")
        print(f"可用命令: {', '.join(COMMANDS.keys())}")
        sys.exit(1)

    fn = COMMANDS[cmd][0]

    if cmd == "logs":
        n = int(sys.argv[2]) if len(sys.argv) > 2 else 50
        fn(n)
    elif cmd == "ssh":
        if len(sys.argv) < 3:
            print("用法: python _server_ops.py ssh <command>")
            sys.exit(1)
        fn(" ".join(sys.argv[2:]))
    elif cmd == "upload":
        if len(sys.argv) < 4:
            print("用法: python _server_ops.py upload <local> <remote>")
            sys.exit(1)
        fn(sys.argv[2], sys.argv[3])
    elif cmd == "download":
        if len(sys.argv) < 4:
            print("用法: python _server_ops.py download <remote> <local>")
            sys.exit(1)
        fn(sys.argv[2], sys.argv[3])
    else:
        fn()


if __name__ == "__main__":
    main()
