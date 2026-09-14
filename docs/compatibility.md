# Compatibility and migration policy

Yotta 尚未发布稳定版。`internal/` Go API、Wails RPC、节点实现接口和未发布合同可以破坏性演进；被淘汰的
开发期 artifact 必须在 strict boundary 明确拒绝，不能静默猜测、修复或交给第二套 runtime。

## 4.0 public compatibility floor

`v4.0.0` 是第一个公开兼容基线。基线不是一份人工抄录的版本表，而是三组不可覆盖的 tracked snapshot：

- `contracts/releases/4.0.0/version-domains.json` 冻结 durable/portable/contract/protocol 等独立版本域；
- `contracts/node/releases/4.0.0/builtin-node-refs.json` 冻结 147 个可持久引用的 built-in NodeRef；
- `contracts/catalog/releases/4.0.0/builtin-catalog-refs.json` 冻结 24 个 TypeRef 和 5 个 CapabilityRef。

产品版本本身不替代各格式、合同和协议的独立 identity。`task versions:inventory` 从当前代码列出完整清单，
`task versions:compatibility:check` 与 `task nodes:compatibility:check` 则把当前 reader/Catalog 与所有已冻结
公开基线逐一比较。

- 4.0.0 之前只在开发环境产生的 artifact 不形成公开升级承诺。需要保留的 owner profile 先在备份或
  staging authority 上运行一次性升级并验证重开，随后 production reader 可以只接受基线格式。
- 一次性升级工具不得成为 ambient fallback、dual reader 或第二套 runtime；切换完成后可以连同旧 fixture
  一起删除，旧 artifact 在 strict boundary 明确报 unsupported。
- 从 4.0.0 起，只要某个 writer 已进入公开稳定版本，它产生的 durable artifact 就必须通过确定性、可测试的
  相邻 migration 升级；不得以“历史较少”为由原地改 identity 或删除仍在支持窗口内的 reader。

## Independent version domains

- 产品版本只来自根 `VERSION`，用于构建、安装包和展示。
- Workflow Source、Data Type、Node Contract、Authoring Projection、Catalog、Program、Run Record、Schedule、
  settings、package registry、worker/plugin/MCP protocol 和 store layout 各自拥有 `format + version`。
- Node Type 使用稳定 URI、独立 SemVer 和 semantic digest；Compiler 使用独立 build identity。
- 这些版本域不与产品版本同步。当前值从所属 package/schema 读取，`task versions:inventory` 只做聚合展示。

## Change rules

- shape 或语义变化先提升所属合同版本，再添加确定性的相邻 migration；不能在同一 identity 下原地改 schema。
- 提升已冻结版本域的 writer 时，代码声明的 `ReadableVersions` 必须继续包含仍受支持的旧版本，并以冻结旧
  fixture、迁移后重开测试证明 reader；只修改 release snapshot 或声明列表不算兼容实现。
- 已冻结 NodeRef 可以原样保留，也可以通过完整、唯一、无环的相邻 NodeRef migration 链升级；同一
  `(nodeTypeId, version)` 的 semantic digest 漂移会直接使门禁失败。
- 已冻结 TypeRef/CapabilityRef 当前没有 rewrite registry，必须原样保留。语义变化应增加新的 versioned URI，
  不能复用旧 ID 改 digest；若未来确需替换，必须先实现引用迁移再改变本规则。
- migration 在副本或 staging authority 上执行，结果通过当前 strict validation、identity/revision 与引用完整性
  检查后才能 durable publish。
- migration 失败、版本未知、checksum 漂移、身份变化或未来 schema 时保留原事实并 fail closed/quarantine；
  不增加 fallback reader、dual-write 或旧 runtime。
- 稳定 migration 必须有冻结旧 fixture、preflight/dry-run、链式升级、kill-point resume/recovery 和重开测试。
- settings 等兼容 reader 如果接受 retired field，成功读取后必须 durable rewrite；不能只在内存丢字段。
- 删除/重命名节点、端口、错误码、Target kind、capability 或 durable field 是 breaking change，需要 release note
  和明确旧 artifact 行为。

