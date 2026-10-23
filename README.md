# CPA Policy

源码仓库为 [CLIProxyAPI/cpa-policy](https://github.com/CLIProxyAPI/cpa-policy)，商店索引单独维护在 [CLIProxyAPI/Plugins-Store](https://github.com/CLIProxyAPI/Plugins-Store)。

基于 `haowang02/cpa-plugin-key-billing` 1.4.2 二次开发，当前版本 **1.4.6**。插件标识改为 **`cpa-codex-controls`**，与原计费插件独立；管理页面名称继续为「账号设置」。

本版已删除 **API Key 管理及自助页面、分析、请求事件、订阅计划、路由规则、模型定价**，包括对应接口、计费/额度扣减、价格同步、调度器、存储代码和前端模块。不会再按插件原有订阅、价格或 API Key 规则拒绝请求。CPA 自身的访问密钥鉴权和上游供应商凭据仍由宿主管理。

保留的功能：

- **认证文件 / AI 供应商**：显示 CPA 返回的全部认证类型和可用供应商列表，保留搜索、启用筛选、原生请求健康度、实时额度/余额查询，以及每行右侧「设置」按钮。供应商支持启停；OpenAI 兼容供应商按组启停。
- **并发策略**：账号单独设置并发上限，默认为 0，不限制也不排队；正数上限使用全局账号队列容量和排队超时。队列容量 0 表示超限直接拒绝。流式请求占用名额直至完成，一个供应商的多个上游密钥分别限制。
- **时区覆盖**：默认关闭；账号单独设置优先于全局。账号单独关闭时跳过全局覆盖。支持通过该账号代理检测出口位置并填充国家、地区、城市和时区。
- **风控中心**：默认关闭；支持本地关键词/正则、Moderations 兼容审核 API、模型范围和分类阈值。提供仅观察/前置拦截、账号单独策略、草稿测试与脱敏记录，详见 [CONTENT-AUDIT.md](CONTENT-AUDIT.md)。
- **错误事件、插件日志**：独立诊断数据库，分页浏览错误、日志等级筛选和清空日志。错误详情来自 CPA 的 `usage.handle`。
- **管理页面**：管理密钥登录、中英文、主题、隐私遮罩及移动端布局。

## 使用与安装

在 CPA 插件商店中添加以下源即可安装当前版本：

```text
https://raw.githubusercontent.com/CLIProxyAPI/Plugins-Store/main/registry.json
```

安装包和源码归档发布在本仓库的 [Releases](https://github.com/CLIProxyAPI/cpa-policy/releases)。详细配置、覆盖范围和限制见 [TIMEZONE.md](TIMEZONE.md)，安装和迁移见 [INSTALL.md](INSTALL.md)。内容审计目前处理用户文本；请求正文隐私替换、自动封禁和通知尚未加入。

需要支持 schema 6 的插件版 CPA，已用于本地验证的版本是 7.3.7。构建要求 Go 1.24+ 和 C 编译器：

```sh
CGO_ENABLED=1 go build -buildvcs=false -trimpath -ldflags='-s -w -X cpa-codex-controls/internal/plugin.GitHubRepository=https://github.com/CLIProxyAPI/cpa-policy' \
  -tags cshared -buildmode=c-shared \
  -o dist/cpa-codex-controls.so ./cmd/cpa-codex-controls
```

macOS 将输出扩展名改为 `.dylib`。新安装：停止 CPA，把对应平台库放入 `plugins.dir`，在 `plugins.configs.cpa-codex-controls` 下设置 `enabled: true`，再启动 CPA。每个插件目录只保留一份此 ID 的可加载库。自建商店源或从旧定制版迁移见 [INSTALL.md](INSTALL.md)。

安装脚本使用 `CPA_PLUGIN_REPOSITORY=CLIProxyAPI/cpa-policy sh install.sh`，读取本仓库的 Release ZIP 和 `checksums.txt`。发布包已注入本仓库地址；商店下载来源由索引中的资源地址决定。原作者链接只用于归属说明。

## 旧数据

旧 `state_file` 仅作为原计费数据库诊断历史的只读导入来源，默认空值，不自动读取其他插件的数据。需要导入时显式指定原文件，例如 `plugins/cpa-key-billing-state-v1.db`。只导入错误记录和插件日志到新的 `diagnostics_file`，默认 `plugins/cpa-codex-controls-diagnostics.db`；重启或重复配置不会重复导入。旧库内容保持不变，密钥、计划、价格、路由和成功请求不会复制到新库。旧诊断结构不兼容时导入回滚并报错。

诊断记录按 30 天保留；过期记录不在新页面显示，旧库仍是完整原始归档。清空新库日志不会修改旧库，也不会在下次启动重新导入已清空日志。新旧库不能使用相同文件。

旧 `routing`、`proxy_locations` 及已知计费配置字段仍可解析，但不再生效。旧定制版的 `custom_location`、`concurrency`、`credential_policies` 需要复制到新 ID 配置下；不会自动读取旧 ID 的配置。

## 本地验证

```sh
gofmt -l .
go vet ./...
go test -race ./...
go mod tidy -diff
npm ci --prefix scripts
npm test --prefix scripts
node scripts/format_ui.mjs --check
python3 scripts/frontend_dummy_backend.py --port 18766
# 另一个终端：使用本地 CPA 和编译后的库，全部请求发往假上游
python3 scripts/e2e_proxy_location.py --cpa /path/to/cli-proxy-api --plugin dist/cpa-codex-controls.so
```

前端演示使用 `demo-management-key`，数据仅保存在内存中。开发目录说明见 [AGENTS.md](AGENTS.md)；授权和参考实现归属见 [NOTICE](NOTICE) 与 `licenses/`。
