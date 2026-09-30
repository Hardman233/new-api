#!/bin/bash
# New-API 备份脚本：MySQL 全量 + Redis + 配置文件
# 定时执行：crontab -e 添加 0 2 * * * /path/to/backup.sh >> /var/log/newapi-backup.log 2>&1

set -euo pipefail

BACKUP_DIR="/data/backup"
DATE=$(date +%Y%m%d_%H%M%S)
RETENTION_DAYS=7
DEPLOY_DIR="/data/newapi-cluster/deploy"  # ⚠️ 根据实际部署路径修改

mkdir -p "$BACKUP_DIR"
cd "$DEPLOY_DIR"

# 加载环境变量
if [ -f .env ]; then
    source .env
else
    echo "ERROR: .env not found in $DEPLOY_DIR"
    exit 1
fi

echo "=== $(date) 开始备份 ==="

# 1. MySQL 全量备份（含 GTID、主从位置信息）
echo "备份 MySQL..."
docker exec mysql-master mysqldump \
    -uroot -p"${MYSQL_ROOT_PASSWORD}" \
    --all-databases \
    --single-transaction \
    --master-data=2 \
    --flush-logs \
    --routines \
    --triggers \
    --events \
    | gzip > "$BACKUP_DIR/mysql_${DATE}.sql.gz"

# 2. Redis 备份
echo "备份 Redis..."
docker exec redis-master redis-cli --no-auth-warning -a "${REDIS_PASSWORD}" BGSAVE
sleep 5  # 等待后台保存完成
docker cp redis-master:/data/dump.rdb "$BACKUP_DIR/redis_${DATE}.rdb"

# 3. 配置文件备份（含密钥、nginx、mysql 配置）
echo "备份配置文件..."
tar -czf "$BACKUP_DIR/config_${DATE}.tar.gz" \
    "$DEPLOY_DIR/.env" \
    "$DEPLOY_DIR/docker-compose.production.yml" \
    "$DEPLOY_DIR/nginx" \
    "$DEPLOY_DIR/mysql/conf" \
    2>/dev/null || true

# 4. 清理过期备份
echo "清理 $RETENTION_DAYS 天前的备份..."
find "$BACKUP_DIR" -name "mysql_*" -mtime +$RETENTION_DAYS -delete
find "$BACKUP_DIR" -name "redis_*" -mtime +$RETENTION_DAYS -delete
find "$BACKUP_DIR" -name "config_*" -mtime +$RETENTION_DAYS -delete

echo "=== 备份完成 ==="
echo "备份文件："
ls -lh "$BACKUP_DIR" | grep "$DATE"
