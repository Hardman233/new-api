-- 审计日志库初始化（可选，如需独立日志库则启用 LOG_SQL_DSN）
-- 当前配置审计表自动建在主库 newapi，无需此文件；保留供后续独立日志库时使用

CREATE DATABASE IF NOT EXISTS newapi_logs CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
GRANT ALL PRIVILEGES ON newapi_logs.* TO 'newapi'@'%';
FLUSH PRIVILEGES;
