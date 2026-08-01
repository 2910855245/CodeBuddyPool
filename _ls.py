import os
import sys

import paramiko

HOST = os.environ.get("SSH_HOST")
USER = os.environ.get("SSH_USER", "root")
PWD = os.environ.get("SSH_PASSWORD")

if not HOST or not PWD:
    sys.exit("[错误] 请先设置环境变量 SSH_HOST 和 SSH_PASSWORD")

ssh = paramiko.SSHClient()
ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
ssh.connect(HOST, username=USER, password=PWD, timeout=20)
for d in ["/opt/codebuddy-pool", "/opt/codebuddy-pool/dashboard"]:
    stdin, stdout, stderr = ssh.exec_command(f"ls -la {d}")
    print(d, "->")
    print(stdout.read().decode())
    print(stderr.read().decode())
ssh.close()
