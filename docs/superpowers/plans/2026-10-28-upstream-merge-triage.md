# 上游合并评审：bestruirui/octopus → 本 fork

- 日期：2026-10-28
- 本仓库：`dev` @ `035e773`（2026-10-27）
- 上游：`bestruirui/octopus` `master` @ `8aa58b0`（2026-10-23）
- 共同祖先：`bf07971`（2026-03-18）
- 方法：逐提交读 diff → 对全部 106 个上游提交做 cherry-pick 试算 → 对候选提交实际应用并 `go build ./...`

---

## 0. 结论

上游领先 **106 个提交**，但只有 **约 12 项** 值得并入；其中 **3 项可直接 cherry-pick（已通过编译验证）**。

其余分为三类：

| 类别 | 数量（约） | 处理 |
|---|---|---|
| 本 fork 已有等价（常常更强）的实现 | ~40 | 不重复劳动 |
| 依赖上游三次架构重构，无法机械合并 | ~30 | 放弃或单独重写 |
| 纯版本号 / CI / 样式 / 文档 | ~33 | 忽略 |

上游最重要的三条"新特性"（故障转移、单渠道多 KEY、Images API 转发）本 fork 都已独立实现，其中多 KEY 与 Images 的实现还更完整。

---

## 1. 上游与分叉现状

| 项目 | 值 |
|---|---|
| 上游仓库 | https://github.com/bestruirui/octopus（go module 同名） |
| 上游分支 | **只有 `master`**（README 里写的 `upstream/dev` 已不存在） |
| 本仓库 remote | `upstream-temp/dev` 是过期引用，建议删除 |
| 本 fork 领先 | 282 提交 |
| 上游领先 | 106 提交 |
| 三点 diff | 267 文件，+16,747 / −32,680（上游净删 1.6 万行） |

提交类型分布（上游 106 个）：feat 31、fix 26、build 19、perf 9、refactor 6、style 5、docs 4、chore 4、revert 1、ci 1。

---

## 2. 三条架构断层（为什么不能整体 merge）

1. **`97d6a04` 协议转换层迁移到 `axonhub/llm`**（41 文件，−10,125 行）
   上游删掉了原生 transformer，改用 `github.com/looplj/axonhub/llm` 的类型体系。
   本 fork 的 `internal/transformer` 是 25.4k 行的自有演进（StreamEvent 管线、Anthropic patching、schema lossy、WS 透传…），合并即全毁。

2. **`0fa0aaf` 前端 Next.js → Vite**（89 文件）
   本 fork 仍是 Next.js 16 静态导出 + 自定义 SPA 路由。此后所有上游前端提交都按 Vite 目录/`use-intl`/`main.tsx` 写，不能直接摘。

3. **`ae295af` 大重构（146 文件）+ `77b37b2` 渠道模型拆表**
   上游把渠道模型拆成 `channel_models` / `channel_keys` / `channel_grants` 三张表，并把协议归属到"模型 × 凭据"组合。
   本 fork 仍是 `BaseUrls[]` + `Keys[]` + `Model/CustomModel` CSV + site 投影，`GroupItem` 直接引用 `channel_id`。合并等于重写渠道/分组/站点/导入全链路。

**附带一条**：上游 `af2787d` 曾整体 revert「单渠道多 URL 多 KEY」，现仅保留多 KEY。
而**多 URL + 选最快端点**正是本 fork 的核心能力（README Features）。反向合并会删功能，绝不可整体 merge。

---

## 3. 上游"新特性"里本 fork 已具备（不要重复劳动）

