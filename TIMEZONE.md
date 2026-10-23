# 并发与时区设置（1.4.4）

本版基于原计费插件二次开发，已按要求删除 API Key 管理、分析、请求事件、订阅计划、路由规则、模型定价及其功能实现。保留认证文件、AI 供应商、上游额度查询、并发限制、时区设置、错误事件和插件日志。插件 ID 改为 `cpa-codex-controls`。

## 单个账号设置

认证文件显示宿主返回的全部账号类型；AI 供应商读取 Codex、Claude、Gemini、Gemini Interactions、Vertex、xAI、Meta 和 OpenAI 兼容配置。旧版 CPA 没有某类配置接口时跳过该类。两个页面保留搜索和「仅启用」筛选，首次打开默认包含已停用项。

认证文件和 AI 供应商列表通过一列「并发」显示「当前 / 上限」，例如 `2/3` 表示当前 2 个请求、上限 3 个；上限为 0 时显示 `2/∞`，表示不限制。当前并发来自本 CPA 实例中插件记录的正在占用请求数，流式请求直至结束才释放；不汇总其他 CPA 实例。这两个页面可见时约每 3 秒更新一次，离开或切到后台时暂停。接口异常显示「—」，恢复后自动更新；修改上限并保存后立即刷新列表。

两类列表的「健康度」复用 CPA 原生 `auth-files` 和 `api-key-usage` 返回的 `recent_requests`：显示最近 20 个时间段的成功率、成功/失败次数和记录条，悬停、键盘聚焦或点按可看单段详情。时间标签保留 CPA 返回的时间，不猜测时区。打开页面或点击顶部刷新读取快照，不另外保存成功请求历史。原生接口缺失或未提供该凭证记录时显示「暂无数据」；无请求不显示为 100% 成功。API Key 健康度沿用 CPA 的供应商名称及 `base_url|api_key` 聚合口径，同一组合会共享宿主统计。

AI 供应商右侧增加启停开关：普通供应商仅增删 `excluded-models` 中的 `*`，保留其他排除规则；OpenAI 兼容供应商修改组级 `disabled`，同组全部密钥同步启停。提交前重新定位配置，只 PATCH 启停字段；定位不唯一时拒绝修改并提示刷新。

列表右侧的「设置」弹窗包含：

- **并发数量**：所有类型均可设置，0 表示不限制；正整数限制这一行上游凭据的同时进行请求数。供应商配置多条密钥时，每条密钥分别计数。
- **自定义时区位置**：左侧开关，右侧定位按钮。开启时显示国家代码、地区、城市、IANA 时区四个必填字段；关闭时收起字段并保留已填值。
- **定位按钮**：通过该行代理检测出口，成功后开启表单并自动填充四个字段。点击保存后生效；检测失败或取消不修改已保存配置。

时区编辑器显示在 Codex 账号及 Codex / OpenAI 兼容供应商中；是否实际改写取决于请求来源格式，只有 `codex` 和 `openai-response` 受支持。其他类型的设置仅显示并发，保存时保留已有位置配置。

时区规则是 **账号单独设置 > 全局配置**：

| 账号状态 | 全局开启 | 全局关闭 |
| --- | --- | --- |
| 未单独设置 / inherit | 使用全局位置 | 不修改 |
| 单独开启 / custom | 使用账号位置 | 使用账号位置 |
| 单独关闭 / preserve | 不修改 | 不修改 |

只修改并发数量不会改变时区继承关系。调整时区开关、位置字段或点击定位并保存后，形成账号单独设置。界面保持精简，不增加恢复继承按钮；需要恢复继承时可在配置中将该项的 `location_mode` 改为 `inherit`。

规则以宿主提供的 `auth_index` 保存，不包含明文凭据。替换供应商密钥/地址、认证文件改名等导致索引变化时，需要给新行重新设置。没有索引的行禁用设置按钮。

## 请求时区覆盖

