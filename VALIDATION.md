# 首版验证记录

验证日期：2026-11-04（Asia/Singapore）。环境：Apple Silicon、macOS 27.2、Go 1.27.3、Swift 6.4.2、Chrome 156.0.8078.81。

## 已完成

| 范围 | 结果与证据 |
| --- | --- |
| 原生构建 | `scripts/build.sh` 成功；生成约 19.4 MiB 的 `dist/DSL Q.app`。`codesign --verify --deep --strict` 通过。 |
| Go 核心 | `go test -race ./...` 通过。覆盖 Range/单路/未知长度/空文件、Cookie、重定向、源文件变化、错误 Range、忽略 Range 后降级、截断连接、503 重试、队列、限速及取消限速后立即恢复。 |
| 并发 | 160 MiB 测试文件实际达到 32 路；运行中降到 2 路后收敛，最终 SHA-256 一致。非法并发值被核心拒绝。 |
| 崩溃恢复 | 对打包后的 Go 进程执行 SIGKILL，再启动同一数据库；已保存的 2 个分段恢复，64 MiB 文件续传后的 SHA-256 一致。见 `.cache/core-process-report.json`。 |
| 持久化与文件 | 加密数据库未出现测试 URL 令牌/Cookie 明文；保存同名文件不覆盖原文件；任务取消与重复接管保持幂等；覆盖准备尚未结束就撤销的竞态。 |
| 原生界面 | 实机添加并完成 80 MiB 下载，文件哈希一致；检查详情、32 路选项、设置快捷键、即时防休眠开关、后台下载、窗口重新打开及正常退出。 |
| 钥匙串 | 在正式应用数据目录首次启动，创建钥匙串密钥；保存默认设置后退出，再启动成功读取加密设置并连接核心。测试下载历史未导入正式配置。 |
| 电源控制 | 状态与失败恢复检查通过；通过 `pmset -g assertions` 验证真实 IOKit 断言创建/释放。实机下载和窗口关闭后保持断言，完成后释放。 |
| Chrome | 真实浏览器通过 Native Messaging 握手、自动接管、Cookie 下载、SHA-256、小文件与 POST 留在浏览器、手动发送、排除站点、交接中断恢复、冷启动客户端及防休眠验证。见 `.cache/chrome-report.json`。 |
| Chrome 中断恢复 | 通过 CDP 停止扩展 Service Worker，观察 stopped → running；原下载已取消时只提交一个核心任务，尚未取消时撤销接管并恢复浏览器下载。 |
| 扩展检查 | TypeScript 检查、构建和 3 组规则/关联/恢复测试通过。 |

完整本地检查入口：`./scripts/check.sh`。构建及检查日志在 `.cache/build.log`、`.cache/check.log`。

## 验证边界

- 浏览器自动化使用独立配置和本地 HTTP 服务，测试用 manifest 仅预授权 `127.0.0.1`；交付 manifest 保持可选网站权限。未向日常 Chrome 配置安装扩展，首次加载及网站授权由使用者完成。
- 手动发送通过扩展弹窗实测，它与右键菜单共用下载入口；系统右键菜单的点击链路未单独自动化验证。
- 电源验证检查了本应用的真实断言，没有强制整机睡眠、合盖或进行整夜下载实验。深色使用系统原生适配，未切换整机外观做单独人工验收。
- macOS 14 是编译部署目标，本次只在当前 macOS 27.2 / Apple Silicon 设备运行，未验证旧系统或 Intel。
- 应用为本地 ad-hoc 签名，未公证、上架或公开发布。

## Chrome 联调复现

需在项目虚拟环境安装 Python Playwright；使用本机 Chrome，无需下载 Chromium。以下测试写入独立 `.cache/bc.*` 配置，不操作日常浏览器资料。

```sh
.cache/venv/bin/python scripts/browser_smoke.py
.cache/venv/bin/python scripts/core_smoke.py
```

加入 `--cold-start` 可验证 Native Messaging 唤起原生客户端；执行前应关闭已有 DSL Q，测试将打开使用独立数据库的应用实例。它用合成密钥测试，不读取真实钥匙串密钥。