| 上游提交 | 上游特性 | 本 fork 对应实现 |
|---|---|---|
| `3abdcda` | 故障转移 | `internal/relay/balancer/`（Failover/RoundRobin/Weighted/Random + 熔断 + route learning） |
| `6e736b4` | 单渠道多 KEY | `model.Channel.Keys []ChannelKey` |
| `4211d6a` | OpenAI Images API | `internal/relay/images.go`（1007 行，1f23d0f，功能为超集）+ `bodycache` + 早心跳 |
| `44f63cb` 的客户端侧 | 客户端断连传播 | `relay/cancel.go`（cancel cause 体系） |
| `bbcce85` | 首字超时 | `relay/first_token_timeout.go` + `firstTokenBudget` |
| 心跳类 | SSE 早心跳 | `relay/heartbeat.go` |
| `baa5d65` 的 UA 部分 | UA 透传 | `relay.go:847-873`（缺失的是 `Accept` 头，见 §4-B） |
| `10f0204` | 流式 context canceled | 7a1352b 已独立修复同类问题（终态判定） |
| `bab1a53` | 日志显示来源 API Key | `model/log.go:60` `RequestAPIKeyName` + UI |
| `00e0739`/`a0a5c66` 的服务端部分 | 首字/耗时/缓存 token | `RelayLog.Ftut` / `UseTime` / `CacheReadTokens` / `CacheWriteTokens` |
| `d8a23d8`/`51f1035`/`da558bc` | 日志丢失/内存/并发修复 | fork 的 flush 队列 + 快照式清理设计天然免疫（逐条核过） |
| `dca5aa5` | 导入分批写入 | `op/backup.go:737/748/759` `CreateInBatches`（batch=20） |
| `dddc6bc`/`dd21e66`/`1cdb163`/`aca27ff` | 迁移修复 | fork 迁移版本号已重排，等价教训已具备（见 §7 例外） |
| `c21e1e1` | ParamOverride 修复 | 已实现且更严格（4 条转发路径 + ParamDrop + 测试） |
| `0538c3e` | 分组置顶 | fork 有自己的 Pinned/PinnedAt（#36），成员拖拽排序也已具备 |
| `e3af162` | gzip | `internal/server/server.go:44` 已启用 |
| `b7b053e` | pnpm workspace | `web/pnpm-workspace.yaml` 完全一致 |
| `9851442` | Gemini 剔除非法 schema 关键字 | fork 更严格且更正确（只剔 `propertyNames/uniqueItems/multipleOf`，其余是 Gemini 合法字段） |

---

## 4. 值得合并的

### A. 可直接 cherry-pick（已实际应用并 `go build ./...` 通过）

| 提交 | 内容 | 证据 | 价值 |
|---|---|---|---|
| **`ce585d8`** | 价格源抓取失败后回退到代理 | `internal/price/price.go:41-57` 仍是"一次直连、失败即返回"的修复前形态；已存在 `GetHTTPClientSystemProxy(true)` | **高**：国内部署访问 `models.dev` 失败 → 价格永远停在过期内置表 |
| **`8cf04fb`** | `octopus passwd <password>` 命令 | 新文件 `cmd/passwd.go`；`conf.Load(cfgFile)`/`db.InitDB(type,path,debug)`/`User.HashPassword()` 签名全部匹配 | **中**：管理员锁死时不用改库 |
| **`4c4c79b`** | `thinking` + `chat_template_kwargs` 字段 | 只加两个 `json.RawMessage` 到 `InternalLLMRequest` | **半条**：入站能收，出站是强类型结构，真正下发还需 ~10 行 + 设计决定（现有测试明确断言不下发 provider 私有字段） |

> 试算结果：106 个提交里只有 7 个零冲突，其中 4 个无价值（docs/easter egg/样式/空补丁），真正有用的就是上表三个。其余"冲突"多为 `main.go` 版本号单行冲突。

### B. 小改后合并（改动小、价值明确）