在「设置」页面的「请求时区覆盖」中打开「启用时区覆盖」，填入国家代码、地区、城市、IANA 时区后保存。默认关闭，不会自动开启覆盖。国家代码采用两位大写字母，时区使用 `Asia/Tokyo`、`America/Los_Angeles` 等 IANA 名称。

关闭全局设置后，未单独配置的账号恢复原始请求；单独开启的账号继续使用自己的位置。本插件的日志和额度重置时间等显示跟随全局时区；账号时区仅用于该账号请求。

保存前调用插件校验接口，再使用 CPA 原有插件配置 PATCH 接口更新对应字段，并读取生效状态确认。其他配置不被整个覆盖。

```yaml
plugins:
  enabled: true
  dir: plugins
  configs:
    cpa-codex-controls:
      enabled: true
      priority: -100
      diagnostics_file: plugins/cpa-codex-controls-diagnostics.db
      custom_location:
        enabled: false
        country: ""
        region: ""
        city: ""
        timezone: ""
      concurrency:
        account_queue_capacity: 0
        queue_timeout_seconds: 30
      credential_policies: {}
```

账号配置示例（索引须替换为 CPA 实际返回值）：

```yaml
credential_policies:
  "0123456789abcdef":
    max_concurrency: 2
    location_mode: custom # inherit / custom / preserve
    location:
      country: JP
      region: Tokyo
      city: Tokyo
      timezone: Asia/Tokyo
```

全局开启示例：

```yaml
custom_location:
  enabled: true
  country: US
  region: California
  city: Los Angeles
  timezone: America/Los_Angeles
```

## 自动检测位置

全局定位按钮使用 CPA 的全局代理或直连；账号按钮使用其 `proxy_url` / `proxy-url`。空代理继承全局代理；显式 `direct` / `none` 直连。检测不读取浏览器定位，不改变网络代理。

检测通过指定出口访问 `api.ipify.org` / `api6.ipify.org` 获取 IPv4 / IPv6，再通过同一代理访问 `ipwho.is/<已检测IP>` 查询该地址的地区。双栈时区冲突会报错；只有一个 IP 版本可用时使用可用结果。检测请求不携带供应商密钥，结果可以手动修正。

按钮填充的是检测时刻的出口位置，不会在模型请求期间访问定位服务，也没有后台自动更新。代理出口变化后需重新检测。CPA 未向拦截器暴露最终的实际代理 URL，其他插件或自定义 executor 临时切换代理时，保存位置不能代表该临时出口。

本实现参考 `codex-proxy-rs` 的具体出口 IP 定位和双栈冲突处理；没有复制其后台自动位置模式，也没有使用其直连查询后回退代理的策略。

## 请求覆盖范围

只处理 CPA 来源格式为 `codex` 或 `openai-response` 的请求，流式和非流式规则相同：

| 请求位置 | 修改内容 |
| --- | --- |
| user 消息的独立 `environment_context` | 直属 `timezone`、`current_date` |
| developer 消息的独立 `codex_apps_client_time_context` | 直属 `timezone`、`current_date` |
| `web_search` / `web_search_*` 工具 | `user_location` 的国家、地区、城市、时区 |

日期按照目标时区的当前日期生成。识别规则参考 `codex-proxy-rs`：存在 `content_item_kinds` 时须匹配对应上下文分类，只替换完整的独立 XML 上下文。普通聊天文本、工具输出、嵌套示例、无效 XML 不替换；不会补造不存在的上下文字段。

普通 Chat Completions、Claude、Gemini 来源请求不做时区改写。WebSocket 和绕过标准拦截链的请求未经端到端验证。

时区数据库内嵌于 Go 库；界面使用浏览器 IANA 数据库。修改时区不改变已有诊断记录的绝对时间戳。

## 并发行为

设置页的「并发策略」包含：

- **账号队列容量**：0～1000，初始值 0（不排队）。限额满时，允许这个数量的额外请求等待；等待不占运行名额，也不发送上游请求。同一实例所有账号总等待数另有 10000 的保护上限。
- **排队超时（秒）**：1～120，初始值 30。从首次入队开始计时，重新检查名额和配置更新不会延长已有等待期限。

