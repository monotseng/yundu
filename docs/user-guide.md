# 云渡文件交换平台：安装与使用手册

## 1. 安装前准备

服务器需要满足：

- Linux AMD64 或 ARM64。
- MySQL 8.4，可创建数据库和应用账号。
- 至少两个已启用版本控制的 S3/MinIO 存储区域。
- 能够解析办公入口和生产入口域名。
- 生产环境由 Nginx 或同类反向代理终止 HTTPS。

发行包内包含单体服务、初始化配置和文档，不需要安装 Go、Node.js 或前端运行环境。

## 2. 解压与目录

```bash
tar -xzf yundu-server-v1.0.0-rc.3-linux-arm64.tar.gz
cd yundu-server-v1.0.0-rc.3-linux-arm64
```

```text
bin/yundu-server       单体服务程序
config/config.yaml     初始化配置
config/env.example     秘密环境变量示例
docs/                  产品、使用和运维文档
runtime/               PID 和日志目录
```

## 3. 初始化数据库

创建空数据库和最小权限账号，字符集使用 `utf8mb4`：

```sql
CREATE DATABASE yundu CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
CREATE USER 'yundu_app'@'应用服务器地址' IDENTIFIED BY '请替换为强密码';
GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, ALTER, INDEX, REFERENCES
  ON yundu.* TO 'yundu_app'@'应用服务器地址';
```

数据库结构由二进制自动迁移，不需要手工导入 SQL。

## 4. 配置秘密

数据库密码和主密钥只通过环境变量注入，不写入 YAML：

```bash
export YUNDU_DB_PASSWORD='数据库强密码'
export YUNDU_MASTER_KEY="$(openssl rand -base64 32)"
```

首次生成的 `YUNDU_MASTER_KEY` 必须固定保存到密码保险库。丢失或更换主密钥会导致既有 TOTP 和集成凭据无法解密。

## 5. 修改配置

编辑 `config/config.yaml`：

- `server.listen_address`：服务监听地址。
- `server.public_base_url`：管理访问基准地址。
- `database`：MySQL 地址、库名、账号和连接池。
- `server.trusted_proxies`：可信反向代理网段。
- `security.trust_portal_header_from`：允许声明入口安全域的代理网段。
- `portals.office.origin`：办公入口完整 Origin。
- `portals.production.origin`：生产入口完整 Origin。
- `limits`：并发和文件限制；办公到生产的硬限制不能调高。
- `antivirus`：ClamAV 开关、地址和超时。

校验配置：

```bash
bin/yundu-server --check-config --config config/config.yaml
```

## 6. 创建首位管理员

```bash
bin/yundu-server \
  --bootstrap-admin admin \
  --display-name '系统管理员' \
  --config config/config.yaml
```

命令只在用户表为空时成功，并且只显示一次激活凭据。管理员从任一有效门户的 `/#/activate` 打开激活页，设置密码、扫描 TOTP 二维码并确认绑定，然后重新登录。

## 7. 启动、状态与停止

```bash
bin/yundu-server --start \
  --config config/config.yaml \
  --pid-file runtime/yundu.pid \
  --log-file runtime/yundu.log

bin/yundu-server --status --pid-file runtime/yundu.pid

bin/yundu-server --stop --pid-file runtime/yundu.pid
```

每次启动都会先应用二进制内嵌的待执行数据库迁移。若迁移失败，服务不会开始监听端口。

健康检查：

```bash
curl -fsS http://127.0.0.1:9080/health/live
curl -fsS http://127.0.0.1:9080/health/ready
```

## 8. 首次业务配置顺序

1. 在“组织与用户”创建部门、团队和用户。
2. 为用户分配角色、授权范围和团队身份。
3. 创建业务系统，配置允许的交换方向。
4. 在“集成配置”创建办公区、生产区对象存储并完成连接测试。
5. 确认 Bucket 已启用版本控制，然后发布存储实例。
6. 在“交换与传输”为两个方向分别选择源、目标存储并发布渠道。
7. 在“审批流程”创建节点和连线，校验、模拟并发布流程。
8. 将已发布流程绑定到方向、团队或业务系统范围。

建议至少使用四个逻辑桶：办公接收、生产交付、生产接收、办公交付。测试环境可以共用 MinIO 服务，但不应让两个安全域共用同一访问凭据。

## 9. 用户交换流程

![首页概览](image/yundu/zh/Snipaste_2026-09-17_09-33-06.png)

### 9.1 创建申请

用户从文件来源入口登录，进入“新建交换”，选择团队、业务系统和密级，填写用途并上传文件。页面会检查当前方向的存储渠道和审批流程是否完整。

![新建交换申请](image/yundu/zh/Snipaste_2026-09-17_09-34-03.png)

### 9.2 查看进度

进入“我的申请”，可以查看申请状态、来源检查报告和流程节点。审批人员在“我的审批”办理当前任务。

### 9.3 领取文件

传输完成后，申请人从目标入口登录“文件领取”。页面一张申请单显示一行，点击左侧加号展开文件明细，然后逐个领取。领取记录按申请单集中查看。

### 9.4 切换界面语言

登录页、激活页或登录后的右上角均提供地球图标和 `en`/`zh` 按钮，可以在中文与英文之间即时切换。按钮显示的是可切换到的目标语言；选择结果保存在浏览器本地，下次访问和重新登录后继续使用。

## 10. 升级与回滚注意事项

升级前备份 MySQL 和对象存储配置，停止旧服务，替换二进制后重新启动。数据库迁移只向前执行；如需回退程序版本，应先确认旧程序兼容已经升级的数据库结构，不要直接删除迁移记录。

完整部署步骤参见 `docs/deployment-guide.md`；日常操作、故障处理和数据库恢复流程参见 `docs/operations-runbook.md` 与 `docs/database-recovery.md`。
