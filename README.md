# 云渡文件交换平台

当前开发里程碑：M9（V1.0 开发交付收口）。详见 [产品功能说明](docs/product-guide.md)、[安装与使用手册](docs/user-guide.md)、[M9 交付说明](docs/m9-delivery.md)与 [AC01～AC64 证据矩阵](docs/ac01-ac64-evidence.md)。

生产与办公网络文件交换及审批。

当前仓库按 M0～M9 分阶段实现。当前运行方式为配置化外部 MySQL + 单体二进制，不依赖 Docker 或 Compose。

## 本地启动

```bash
cp configs/config.example.yaml configs/config.local.yaml
export YUNDU_DB_PASSWORD='本地数据库密码'
export YUNDU_MASTER_KEY='部署时生成并固定保存的32字节Base64主密钥'
./release/yundu-linux-arm64 --migrate --config configs/config.local.yaml
./release/yundu-linux-arm64 --bootstrap-admin admin --display-name '系统管理员' --config configs/config.local.yaml
./release/yundu-linux-arm64 --start --config configs/config.local.yaml --pid-file yundu.pid --log-file yundu.log
```

首次引导命令仅在用户表为空时可执行，激活凭据只在终端显示一次，默认 24 小时有效。使用办公或生产入口打开 `/#/activate`，设置密码并用 Microsoft Authenticator 扫描平台内部生成的二维码。绑定成功不会直接创建业务会话，必须重新进行密码和 TOTP 登录。主密钥必须稳定保存；更换或丢失会导致既有 TOTP 秘钥无法解密。

查看状态与停止：

```bash
./release/yundu-linux-arm64 --status --pid-file yundu.pid
./release/yundu-linux-arm64 --stop --pid-file yundu.pid
```

访问 `http://127.0.0.1:9080`。不传 `--start` 时以前台模式运行，仍会创建 PID 文件并可由另一个进程使用 `--stop` 优雅停止。开发模式前端可独立启动：

```bash
cd web
npm ci
npm run dev
```

## 单体构建

```bash
make build
./release/yundu-linux-arm64 --config configs/config.local.yaml
```

`make build` 根据当前架构生成唯一发行文件 `release/yundu-linux-<arch>`；`make build-all` 同时生成 AMD64、ARM64 版本。数据库迁移、服务启停和嵌入式前端都包含在该程序内，运行时不需要 Node.js。数据库地址、账号、密码、库名和连接池均来自 YAML；生产密码建议写成 `${YUNDU_DB_PASSWORD}`。

开发阶段和未联调项见 [实施计划](docs/implementation-plan.md)，接口见 [OpenAPI](api/openapi.yaml)。

## 发行包

构建 AMD64、ARM64 两个标准交付包：

```bash
make release-package VERSION=1.0.0-rc.1
```

产物写入 `release/`，每个压缩包包含 `bin/yundu-server`、生产初始化配置、秘密环境变量示例和必要文档，同时生成 SHA-256 校验文件。
