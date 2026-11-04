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
