# ManyRouter

<!-- impeccable:product-schema 1 -->

## Platform

web

## Stack

已确认：React 19、TypeScript 严格模式、Rsbuild 2、TanStack Router、TanStack Query、Tailwind CSS 4、Base UI、i18next、pnpm 10。ManyRouter 控制台位于 `web/`，通过同源管理接口访问 Go 服务。

## Users

运营所有者反复维护多个 New API 站点、供应商、模型采购价、人工 Auto 成员及各站售价；需要快速判断配置属于哪个站点，以及修改是否真正同步到站点。

## Product Purpose

一次录入供应商，再投放到多个站点。上游凭证共享，站点销售设置、Auto、价格和同步结果独立。ManyRouter 负责保存并下发配置，New API 继续完成用户请求转发与计费。

## Operating Context

日常操作顺序是维护供应商和模型、选择目标站点投放、检查并发布站点配置、查看同步结果、必要时恢复历史线路。运营者在桌面浏览器进行重复操作，移动浏览器可查看记录并完成紧急停用。

## Capabilities and Constraints

- M1 交付所有者初始化与登录、多站供应商运营、人工 Auto、独立售价、线路历史恢复、同步与审计。
- 专属分组和固定 Auto 采用入口开放状态；关闭入口后，已有绑定该组的密钥也停止调用，成员、渠道、价格和历史继续保留。
- 密钥输入后不回显；轮换操作独立于普通资料保存。
- 价格使用十进制字符串，不在浏览器中转换为浮点金额。
- 自动评分、自动选线和用户自定义 Auto 属于后续里程碑。
- 页面不得把本地状态当成服务端保存成功；接口失败保留输入并给出可操作错误。

## Brand Commitments

名称为 ManyRouter。采用中文业务文案、克制且高密度的运营后台；不使用营销区块、说明卡、虚构指标或装饰性图表。

## Evidence on Hand

需求、技术、架构与路线图位于 `docs/`；M0 管理与同步接口已实现。新接口以本仓库的实际后端契约为准，空站点和失败结果如实展示。

## Product Principles

- 先明确站点，再执行影响该站点的操作。
- 上游采购与用户售价分别维护。
- 保存、发布与真正生效分别表达。
- 历史记录支持追溯，恢复以新版本完成。