账号只配置自己的并发数量，默认为 0（不限制）。没有全局并发上限或并发继承选项。0 直接放行，不检查并发上限、不进入等待队列；仍记录当前运行数用于列表展示。只有单独填写 1～100000 的正整数后，才执行限额和队列逻辑。

兼容旧配置：`default_max_concurrency` 仅允许读取但不再生效；旧 `concurrency_mode: inherit` 归一化为上限 0，`custom` 或未带模式的账号保留原有数值。返回配置和新保存的数据不再包含这两个旧字段。时区的继承规则不变。

账号上限覆盖经过 CPA 标准 after-auth 拦截器的请求格式，与时区开关无关。运行数和队列都属于当前实例，不跨进程共享；在宿主已选定的账号上等待，不自动更换其他账号，也不重新引入路由规则。

- 不排队时，超限返回 HTTP 429、`upstream_concurrency_limit` 和 `Retry-After: 1`。
- 队列已满返回 HTTP 429 / `concurrency_queue_full`，超时返回 HTTP 429 / `concurrency_queue_timeout`，两者同样提供 `Retry-After: 1`。
- 每个账号按入队顺序等待，名额保留至 `request.complete`（包括流式请求）；重试切换凭据时转移名额，同一请求重复钩子不会重复占用。
- 客户端取消会通过宿主完成事件唤醒等待调用并回收位置；超时、取消都不会再执行上游请求。队列只在当前拦截调用内等待，不创建后台工作线程，调用结束时停止本次超时计时器。
- 修改上限会唤醒队列并重新判断容量，设为 0 会立即放行全部等待请求；已排队请求保留首次等待期限，缩小容量不驱逐已有等待者，新增请求遵守新容量。关闭插件会释放等待者并停止执行插件限制。
- 管理接口 `/credential-policies` 返回配置、`active` 运行数、`waiting` 等待数和全局 `concurrency`；列表仍显示「当前运行数/实际生效上限」。

本版未恢复下游 API Key 并发管理，因此没有对应的「密钥队列」设置；也没有参考项目的专用自动审批请求通道，因此不提供「自动审批独立并发」占位选项。已删除的订阅、计费限制不再执行。

## 错误事件与插件日志

两项功能独立于已删除的计费模块。成功请求不会写入请求事件或用量表；只有 `usage.handle` 报告的上游失败写入错误记录，保留时间、供应商、模型、认证索引、状态码、错误类型、错误正文、延迟和 TTFT。不从原始响应猜测失败详情或关联并发请求。

诊断数据库默认为 `plugins/cpa-codex-controls-diagnostics.db`，可以通过 `diagnostics_file` 修改。记录保留 30 天；日志支持按等级筛选、搜索已加载内容、分页和清空，错误支持分页和展开详情。调试日志通过 `debug: true` 开启。

旧 `state_file` 仅用于首次只读导入错误事件和插件日志，默认空值，不自动读取其他插件的数据。需要从原计费库导入时显式指定旧文件，例如 `plugins/cpa-key-billing-state-v1.db`。导入在新库事务内完成，不修改旧库，重启不重复导入；清空新日志也不会使旧日志重新出现。旧结构不兼容则回滚并报告错误。只有诊断记录被复制，旧密钥、价格、计划、路由和成功请求留在原始归档中。新旧数据库必须使用不同文件。

从 timezone.13 迁移时可在停用旧定制版后，将新插件的 `diagnostics_file` 显式设为原诊断库路径以保留记录；不要将该诊断库填入 `state_file`。迁移步骤见 [INSTALL.md](INSTALL.md)。`routing`、`proxy_locations`、`codex_fast_mode_billing`、`mask_api_key_view_emails` 和 `allow_api_key_quota_reset` 等旧字段允许留在 YAML 中，但不再生效，其管理接口已经删除。

## CPA 接口限制