用户将范围调整为仅 Chrome 后，已移除 Edge 界面入口和注册配置，并删除本次下载的 Edge 测试程序、安装包及独立测试配置；`/Applications` 中没有安装 Edge。

## 0.1.1：扩展目录选择修复

- 安装入口将包内资源导出到 `~/Downloads/DSL Q Chrome 扩展`，并提供复制路径按钮；Chrome 不再需要进入 `.app` 内部。
- 新增导出检查覆盖普通目录类型、文件完整性、嵌套资源、重复更新、扩展身份保留以及同名用户文件夹保护。
- `scripts/build.sh`、`scripts/check.sh` 与签名检查通过。导出的实际文件逐项比对，与包内扩展一致。
- 在用户的 Chrome 目录选择器中定位到导出文件夹，确认“选择”按钮可用。
- `/Applications/DSL Q.app` 已更新到 0.1.1；旧应用保留在项目 `.cache/installed-backups` 中。

## Chrome 接管修复与默认类型扩充（0.1.2）

验证日期：2026-11-04（Asia/Singapore）。应用版本 0.1.2，扩展版本 0.1.1，扩展 ID 不变。

- 修复前使用原构建和独立 Chrome 配置：正常响应通过；仅将响应延迟 5 秒即无法接管，核心没有产生任务。记录见 `.cache/plan-baseline-report.json`、`.cache/plan-slow-response-report.json`。
- 修复后真实 Chrome 156.0.8078.81 联调通过 15 项检查，记录见 `.cache/capture-fixed-report.json`、`.cache/capture-chrome.log`。面板式接口包含 Cookie/Referer、无后缀 URL、5 秒响应延迟、未知文件长度和 `.TAR.GZ` 响应文件名，验证核心只产生一个任务、Chrome 原下载取消、最终 SHA-256 正确。
- 1 KiB PDF、UTF-8 编码文件名、重定向均成功接管；认证无法重放时恢复 Chrome，并显示认证失败原因；未匹配的小文件、POST、排除站点留在浏览器。手动下载和 Service Worker 交接中断恢复通过。
- 浏览器测试改用 Chrome 原生文件名确定流程。Playwright 默认的 `allowAndName` 会强制 UUID 文件名并绕过扩展文件名事件，不能代表日常 Chrome 的小文件接管行为。独立小文件证据见 `.cache/capture-small-report.json`；可用 `--small-only` 复现，`--trace-handoff` 输出隔离测试中的阶段信息。
- 扩展 12 组测试覆盖晚到元数据、处理期间新事件、GET/POST 歧义、跨来源认证头隔离、交接恢复、诊断脱敏及 Go/Swift/扩展默认列表一致性。默认类型由 9 种增至 90 种。
- Go 竞态测试通过，新增持久化迁移验证：旧默认升级且写回加密存储，自定义列表和空列表不改变；自动接管开关、阈值、排除站点保持原值。
- 原生构建、现有电源/扩展导出检查和应用签名检查通过。最终构建与检查日志见 `.cache/capture-build.log`、`.cache/capture-check.log`。

真实站点边界：使用日常 Chrome 访问用户截图中的面板文件页时，页面返回 HTTP 502，无法触发该站实际下载。因此已验证的是具有同类特征的隔离面板用例，尚不能声称截图中的原站案例已经验收，也未将其故障归因于单一已确认根因。

本机交付：已更新 `/Applications/DSL Q.app` 到 0.1.2，导出目录扩展更新到 0.1.1，并在日常 Chrome 中实际重新加载。弹窗确认已连接、网站已授权、自动接管已开启。原应用与扩展保存在 `.cache/installed-backups/capture-fix-_grj3k8_`；安装记录见 `.cache/capture-install.json`。先验证包含额外 `tar.gz` 的 10 项自定义列表未被迁移覆盖；用户随后明确选择合并，已通过设置界面补充并保存为 91 项，Chrome 弹窗再次确认 91 项规则、20 MiB 阈值及自动接管开启。原生设置初始文案也已修正，避免未检测就错误显示“尚未安装连接组件”。最终文案构建日志见 `.cache/capture-swift-final.log`。
