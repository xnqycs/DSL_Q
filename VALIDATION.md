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


## 下载确认、独立窗口与生命周期通知（0.2.0）

验证日期：2026-11-04（Asia/Singapore）。应用 0.2.0，扩展 0.1.2，macOS 14 最低版本不变。

- 核心增加只读 `tasks.preview`、幂等 `tasks.confirm` 和取消操作。浏览器提交后持久化为 `awaiting_confirmation`，确认前不分配目标目录或临时文件。取消待确认项隐藏记录但保留交接凭据，崩溃恢复不会重建下载。
- Go 竞态测试覆盖预览无副作用、确认前无下载、重启后继续等待、错误目录可修改后重试、重名保护、重复确认、取消清理、旧版空临时文件兼容、旧设置默认通知开启及运行标识。日志：`.cache/dialog-check-final.log`。
- Swift 通知检查覆盖排队、极小/零字节快速完成、历史记录基线、暂停继续、手动重试、取消、授权允许/拒绝与重复请求；两种通知开关关闭期间的事件不会在重新开启后补发。
- 真实 Chrome 联调确认了交接完成后等待用户、确认后只下载一份、取消确认后 Chrome 与核心均停止；保留上一版 Cookie、延迟响应、编码文件名、重定向和 Service Worker 恢复回归。记录：`.cache/confirmation-final-report.json`、`.cache/dialog-chrome-final.log`；最终 18 项检查还覆盖独立测试实例冷启动后仅等待确认、不开始下载或持有防休眠断言。
- 独立测试应用使用不同 bundle ID、加密数据库和 `.cache/dialog-ui.pmsajw4f` 文件目录。实机操作验证批量逐项确认、真实目录选择器、文件改名、单任务并发、独立窗口、暂停继续、关闭后继续、双击重新打开、取消清理、确认窗关闭不创建任务、预览失败和下载失败反馈。
- 关闭主窗口后，通过同一浏览器 IPC 入口提交接管，确认窗口自动出现；点击开始后正常下载。三个完成文件（64 MiB、32 MiB、2 MiB）SHA-256 均正确，取消任务没有输出文件或临时分片。
- 系统通知实际投递并通过 `UNUserNotificationCenter.deliveredNotifications()` 读回：4 次开始、2 次完成、1 次取消、1 次失败；暂停继续没有重复开始通知。测试应用的通知权限已恢复为原先关闭状态。记录：`.cache/dialog-ui-report.json`、`.cache/dialog-ui.pmsajw4f/app.log`。
- 实机发现并修正 macOS 自动将窗口合并为标签页的行为，下载窗口明确禁用自动标签。未知大小任务在暂停或失败后显示静止进度；窗口最小高度保证操作区可见。

验证边界：完成文件的内容一致性使用隔离文件验证，未重新传输生产面板中的完整大文件。通知中心卡片的实际点击未自动化验证（当前电脑工具不支持 Fn 修饰键），点击任务跳转已实现。macOS 14 是部署目标，本次实机为 macOS 27.2。

本机交付：`/Applications/DSL Q.app` 已更新到 0.2.0，日常 Chrome 扩展已重新加载为 0.1.2。原有完成记录、91 项接管规则、并发设置和防休眠设置均保留；开始/结束通知默认开启，系统通知已授权。备份位于 `.cache/installed-backups/dialogs-b5hxu08r`，安装收据为 `.cache/dialog-install.json`。

实际面板补验：收尾时原面板已恢复可访问。通过截图中的同一右键下载入口触发 7.56 GB 压缩包，DSL Q 自动弹出确认窗口，正确显示文件名、大小、类型、续传能力和保存目录。点击取消后待确认项消失，资料库仍只有原先的完成记录；Chrome 下载记录明确显示这次下载已取消。本次没有点击“开始下载”，没有重下整个压缩包。


## Chrome“另存为”抢先弹出修复（0.2.1）

验证日期：2026-11-05（Asia/Singapore）。应用资源版本 0.2.1，扩展 0.1.3。

