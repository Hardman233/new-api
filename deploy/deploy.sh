#!/bin/bash
# New-API 一键部署脚本（国内服务器生产环境）
# 用法：./deploy.sh

set -euo pipefail

echo "=== New-API 国内服务器一键部署 ==="
echo

# 检查 Docker
if ! command -v docker &> /dev/null; then
    echo "❌ Docker 未安装，正在安装..."
    curl -fsSL https://get.docker.com | bash
    systemctl enable --now docker
fi

if ! command -v docker-compose &> /dev/null; then
    echo "❌ Docker Compose 未安装，正在安装..."
    COMPOSE_VERSION=$(curl -s https://api.github.com/repos/docker/compose/releases/latest | grep tag_name | cut -d\" -f4)
    curl -L "https://github.com/docker/compose/releases/download/${COMPOSE_VERSION}/docker-compose-$(uname -s)-$(uname -m)" -o /usr/local/bin/docker-compose
    chmod +x /usr/local/bin/docker-compose
fi

echo "✅ Docker 环境就绪"
echo

# 检查 .env
if [ ! -f .env ]; then
    echo "⚠️  未找到 .env 文件，正在生成..."
    echo

    cat > .env <<EOF
# 自动生成的密钥（首次部署后请妥善备份此文件）
SESSION_SECRET=$(openssl rand -base64 32)
CRYPTO_SECRET=$(openssl rand -base64 32)
MYSQL_ROOT_PASSWORD=$(openssl rand -base64 16)
MYSQL_PASSWORD=$(openssl rand -base64 16)
REDIS_PASSWORD=$(openssl rand -base64 16)
MYSQL_REPL_PASSWORD=$(openssl rand -base64 16)
AUDIT_LOG_SECRET=$(openssl rand -base64 32)

# 审计媒体 OSS（外部云 S3 兼容）留空则只记元信息，纯文本对话仍进库
AUDIT_OSS_BUCKET=
AUDIT_OSS_ENDPOINT=
AUDIT_OSS_REGION=
AUDIT_OSS_ACCESS_KEY=
AUDIT_OSS_SECRET_KEY=
EOF

    chmod 600 .env
    cp .env .env.backup

    echo "✅ 已生成 .env 和 .env.backup"
    echo "⚠️  请妥善备份 .env 文件（含关键密钥）"
    echo
else
    echo "✅ 发现已有 .env 文件"
    echo
fi

# 检查域名配置
if grep -q "api.yourdomain.com" nginx/conf.d/newapi.conf; then
    echo "⚠️  Nginx 配置中域名仍为占位符 api.yourdomain.com"
    read -p "请输入你的域名（如 api.example.com）: " DOMAIN
    if [ -n "$DOMAIN" ]; then
        sed -i "s/api.yourdomain.com/$DOMAIN/g" nginx/conf.d/newapi.conf
        echo "✅ 已更新域名为 $DOMAIN"
    else
        echo "⚠️  域名未更新，稍后请手动编辑 nginx/conf.d/newapi.conf"
    fi
    echo
fi

# 检查 SSL 证书
if [ ! -f nginx/ssl/fullchain.pem ] || [ ! -f nginx/ssl/privkey.pem ]; then
    echo "⚠️  未找到 SSL 证书"
    echo "请将证书文件放入 nginx/ssl/ 目录："
    echo "  - fullchain.pem（完整证书链）"
    echo "  - privkey.pem（私钥）"
    echo
    read -p "证书已放置？继续部署将使用 HTTP (y/N): " CONTINUE
    if [[ ! "$CONTINUE" =~ ^[Yy]$ ]]; then
        echo "已取消部署，请放置证书后重新运行"
        exit 1
    fi
fi

# 创建必要目录
echo "创建数据目录..."
mkdir -p data logs/{nginx,newapi-master,newapi-slave-1,newapi-slave-2} mysql/{data,conf,init} redis/data nginx/ssl
echo "✅ 目录结构就绪"
echo

# 启动服务
echo "启动 Docker Compose..."
docker-compose -f docker-compose.production.yml config > /dev/null
docker-compose -f docker-compose.production.yml up -d
echo
echo "✅ 服务已启动"
echo

# 检查服务状态
echo "等待服务就绪（约 30 秒）..."
sleep 30
docker-compose -f docker-compose.production.yml ps
echo

# 健康检查
echo "=== 健康检查 ==="
for port in 3001 3002 3003; do
    if curl -sf http://127.0.0.1:$port/api/status > /dev/null; then
        echo "✅ NewAPI 端口 $port 正常"
    else
        echo "⚠️  NewAPI 端口 $port 未响应"
    fi
done
echo

echo "=== 部署完成 ==="
echo
echo "后续步骤："
echo "1. 访问管理后台：http://your-server-ip:3001（master 节点）"
echo "2. 创建管理员账号并登录"
echo "3. 配置渠道、模型价格、用户令牌"
echo "4. （可选）配置 OSS：编辑 .env 填入 AUDIT_OSS_* 变量后 docker-compose up -d"
echo "5. 设置定时备份：crontab -e 添加 0 2 * * * $(pwd)/scripts/backup.sh"
echo
echo "查看日志：docker-compose -f docker-compose.production.yml logs -f --tail=100"
echo "重启服务：docker-compose -f docker-compose.production.yml restart"
echo
echo "⚠️  重要：请将 .env.backup 文件保存到异地（含所有密钥）"
