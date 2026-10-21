# 安装与迁移

本插件 ID 为 **`cpa-codex-controls`**，版本 **1.4.2-timezone.14**。管理菜单保持「账号设置」。包内动态库、接口路径和配置节点都使用新 ID，与原 `cpa-key-billing` 区分。

## 首次安装

1. 选择与 CPA 服务器相同系统、架构的 ZIP。当前本地包提供 Linux amd64、Linux arm64、macOS arm64；Linux 构建面向 glibc 2.17+。
2. 停止 CPA，将 ZIP 根目录中的 `cpa-codex-controls.so`（Linux）或 `cpa-codex-controls.dylib`（macOS）放入 `plugins.dir`。容器部署时，使用容器可见的挂载目录。
3. 将以下内容合并到原 CPA 配置，再启动 CPA；已有的其他插件配置保留。

```yaml
plugins:
  enabled: true
  dir: plugins
  configs:
    cpa-codex-controls:
      enabled: true
```

默认并发上限为 0，不限制、不排队；请求时区覆盖默认关闭。注册后通过宿主管理菜单进入「账号设置」，独立页面路径是 `/v0/resource/plugins/cpa-codex-controls/ui`。

## 从插件商店安装

插件源码和安装包位于 `CLIProxyAPI/cpa-policy`；`CLIProxyAPI/Plugins-Store` 只维护商店索引，使用 `schema_version: 2` 的 direct 安装方式。

在宿主插件商店源管理中添加以下地址，或合并到配置文件：

```yaml
plugins:
  store-sources:
    - https://raw.githubusercontent.com/CLIProxyAPI/Plugins-Store/main/registry.json
```

从该源安装 `cpa-codex-controls`，根据宿主提示重启，确认注册版本。源和 Release ZIP 必须能从 CPA 服务器访问。当前版本提供 Linux amd64、Linux arm64、macOS arm64；不提供 Windows、macOS amd64 包。

安装资源发布在 `https://github.com/CLIProxyAPI/cpa-policy/releases/tag/v1.4.2-timezone.14`，三个平台 ZIP 内动态库均在根目录，另提供完整源码 ZIP 和 `checksums.txt`。

需要自行维护源时，复制商店仓库的 `registry.json`，将仓库、资源地址和署名改为自己的值；更新时同步修改版本、SHA256 和字节数，并保留许可证及原作者归属。

当前已验证的 .14 动态库仍保留构建时的仓库元数据占位值；这不是安装更新源，实际下载来源由商店索引决定。后续 GitHub Release 工作流会注入实际仓库；手工构建时可加 `-ldflags='-X cpa-codex-controls/internal/plugin.GitHubRepository=https://github.com/CLIProxyAPI/cpa-policy'`。

命令行安装：

```sh
CPA_PLUGIN_REPOSITORY=CLIProxyAPI/cpa-policy sh install.sh
```

安装脚本读取本仓库的 `releases/latest`。Windows 脚本使用同名环境变量，但本版本未发布 Windows 包。

`127.0.0.1:18766` 是本地功能演示，不是宿主插件商店入口。

## 从旧定制版迁移

新 ID 会被 CPA 视为独立插件，旧 ID 的设置不会自动继承。迁移前备份 CPA 配置、旧库和数据库。

1. 停止 CPA，将旧定制版 `cpa-key-billing` 的 `enabled` 设为 `false`。若移走旧库，将其放到插件扫描目录之外，保留以便回退。
2. 在 `plugins.configs.cpa-codex-controls` 下开启新插件，并复制旧定制版的 `custom_location`、`concurrency`、`credential_policies`；需要时复制 `debug` 和宿主的 `priority`。不要复制旧 `store`，它含原插件 ID 对应的来源和版本信息。
3. 如需保留 timezone.13 的错误事件和插件日志，将新配置的 `diagnostics_file` 显式设为旧诊断库的实际路径，例如 `plugins/cpa-key-billing-diagnostics.db`。确保旧定制版已停用。没有历史需求时不填，使用独立的新库。
4. 安装新库并启动，核对账号并发上限和时区开关。新浏览器存储命名空间会要求独立页面重新登录。

仅从原始计费数据库导入历史时使用 `state_file`，例如 `plugins/cpa-key-billing-state-v1.db`；它只读导入错误和日志，不复制计费功能。**不要把 timezone.13 的 diagnostics 数据库填入 `state_file`。** 新安装默认不导入旧数据。

原始计费插件可以使用原 ID 独立安装；若同时启用，两边的请求规则都会参与处理。迁移旧定制版时停用旧版，避免重复进行并发限制或时区覆盖。

需要回退时停止 CPA，停用新 ID，恢复备份的旧库和旧配置，再启动。迁移过程不删除或改名旧数据文件。

`127.0.0.1:18766` 是本地功能演示，不是宿主插件商店入口。
