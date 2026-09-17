# 云渡文件交换平台部署指南

本文适用于 `v1.0.0-rc.5` 单体发行包。当前部署不依赖 Docker、Compose、Node.js、Redis或消息队列；前端资源、数据库迁移和后台任务均包含在 `bin/yundu-server` 中。

## 1. 部署拓扑

- 一台 Linux AMD64 或 ARM64 应用服务器。
- MySQL 8.4，数据库和会话时区统一为 UTC。
- 办公区和生产区可访问的 S3/MinIO 版本化对象存储。
- Nginx 提供办公、生产两个 HTTPS 域名，并转发到同一个单体进程。
- 数据库密码和主密钥由环境变量注入。

办公入口与生产入口必须使用不同 Origin。平台根据可信代理传入的 `X-Yundu-Portal-Zone` 判断安全域，并再次校验请求 Origin。

## 2. 安装目录

```bash
sudo useradd --system --home /opt/yundu --shell /usr/sbin/nologin yundu
sudo mkdir -p /opt/yundu /etc/yundu /var/log/yundu /run/yundu
sudo chown yundu:yundu /opt/yundu /var/log/yundu /run/yundu
tar -xzf yundu-server-v1.0.0-rc.5-linux-arm64.tar.gz
sudo cp yundu-server-v1.0.0-rc.5-linux-arm64/bin/yundu-server /opt/yundu/
sudo cp yundu-server-v1.0.0-rc.5-linux-arm64/config/config.yaml /etc/yundu/config.yaml
sudo chmod 0755 /opt/yundu/yundu-server
sudo chmod 0640 /etc/yundu/config.yaml
```

AMD64 服务器将包名中的 `arm64` 替换为 `amd64`。

## 3. 数据库与秘密

```sql
CREATE DATABASE yundu CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
CREATE USER 'yundu_app'@'应用服务器地址' IDENTIFIED BY '强密码';
GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, ALTER, INDEX, REFERENCES
  ON yundu.* TO 'yundu_app'@'应用服务器地址';
```

通过企业秘密系统注入以下变量：

```bash
export YUNDU_DB_PASSWORD='数据库密码'
export YUNDU_MASTER_KEY='固定保存的至少 32 字节随机主密钥'
```

主密钥不得重新随机生成。丢失主密钥会导致既有 TOTP 和集成凭据无法解密。

## 4. 应用配置

编辑 `/etc/yundu/config.yaml`，至少替换 MySQL 地址、管理基准地址以及两个门户 Origin。交付配置中的 `security.trust_portal_header_from` 默认包含 `0.0.0.0/0` 和 `::/0`，便于首次安装直接访问；它只放开门户域请求头的来源，门户 Origin 校验仍然生效。生产环境完成联调后，建议将其收紧为实际 Nginx 出口地址或网段。`server.trusted_proxies` 仅在需要解析代理转发的客户端地址时配置，并应始终限定为实际可信代理。

```bash
sudo -u yundu -E /opt/yundu/yundu-server --check-config --config /etc/yundu/config.yaml
sudo -u yundu -E /opt/yundu/yundu-server --migrate --config /etc/yundu/config.yaml
```

本版本最新数据库迁移版本为 `19`。

## 5. Nginx 双入口示例

两个 server 块使用相同上游，但注入不同且固定的安全域。不要透传客户端提供的同名请求头。

```nginx
upstream yundu_backend {
    server 127.0.0.1:9080;
    keepalive 32;
}

server {
    listen 443 ssl http2;
    server_name office.example.internal;
    # ssl_certificate / ssl_certificate_key 按企业规范配置

    location / {
        proxy_pass http://yundu_backend;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Yundu-Portal-Zone OFFICE;
        proxy_request_buffering off;
        proxy_read_timeout 600s;
    }
}

server {
    listen 443 ssl http2;
    server_name production.example.internal;

    location / {
        proxy_pass http://yundu_backend;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Yundu-Portal-Zone PRODUCTION;
        proxy_request_buffering off;
        proxy_read_timeout 600s;
    }
}
```

配置中的 `portals.office.origin` 与 `portals.production.origin` 必须分别精确匹配上述 HTTPS Origin，不包含路径和末尾斜杠。

## 6. 首位管理员与启动

仅在用户表为空时执行一次：

```bash
sudo -u yundu -E /opt/yundu/yundu-server --bootstrap-admin admin \
  --display-name '系统管理员' --config /etc/yundu/config.yaml
```

妥善传递终端一次性显示的激活凭据。管理员访问任一门户的 `/#/activate`，设置密码并绑定 Microsoft Authenticator。

```bash
sudo -u yundu -E /opt/yundu/yundu-server --start \
  --config /etc/yundu/config.yaml \
  --pid-file /run/yundu/yundu.pid \
  --log-file /var/log/yundu/yundu.log

sudo -u yundu /opt/yundu/yundu-server --status --pid-file /run/yundu/yundu.pid
curl -fsS http://127.0.0.1:9080/health/live
curl -fsS http://127.0.0.1:9080/health/ready
```

停止服务：

```bash
sudo -u yundu /opt/yundu/yundu-server --stop --pid-file /run/yundu/yundu.pid
```

## 7. 上线验证

1. 分别从办公、生产域名登录，确认入口安全域显示正确。
2. 创建部门、团队、用户、业务系统和角色范围。
3. 配置两个安全域的版本化存储，完成测试并发布。
4. 发布双向交换通道和审批流程，并完成方向绑定。
5. 使用普通用户完成上传、检查、审批、传输及目标入口领取。
6. 验证审计事件中的实名操作者、时间和结果。
7. 验证 `zh`/`en` 切换及角色菜单隔离。

## 8. 升级与回滚

升级前备份 MySQL、配置和旧二进制，执行 `--stop` 后替换程序，再执行 `--check-config`、`--migrate` 和 `--start`。数据库迁移只向前执行；回滚程序前必须确认旧版本兼容当前 schema，不得删除迁移记录或直接回退数据库结构。

生产高可用、容器化和性能拓扑不属于本候选包范围，需在企业预生产性能验证后单独设计。
