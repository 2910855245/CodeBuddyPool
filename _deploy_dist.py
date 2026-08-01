import os
import sys

import paramiko

HOST = os.environ.get("SSH_HOST")
USER = os.environ.get("SSH_USER", "root")
PWD = os.environ.get("SSH_PASSWORD")
LOCAL_DIST = os.path.join(os.path.dirname(os.path.abspath(__file__)), "dashboard", "dist")
REMOTE_DIST = os.environ.get("REMOTE_DIST", "/opt/codebuddy-pool/dashboard/dist")

if not HOST or not PWD:
    sys.exit("[错误] 请先设置环境变量 SSH_HOST 和 SSH_PASSWORD")

ssh = paramiko.SSHClient()
ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
ssh.connect(HOST, username=USER, password=PWD, timeout=20)


def run(cmd):
    stdin, stdout, stderr = ssh.exec_command(cmd)
    out = stdout.read().decode()
    err = stderr.read().decode()
    return out, err


# backup
run(f"rm -rf {REMOTE_DIST}.bak && cp -r {REMOTE_DIST} {REMOTE_DIST}.bak 2>/dev/null")
# clear remote dist
run(f"rm -rf {REMOTE_DIST}/*")

sftp = ssh.open_sftp()


def upload_dir(local, remote):
    try:
        sftp.mkdir(remote)
    except IOError:
        pass
    for item in os.listdir(local):
        lp = os.path.join(local, item)
        rp = remote + "/" + item
        if os.path.isdir(lp):
            upload_dir(lp, rp)
        else:
            sftp.put(lp, rp)


upload_dir(LOCAL_DIST, REMOTE_DIST)
sftp.close()

out, err = run("systemctl restart codebuddy-dashboard && sleep 1 && systemctl is-active codebuddy-dashboard")
print("service:", out.strip(), err.strip())

ssh.close()
print("DONE")
