# 前端依赖安全审计

审计日期：2026-09-17

## 本次升级

- `@umijs/max` 从 4.7.17 升级到官方补丁版本 4.7.18。
- 通过 npm `overrides` 将旧 Axios、Immer、Node Fetch、PostCSS、Send 和兼容的 Path-to-RegExp 分支提升到已修复版本。
- 使用 `npm ci` 重新生成干净依赖树，并完成 TypeScript 检查和生产构建。

## 审计结果

| 严重级别 | 升级前 | 升级后 |
|---|---:|---:|
| Critical | 1 | 0 |
| High | 17 | 8 |
| Moderate | 18 | 19 |
| Low | 10 | 9 |
| 合计 | 46 | 36 |

剩余 High 项来自 Umi 4.7.18 间接依赖的打包器、Less/Image Size、Ant Design Pro Layout 和 Path-to-RegExp。审计时对应上游尚无可直接升级且保持兼容的修复版本，不能通过 `npm audit fix --force` 强制替换，否则可能降级 Umi 或破坏路由和构建接口。

## 运行时影响

Node.js 和 `node_modules` 只参与前端构建，不进入发行包。生产介质包含 Go 静态单体二进制及其内嵌的浏览器静态资源，因此上述告警不代表生产服务器安装了这些 Node.js 包。后续仍应持续跟踪 Umi 上游版本，并在其发布兼容修复后重新审计和构建。

复核命令：

```bash
cd web
npm ci
npm audit
npm run typecheck
npm run build
```