所有修改都在本插件内完成，没有修改 CPA 主程序或宿主管理前端。账号按钮在本插件的认证文件和供应商列表内。

请求在 `request.intercept_after` 中按已选凭据处理。全局覆盖无需身份；存在账号规则但宿主未提供已选凭据索引时返回 503，避免错用规则。配置 `priority` 仍由宿主管理，本分支不自动修改。

CPA 在同一阶段按插件优先级从大到小调用拦截器，例如 `100 → 10 → 0 → -100`。建议配置 `priority: -100`，前提是需要先执行的其他插件优先级都大于 -100；相同优先级按插件 ID 排序，不能据此保证本插件最后执行。该配置同时影响本插件在这个阶段的时区覆盖和并发检查。

优先级只控制调用顺序，不扩大覆盖范围。后续插件、协议转换、payload 规则或 executor 仍可能再次修改请求。不能强制控制其他插件的最终输出，也不能改变它们的页面时区、宿主日志时区、系统时间或出口 IP。其他插件发起的模型请求是否受本插件处理，取决于它们是否经过 CPA 的同一请求拦截链；直接调用 HTTP 上游的请求不因这个优先级而被拦截。

已在 CPA 7.3.7 和 `cpa-plugin-codex-candy-eval` 0.4.2（源码 `4f83676`）上用假上游验证糖果测试请求：该插件通过 `host.model.execute_stream` 发起请求，CPA 只跳过调用方自身的拦截器，本插件仍会执行。它写入的 `environment_context` 中 `Etc/UTC` 和 UTC 日期可按本插件规则覆盖；账号设置优先于全局，账号明确关闭则保留原值。测试运行数会计入本插件并在结束后释放。分别使用优先级 -100 和 100 均通过，因此无需为这个只注册管理接口的测试插件专门调整相对优先级。此结论不代表其他插件的独立 HTTP 调用也会经过该链路。

## 构建、安装与验证

需要支持插件 schema 6 的 CPA，测试使用 7.3.7；不支持 `no-plugin` 构建。插件 ID 为 `cpa-codex-controls`，需要在同名 `plugins.configs` 节点配置。每个 ID 只能有一份可加载库。从旧定制版迁移时先停用旧版，再复制有效配置到新 ID；不要复制旧 `store` 来源或版本固定值。原库与原数据保留。

当前提供 Linux amd64、Linux arm64 和 macOS arm64 构建。Linux 使用 glibc 2.17 目标，不是 musl/Alpine 原生构建。上游商店安装脚本不包含这个定制分支。

```sh
CGO_ENABLED=1 go build -buildvcs=false -trimpath -ldflags='-s -w' \
  -tags cshared -buildmode=c-shared \
  -o dist/cpa-codex-controls.so ./cmd/cpa-codex-controls
# macOS 使用 .dylib 扩展名

gofmt -l .
go vet ./...
go test -race ./...
go mod tidy -diff
npm ci --prefix scripts
npm test --prefix scripts
node scripts/format_ui.mjs --check
python3 scripts/e2e_proxy_location.py --cpa /path/to/cli-proxy-api --plugin /path/to/cpa-codex-controls.so
```

本地端到端脚本使用真实 CPA、两个本地代理和假上游，验证默认透传、账号/全局优先级及开关、检测配置、流式/非流式、并发 429/释放、重启恢复、已删除接口 404、独立错误记录和旧诊断历史只读导入。UI 演示和端到端测试仅使用测试凭据。

## 参考版本与授权

- 原插件：`haowang02/cpa-plugin-key-billing`，`4e405bd1d953cdc477cdbdd0d4890bec3b58abc3`。
- 位置参考：`zyycn/codex-proxy-rs`，`60570883007febebe732597a9881397098d724f5`。
- CPA 接口参考：`router-for-me/CLIProxyAPI`，`ae3ee3be303fb07fb8173279923413005bf32177`。

授权、参考文件和修改说明见 `NOTICE`、`LICENSE` 与 `licenses/`。
