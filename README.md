# ManyRouter

ManyRouter 是 New API 站点的供应商与线路控制服务。M0 已完成：一个真实供应商可以自动接入一个 New API 站点，并通过重新读取确认渠道、专属分组和价格已经生效。

## 当前能力

- 加密保存 New API 管理凭证和供应商上游凭证。
- 录入站点、供应商、模型与采购价格。
- 生成带版本的单站线路方案。
- 通过 River 持久任务调用 New API 现有管理接口。
- 自动写入专属分组价格与用户可选范围，同时保留非受管配置。
- 识别人工停用、重复资源和绑定冲突。
- 测试渠道后启用，并重新读取确认真实状态。

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
