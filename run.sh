#!/usr/bin/env bash
source /etc/profile
date

# 获取脚本所在目录的绝对路径
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# 运行预订（Docker 方式；.cache 挂载持久化用户凭证与节假日缓存，需 uid 1001 可写）
echo "Running meeting room booking script..."
docker run --rm --env-file .env -v "$SCRIPT_DIR/.cache:/app/.cache" room auto --run

if [ $? -eq 0 ]; then
    echo "Script completed successfully"
else
    echo "Script failed with error code $?"
    exit 1
fi