| 提交 | 内容 | 为什么值得 | 改动量 |
|---|---|---|---|
| `0771ca0` | Windows 自更新改为 临时目录 → 改名 → 复制 → 回滚 | `internal/update/core.go:38` 现在直接解压覆盖正在运行的 `.exe`，Windows 上必然失败 | ~60 行，自包含 |
| `d5a893f` | 全局模型过滤正则 | fork 只有渠道级/分组级 `match_regex`；挂在 `helper.FetchModels` 单点即可覆盖手动/定时/站点三条同步路径 | 后端 ~50 行 + 设置项 UI |
| `66b8f86` 的登录切片 | 登录页「信任设备」 | 顺手修 **86400 分钟/秒语义**：现在每次登录签发 **60 天** JWT（`auth.go:59` 按分钟解释） | ~15 行 + 3 个语言键 |
| `1ab744f` | 日志 SSE 建连即 flush + 心跳 | `handlers/log.go:272` 订阅后不写任何字节，空闲时 `EventSource` 一直 CONNECTING | ~10 行 |
| `a9f65c8` | 模型编辑弹层贴底翻转 | fork 是修复前基线（`model/Item.tsx:47` 只用 `rect.top`） | ~20 行 |
| `33d6a1f` | 自定义 API Key 值 | 迁移场景需要；**必须同时**加 `sk-octopus-` 前缀校验、唯一性和 `apiKeyIDMap` 的 Del/Set | 后端 ~20 行 |
| `276ec89` | 创建渠道失败弹窗 | 现在失败只写日志，界面无提示 | 1 行 |
| `27a29a3` 两个切片 | `LLMBatchCreate` 加 `OnConflict DoNothing`；模型同步加互斥 | `op/llm.go:91` 裸 `Create` + `task/sync.go` 无锁 + `handlers/channel.go:207` 可直接触发同步 → 并发重复插入会让整批失败 | ~15 行（其余不要） |
| `e674c17` | 价格抓取按 family / modality 过滤 | 第三方托管模型会套用宿主价格；内置预设已停在 2025-12-03 | ~30 行 |
| `baa5d65` 缺失部分 | chat/responses 适配器补 `Accept: text/event-stream` + `Cache-Control`/`Connection` | `images.go:652` 已正确，chat/responses 硬写 `application/json`，属于内部不一致 | ~30 行，集中在 `copyHeaders` |
| `8e93fa6` 的能力半 | attempts 显示所用渠道 KEY 名 | attempt 已有 `ChannelKeyID`，只差 name 与 UI 渲染 | 后端 ~10 行 + UI |
| `00e0739`/`a0a5c66` | 日志卡片显示 tokens/s 与缓存率 | 用现有 `output_tokens/(use_time−ftut)` 算，纯前端 | ~30 行 |
| `d7ca3df` | 首页统计导出 PNG（snapdom）+ 渠道名模糊 | 纯前端、自包含，分享/截图价值；注意 `use-intl`→`next-intl`、locale 路径不同 | ~1 天 |
| `1db150c` | 增加小米模型价格源 | 在 `price.Provider` 与 `scripts/updatePrice.py` 各加一项 | 数据改动 |

### C. 需要单独设计（不要当作 cherry-pick）

| 提交 | 内容 | 原因 |
|---|---|---|
| `bdc9948` | 子路径反向代理 | fork 的 API base 与 `assetPrefix` 已是相对路径，但 SW 仍注册在 `/sw.js` + `scope:'/'`、预缓存 `'/'` 全是根绝对路径；Next 需 `basePath` 方案 |
| `fd8b3b8` | 流式"启动事件"缓冲后再提交 | 需按 `StreamEventKind` 区分 startup/output 并回放，~200-250 行，还要和首字超时交互 |
| `10f0204` | 通用转换路径收到终态事件即停 | ~40 行，但 fork 只在两条透传路径做了终态判定 |
| `44f63cb` | 手动取消请求 | fork 没有上游的 `RequestState` 内存注册表（日志是完成后落库），停止按钮没有对象可挂 → 新子系统 ~300-400 行 |
| `b2e4fbd` | 渠道统计按模型明细 | 需要新的"渠道 × 模型"统计接口；fork 渠道列表只回 `StatsChannel` |
| `357787d` | 更多渠道模板预设 | fork 完全没有 preset 体系（`Form.tsx` 只编辑 base_urls），数据可参考，UI 需新建 |
| `0538c3e` | 分组成员上移/下移按钮 | 只是拖拽的无障碍替代，fork 已有拖拽 + 组置顶 |
| `e3af162` | 静态资源预压缩 | 运行时 gzip 已有，收益仅 CPU/带宽 |

