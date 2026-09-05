# ManyRouter

ManyRouter 是 New API 站点的供应商与线路控制服务。M0 已完成真实单站接入验收；M1 已实现多站人工运营、独立价格版本和运营控制台，真实双站供应商验收仍待完成。进度见 [路线图](./docs/roadmap.md)。

## 当前能力

- 加密保存 New API 管理凭证和供应商上游凭证。
- 录入站点、供应商、模型与采购价格。
- 生成带版本的单站线路方案。
- 通过 River 持久任务调用 New API 现有管理接口。
- 自动写入专属分组价格与用户可选范围，同时保留非受管配置。
- 识别人工停用、重复资源和绑定冲突。
- 测试渠道后启用，并重新读取确认真实状态。
- 通过独立所有者账号登录控制台，按站点管理供应商投放、人工 Auto 和销售倍率。
- 保存整站线路历史；恢复时保留当前共享供应商凭证和已确认售价，并检查历史模型仍可用。
- 更换、替换或取消待同步供应商密钥，分别显示各站点已确认的版本。
- 草案确认后发布价格；只有 New API 倍率与计费基准重读一致才记录核对成功。
- 不同站点分别执行，同一站点按顺序同步；过期任务不会覆盖更新的配置。

M0 不管理 New API 用户、用户 API Key、余额和计费，也不进入用户请求链路。

## 运行模式

```powershell
manyrouter migrate
manyrouter serve
manyrouter worker
manyrouter all
```

启动配置见 [`.env.example`](./.env.example)。部署时先运行 `migrate`，应用启动不会自动执行未知数据库迁移。

## 本地门禁

```powershell
pnpm gate
```

PostgreSQL 集成测试使用 WSL Docker：

```powershell
wsl.exe -d Ubuntu-24.04 --cd /mnt/c/code/ManyRouter docker compose -f deploy/compose.test.yaml up -d --wait
$env:MANYROUTER_TEST_DATABASE_URL='postgres://manyrouter_test:manyrouter_test@127.0.0.1:55432/manyrouter_test?sslmode=disable'
pnpm test:integration
```

真实 New API 合同测试需要先提供从声明提交编译的独立测试二进制：

```powershell
$env:MANYROUTER_NEW_API_BINARY='C:\path\to\new-api.exe'
pnpm test:contract
```

合同测试在临时目录和随机本机端口运行 New API，使用临时 SQLite 与本地模拟上游，结束后自动停止进程并清理运行数据。

## 管理接口顺序

1. `POST /api/v1/sites`
2. `POST /api/v1/suppliers`
3. `POST /api/v1/site-suppliers`
4. `POST /api/v1/site-suppliers/{relation_id}/sync`
5. `GET /api/v1/sync-operations/{operation_id}`

管理接口使用 `Authorization: Bearer <运营令牌>`，写请求同时要求 `Idempotency-Key`。完整契约见 [`api/openapi.yaml`](./api/openapi.yaml)。

## M1 控制台

前端位于 `web/`，使用已确认的 React、TypeScript 严格模式和 Rsbuild。依赖与构建：

```powershell
pnpm install
pnpm --dir web check
pnpm --dir web test
pnpm --dir web build
```

本地前端通过 `/api` 代理后端，代理目标可用 `MANYROUTER_API_TARGET` 指定。启动前先注入 `.env.example` 所列后端环境变量；程序不会自动读取 `.env`。生产会话 Cookie 默认要求 HTTPS，只有本地 HTTP 调试才设置 `MANYROUTER_AUTH_COOKIE_SECURE=false`。

首次打开控制台时，以部署运营令牌完成一次性所有者初始化。之后使用账号密码和服务端会话；浏览器不保存供应商密钥或部署令牌。M0 运营令牌接口仍保留兼容权限，应只用于受保护的管理环境。

运营契约见 [`api/operations.yaml`](./api/operations.yaml)。写操作要求操作原因、当前业务版本、重复请求编号及会话防伪造校验。保存或返回同步任务仅表示目标配置已接收，实际结果在同步记录中查看。

售价页面管理分组倍率及对应站点计费基准，尚未宣称独立计算所有模型的最终扣费。New API 管理员直接修改基础计费规则时，后续同步会发现基准差异；修改过程中可能已产生部分生效结果，最近确认历史不能替代实时账单。

专属分组和 Auto 的入口开关沿用 New API 用户分组权限：关闭入口后，已有该组密钥也会被拒绝调用。双站进程测试已确认关闭入口返回 403，运营时应把它当作影响现有调用的变更。