## Current development upgrade seam

当前代码仍包含以下 pre-4.0 一次性升级入口：

| Durable artifact | 当前迁移路径与发布行为 |
| --- | --- |
| profile root | `internal/storage/migrate` 注册 layout 1 → 2 → 3；1 → 2 升级 Catalog 并导入旧 Run JSON，2 → 3 把 Blob layout 1 迁到当前分片布局。步骤使用 snapshot、journal、校验、resume/rollback 后再发布 root manifest |
| Schedule | `internal/services/schedule.Store` 链式读取 v1 → v2 → v3 → v4 → v5；全部文件先解析、迁移和校验，再以 crash-atomic 文件替换持久写回 |
| Macro | `internal/services/macro` 严格读取 v1 carrier，迁成 v2；Service 成功读取后用 Blob record CAS 发布新的 canonical carrier |
| settings | envelope 仍是 `yotta.settings/1`；兼容 reader 只删除明确登记的 `workflowConsent`、`allowPrivateNetwork` 和 `executableDigest` retired fields，并立刻保存为新的 generation |
| Workflow Source format | `internal/workflowstore` 显式读取 v1、v2、v3、v4 并迁移到 v5；v2 增加对外参数，v3 增加独立展示块，v4 增加标题说明，v5 增加工作流运行目标声明。保留作者默认值与稳定参数/目标 ID，本机参数和目标绑定独立存储，不进入发布包；本地旧目标引用迁移时先保留本机绑定，再通过 strict validation 和原 Source CAS 发布 |
| Workflow NodeRef | Source Store 打开时只应用 `internal/workflow/authoring` 已登记且 reducer 能证明兼容的 contract upgrade；先对全部 Source preflight，再通过 Catalog revision CAS 持久发布，失败时不留下半迁移集合 |
| Workflow Bundle | v1 manifest 可升级为 v2；导入时先校验归档 Source，再进入与本地 Source 相同的 migration seam，不维护第二套节点升级逻辑 |
| Snippet NodeRef | Snippet 读取/保存时复用 Workflow Authoring 的 detached-node migration；成功后保持用户时间戳并原子写回，关闭并重开仍是当前 NodeRef |
| TypeRef / CapabilityRef | 4.0 引用由 built-in Catalog release snapshot 精确冻结；当前策略是保留旧 versioned URI 并并列增加新 URI，不做运行期猜测 |
| Asset / InputClip | 当前格式严格读取；Catalog/Blob 解码错误从 List/Get 返回，不再把损坏或存储故障静默显示成空列表 |

这里的“能读”不等于长期支持。版本未知、额外字段、身份不匹配或迁移后不能通过当前 validation 的 artifact
仍然被拒绝。精确路径、checksum、字段和 fixture 必须回查上述 package；本页不充当 parser 规范。

这些 pre-4.0 reader 是当前 owner profile 的开发期切换能力，不自动成为公开支持窗口。4.0.0 发布前可以在
备份副本上完成一次性升级、用当前代码重开验证，再删除不需要的旧 reader/fixture；4.0.0 发布时冻结公开
floor。此后 writer 产生的格式必须保留可测试的相邻 migration，只有明确的支持窗口和废弃策略允许时才能删除
reader。

## Release workflow

首次冻结某个产品版本时，先确认代码、生成合同和旧样本回放，再显式执行：

```powershell
task versions:compatibility:freeze
task nodes:compatibility:freeze
```

Freeze 使用 create-only 语义：内容相同可重复验证，已有路径内容不同会失败，不能覆盖历史发布事实。普通
`task check:full` 校验全部已冻结 floor；`task package` 还要求当前 `VERSION` 已有两类 snapshot。后续提升任何
writer/节点/TypeRef/CapabilityRef 时，应先让相应 reader 或引用迁移通过这些门禁，再发布新 floor。