### D. 明确不要

- `97d6a04`、`0fa0aaf`、`ae295af`、`77b37b2`：架构级，会毁掉 fork 的 transformer / 前端 / 渠道模型
- `af2787d`：会删掉多 URL 能力
- `9851442`：fork 的实现更正确，照搬会降低工具调用保真度
- `994d621`（fork 无 active item 机制）、`7d7244f`/`04a213e`（CI 命名改成 amd64 会打断 `getDownloadFilename` 自更新）
- 全部纯样式、版本号、docs 提交

---

## 5. 安全事项（优先于任何功能）

1. **CVE-2026-111943 / GO-2026-6809**（crypto/x509 中间证书 denylist）
   修复版本 `go1.26.10+` / `go1.27.3+`。本仓库 `go.mod` 是 **`go 1.25.0`**，而 1.25 已不在 Go 安全支持窗口内（当前支持 1.26/1.27），**拿不到该补丁**。
   → 升级 `go`/`toolchain` 指令并用新工具链重建发布物。上游 `8aa58b0` 本身只是依赖+版本号，无代码可摘。
2. **登录令牌有效期**：`GenerateJWTToken` 按**分钟**解释 `expire`，前端写死 `86400` → 每次登录 **60 天**。上游已改为秒（86400 = 1 天），并把"信任设备"映射到 -1（30 天）。
3. **`passwd` 命令**：密码走 argv（`ps`/history 可见），且不会吊销已签发 JWT（JWT secret 独立，见 `auth.go:21`）。
4. **自定义 API Key**：`middleware/auth.go:53` 会强制 `sk-octopus-` 前缀，且 `APIKey` 没有唯一索引，直接放行自定义值存在串号风险。
5. **全局模型过滤正则**：运算符可控的正则要用 `regexp2.MatchTimeout` 防 ReDoS。
6. **`sitesync` MySQL 大小写风险**：`site_models` 唯一索引在 MySQL 默认 `utf8mb4_*_ci` 下大小写不敏感，但 `sitesync/http.go:402-418` 去重是大小写敏感的 → 同一 provider 返回大小写变体模型名时，整个账号的同步事务会失败。可改用 case-fold 去重或 `OnConflict{DoNothing}`。

---

## 6. 验证记录

- 106 个上游提交全部做了 `git cherry-pick -n` 试算（`/tmp/octopus-probe` 独立 worktree，未触碰工作区）
- 零冲突：7 个；其余冲突文件多为 `main.go` 版本号
- 实际应用 + `go build ./...`：`ce585d8`、`8cf04fb`、`4c4c79b` **全部通过**（go1.25.0，`static/out` 打桩）
- 未执行：`go test ./...`、前端 `pnpm build`

---

## 7. 建议落地顺序

| 优先级 | 内容 |
|---|---|
| P0 安全 | Go toolchain 升级；登录 86400 语义 |
| P1 低风险高价值 | `ce585d8`、`8cf04fb`、`0771ca0`、`1ab744f`、`276ec89` |
| P2 功能 | `d5a893f`、`33d6a1f`（含加固）、`d7ca3df`、`a9f65c8`、`27a29a3` 切片 |
| P3 需设计 | `bdc9948`、`fd8b3b8`、`10f0204`、`e674c17` |

按 CONTRIBUTING「一个 PR 一个主题」，每项独立提交。

## 8. 仓库卫生

- README 的 `upstream/dev` 应改为 `upstream/master`（上游已无 dev 分支）
- 删除过期的 `upstream-temp/dev` 引用
- README 的「~180 commits ahead」实际已是 282

---

## 9. 落地记录（2026-10-28，分支 `upstream-picks`）

