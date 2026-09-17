# 云渡 v1.0.0-rc.4 发布说明

发布日期：2026-09-17

## 本次更新

- 完成中文、英文界面补充及动态内容国际化修复。
- 优化登录、验证码、品牌区、侧栏菜单和版本信息布局。
- 升级 Umi 至 4.7.18，并消除前端依赖树中的 Critical 告警。
- 提供中英文 GitHub 项目说明和产品截图。
- 改进发行脚本，为每个平台包生成平台描述及包内 SHA-256 清单。

## 发行介质

- `yundu-server-v1.0.0-rc.4-linux-amd64.tar.gz`
- `yundu-server-v1.0.0-rc.4-linux-arm64.tar.gz`
- `SHA256SUMS-v1.0.0-rc.4.txt`

每个架构包均包含对应静态二进制、生产配置模板、秘密环境变量模板、中英文 README、部署及运维文档、`PLATFORM` 平台信息和 `MANIFEST.sha256` 包内文件校验清单。

## 已知边界

本版本仍为发布候选版。前端构建链剩余 36 项上游告警（0 Critical），详情见 `frontend-dependency-audit.md`。真实对象存储、Authenticator、ClamAV、企业微信、SMTP、WORM、Zabbix、负载和恢复演练仍需在企业预生产环境留存验收证据。