- 开启 Chrome“下载前询问每个文件的保存位置”，将 Native Messaging 准备中的探测延迟 5 秒。修复前，扩展在 Chrome 下载仍为 `in_progress` / 暂停时就调用文件名回调，允许保存对话框出现；已稳定复现失败，见 `.cache/prompt-before.log`。
- 扩展现在持有文件名回调，直到 Chrome 原任务取消或明确放弃接管。晚到的元数据有短暂等待机会；每个回调只释放一次。准备超过 13 秒时恢复浏览器并撤销迟到的准备结果，避免先出现 Chrome 保存窗口、之后又出现 DSL Q 确认。这个期限留在 [Chromium 的 15 秒文件名等待上限](https://chromium.googlesource.com/chromium/src/+/refs/heads/main/chrome/browser/extensions/api/downloads/downloads_api.cc)以内，不更改浏览器全局保存偏好。
- 扩展 18 项测试和 TypeScript 检查通过，包括慢准备、事件乱序、认证失败恢复、超时后禁止再次接管、非 GET 和缺失元数据回退。原生构建、包签名检查通过：`.cache/prompt-build.log`。
- 真实有界面 Chrome 156.0.8078.81，独立配置且询问保存位置开启：8 项检查通过。覆盖 5 秒探测、1 KiB PDF、手动发送仅进入 DSL Q 确认，以及认证失败、规则不匹配、14 秒探测超时保留 Chrome 且不产生迟到任务。测试只观测文件名回调释放时的真实 Chrome 状态，不修改保存对话框行为。记录：`.cache/prompt-final-report.json`、`.cache/prompt-final.log`。
- 原有真实 Chrome 回归 17 项通过，含 Cookie/Referer、5 秒响应、未知长度、编码文件名、重定向、取消确认、SHA-256、POST、排除站点与两种 Service Worker 中断恢复：`.cache/prompt-regression-report.json`。本次只修改扩展接管时序，Go 与原生功能测试沿用上一节的通过记录；未重复执行 Go 竞态测试。
- 日常 Chrome 已重新加载扩展 0.1.3，连接、授权及 91 项规则正常。“下载前询问每个文件的保存位置”经界面核实仍开启。通过原面板同一右键下载入口复测 7.56 GB 压缩包，没有出现 Chrome 保存对话框，扩展显示“已交给 DSL Q”，资料库新增等待确认项。原来已有一个确认窗口，因此新项按队列等待；只取消本次新增的测试项，保留原待确认项和原完成文件，没有开始重新传输整个压缩包。
- 为保留用户当时打开的确认窗口，本机仅更新包内扩展、导出的扩展及版本资源并重新签名，没有退出应用。备份：`.cache/installed-backups/save-prompt-djkg494e`；收据：`.cache/prompt-install.json`。新安装包仍由完整构建产生。

复现保存窗口专项：`.cache/venv/bin/python scripts/browser_smoke.py --headed --prompt-check`。成功接管只显示 DSL Q 确认；未匹配、认证失败或准备超时仍由 Chrome 处理并遵循其保存偏好，不保证所有网站/所有失败场景都没有浏览器提示。

## 关闭主窗口后隐藏 Dock（2026-11-05）

- 主窗口关闭通知只绑定 SwiftUI 主窗口，不替换其 delegate。关闭后切换为 `.accessory`；菜单、设置、应用重开和通知回退共用主窗口打开入口，恢复 `.regular`。异步关闭回调带有代次校验，防止快速重开后再次隐藏 Dock。
- `scripts/check.sh` 通过：Go 竞态检查、扩展类型检查及 18 项测试、Swift 构建、电源/导出/通知检查。最终改动另通过 Swift 构建、完整 `scripts/build.sh` 和应用签名验证。日志：`.cache/dock-check.log`、`.cache/dock-swift-final.log`、`.cache/dock-build.log`。
- 从当前 `AppDelegate` 源码提取的一次性 AppKit 检查通过：可见窗口复用、关闭隐藏、辅助窗口打开/关闭不改变策略、辅助窗口仍可见时重开、连续 5 次快速关闭重开、最小化与恢复、最后窗口关闭后保持运行。记录：`.cache/dock-lifecycle.log`；该检查使用替身退出模型，不代表下载核心测试。
- 隔离应用包、数据库和本地 HTTP 文件实机验证了鼠标 ×、⌘W、下载确认/进度窗口、设置入口恢复及键盘输入、访达重开、最小化和 ⌘Q 正常退出。主窗口关闭期间下载继续，32 MiB 文件最终 SHA-256 一致。报告：`.cache/dock-ui-report.json`。事件诊断只加入临时测试副本，没有进入交付构建。
- 验证边界：运行环境为 Apple Silicon / macOS 27.2，macOS 14 仅通过部署目标编译。Dock 显示状态通过系统 `NSRunningApplication.activationPolicy` 核对；当前界面工具未提供 Dock 和状态栏菜单的可用界面，因此未直接验收菜单栏图标点选、菜单栏退出及通知中心卡片点击。其入口代码已核对，共用的主窗口恢复路径已实测。未替换 `/Applications/DSL Q.app`，未修改日常浏览器配置。

## 后台空闲 CPU 修复（2026-11-06）

- 修复前，安装版 0.2.2 的 6 个任务均已完成；25 秒观测中原生进程消耗 4.16 秒 CPU 时间，平均约 16.6%，核心约 0.6%。调用栈反复出现 `NSStatusItem` / `MenuBarExtra` 重绘。单独运行原核心、使用空数据库，3.1 秒收到 12 份完全相同的快照；同值 `@Published` 赋值实验也确认会重复触发更新通知。
- RPC 改为保留最新待发送快照，在输出端去重；相同创建时间的任务按 ID 稳定排序。原生任务、设置和防休眠状态只在值变化时发布。首次空快照仍建立通知基线，确认窗口、通知和电源同步保持执行；下载调度及进度刷新周期仍为 250 毫秒，协议字段和数据库格式不变。
- `scripts/check.sh` 通过：Go 竞态检查、扩展类型检查与 18 项测试、Swift 构建、电源/扩展导出/通知检查，以及新增快照检查。新增覆盖相同快照去重、分段和设置变化、慢消费者接收最终完成状态、写失败退出、稳定排序、快照独立副本、首次空列表与历史通知基线。Swift 检查沿用独立可执行程序方式，无需本机 Command Line Tools 未附带的 XCTest。日志：`.cache/idle-cpu-check.log`。
- `scripts/build.sh` 与应用签名验证通过，macOS 14 部署目标不变。打包核心空闲 3.1 秒只发送 1 份初始快照，无重复发送；空闲时修改设置正常送达。记录：`.cache/idle-cpu-build.log`、`.cache/idle-cpu-core-report.json`。
- 隔离发布应用使用独立 bundle ID、数据库和合成密钥，预置 6 个已完成任务。关闭主窗口、确认 `.accessory` 后连续测量 60.08 秒：原生进程累计 CPU 时间未增加到 `ps` 的 0.01 秒精度，观测平均 0.0%，核心平均约 0.083%。随后 5 秒采样的 457 次主线程栈全部处于系统事件等待，未出现菜单栏重绘热点。记录：`.cache/idle-cpu-performance.json`、`.cache/idle-cpu-sample.txt`。前后使用不同数据库，不将该结果视为所有任务规模或系统版本的性能保证。
- 实机隔离验证：主窗口关闭后浏览器 IPC 弹出确认，开始下载保持 `.accessory`；暂停释放防休眠断言，继续重新获取；设置快捷键重开主窗口并恢复 `.regular`。关闭所有窗口后 32 MiB 下载完成，SHA-256 为 `1cbd22e11bc209926b1e050d644779ba4105d7a023109c3b78bb35edf5c7c292`，完成后释放断言，再测 10 秒原生 CPU 仍为观测 0.0%。重开显示最终完成状态，⌘Q 后应用与核心均退出。记录：`.cache/idle-cpu-ui-report.json`、`.cache/idle-cpu-background-report.json`、`.cache/idle-cpu-after-download.json`。
- 真实 Chrome 156.0.8078.81、独立配置联调 17 项通过，覆盖握手、接管与确认、Cookie、慢响应、重定向、小文档、SHA-256、取消、失败回退及 Service Worker 重启恢复。日志：`.cache/idle-cpu-chrome.log`。

验证边界：仅在 Apple Silicon / macOS 27.2 运行；当前电脑工具只暴露应用窗口，SystemUIServer 读取超时，因此未直接点击状态栏图标验证菜单重开，共用的主窗口恢复路径已经实测。本次未替换已安装的 `/Applications/DSL Q.app`，未修改日常 Chrome 配置，未发布版本。