已合入 18 个提交，全部通过 `go vet ./...`、`go test ./...`（Go 1.26.10）与前端 `tsc --noEmit`、`next build`：

| 提交 | 对应上游 | 说明 |
|---|---|---|
| `fix(price): fall back to the configured proxy when price sync fails` | `ce585d8` | 抽出可测的抓取/解析函数，直连失败回退代理 |
| `feat(cli): add passwd command to reset the user password` | `8cf04fb` | 新增 `op.UserSetPassword`，支持 stdin 读取密码 |
| `fix(update): install the new binary through a temp dir and rename` | `0771ca0` | 临时目录 + 改名替换 + 失败回滚，带 `.old` 清理 |
| `fix(log): flush the log SSE stream on connect and keep it alive` | `1ab744f` | 首帧注释 + 15s 心跳 |
| `fix(channel): surface channel creation errors in the UI` | `276ec89` | 三语言 toast |
| `fix(model): serialize model syncs and tolerate concurrent price inserts` | `27a29a3` / `0296c70` | `TrySyncModelsTask` + `ON CONFLICT DO NOTHING`，手动同步返回 409 |
| `feat(model): add a global model filter for model syncing` | `d5a893f` | 设置项 + `helper.FetchModels` 单点过滤（AND、2s 超时） |
| `fix(transformer): request an SSE Accept header for streaming upstream calls` | `baa5d65` | chat/responses 流式 `Accept: text/event-stream` |
| `feat(price): only price text-output models from their own vendor` | `e674c17` | family 前缀 + 文本输出过滤，保留嵌入模型并固定遍历顺序 |
| `feat(apikey): allow setting the key value explicitly` | `33d6a1f` | 自定义 key + 前缀/长度/唯一性校验 + 索引重建 |
| `build: require go 1.26.10 for the crypto/x509 denylist fix` | `8aa58b0` | CVE-2026-111943 / GO-2026-6809 |
| `fix(auth): interpret the login expire value as seconds` | `66b8f86` 登录切片 | 86400=1 天、-1=30 天、上限 30 天；新增信任设备勾选 |
| `feat(price): sync prices for Xiaomi MiMo models` | `1db150c` | `xiaomi` / `mimo` |
| `feat(log): record the channel key name used by each attempt` | `8e93fa6` 能力半 | attempt 落库前补 Key 备注名 |
| `feat(log): show output speed and cache hit rate in the log card` | `a0a5c66` / `00e0739` | 纯前端推导 t/s 与缓存命中率 |
| `fix(web): flip the model edit overlay when it would overflow the viewport` | `a9f65c8` | 高度缓存 + 贴底翻转 |
| `feat(transformer): pass through provider thinking extensions on chat` | `4c4c79b` | `thinking` / `chat_template_kwargs` 原样透传 |
| `test: fix a printf verb flagged by the go 1.26 vet check` | — | 工具链升级后暴露的 vet 问题 |

**未做（有意）**

- `d7ca3df` 首页导出 PNG：需新增 `@zumer/snapdom` 依赖，价值中等，单独排期更合适。
- `bdc9948` 子路径部署：SW 仍是根作用域，需要 Next.js `basePath` 方案，属于设计类改动。
- `fd8b3b8` / `10f0204` / `44f63cb`：分别涉及流式启动事件缓冲（~200 行）、通用转换路径终态判定、请求注册表与手动取消，均需单独设计。
- `9851442`：本 fork 的 schema 处理已比上游更严格，不做回退。
- 其余样式/CI/版本号提交。

**验证方式**

- 每次改动均跑 `go vet ./...`、`go build ./...`、`go test ./...`（含新增单测）。
- 前端每次改动跑 `tsc --noEmit`、`eslint`，最后跑一次 `next build` 全量生产构建。
- 端到端：自定义 API Key 创建/认证/改值失效、全局模型过滤（假上游 4→2→1 个模型）、`passwd` 改密后旧密码 401 新密码 200、JWT 有效期 86400 秒=1 天 / -1=30 天。
