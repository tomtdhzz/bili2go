# bili2go — 产品需求文档 (PRD)

> 输出 1/2 · 领域 + 需求规格。对应 requirement-orchestrator 的 spec-driven「Requirements + Acceptance scenarios」层。
> 状态：待评审（评审通过后进入 execute 实现）。日期：2026-09-03。

## 1. 背景与目标

参考项目 `you2php`（PHP）实现了 YouTube 视频代理下载：抓 watch 页 HTML、逆向 JS 签名得到直链、服务端 socket 透传字节。
本项目 **用 Go 重写下载能力，目标平台改为哔哩哔哩（Bilibili）**，下载策略借鉴 `Bilibili-Evolved`
（`registry/lib/components/video/download/apis/{url,dash,flv}.ts`、`components/video/video-quality.ts`）。

与 YouTube 的本质差异（已调研确认）：
- Bilibili **无需逆向 JS 签名**，直接调 JSON API `playurl` 拿真实流地址。
- 需先把 `bvid → aid + cid`（多一次 `x/web-interface/view` 请求）。
- DASH 格式下 **音频、视频流分离**，必须分别下载后用 ffmpeg 合并为单个 mp4。
- CDN 强制校验请求头：`Referer: https://www.bilibili.com` + 浏览器 `User-Agent`，否则 403；720P/1080P 需 `SESSDATA` cookie。

## 2. 领域划分（Domain）

| 子域 | 职责 |
|---|---|
| 视频元数据解析 | `bvid`/URL → `aid`、`cid`、分 P 列表、标题、时长 |
| 播放地址解析 | 调 `playurl(fnval=4048)`，解析 `dash.video[]/audio[]`，按 `qn` 选流 |
| 清晰度/编码策略 | qn 码表、fnval 位掩码、编码(AVC/HEVC/AV1)优选与回退 |
| 认证 | 统一注入 Referer / UA / `SESSDATA` cookie |
| 流下载与合并 | 并发下载选中的 video+audio 流，ffmpeg `-c copy` 合并为 mp4 |
| 对外服务 | HTTP API + CLI 两种触发方式 |

## 3. 需求（Requirements，结果导向，可观测可测）

- **R1 清晰度枚举**：给定一个 B 站视频（`bvid` 或视频页 URL），系统能返回该视频可下载的清晰度列表与当前编码。
- **R2 DASH 下载合并**：用户指定清晰度，系统下载对应 DASH 视频流 + 音频流，合并为一个可正常播放的 mp4。
- **R3 认证分级**：未配置 `SESSDATA` 时可下到免登录清晰度（≤480P，qn≤32）；配置后可下到 720P/1080P（qn 64/80）。请求高于当前可用清晰度时自动降级并说明。
- **R4 多 P 支持**：支持多 P 视频，可通过分 P 序号（page）或 `cid` 指定具体分 P。
- **R5 编码策略**：优先按请求的编码（默认 AVC/H.264）选流；该清晰度下无此编码时，按配置回退到其它编码并记录。
- **R6 双触发方式**：可通过 HTTP 服务与 CLI 两种方式触发下载。

### 非目标（Non-goals，显式排除以界定边界）

- 4K/HDR/杜比视界/8K（qn ≥ 112，需大会员）—— **优先级低，本期不做**（预留 qn 码表与 fnval，接口不阻断，但不保证产出）。
- FLV / `durl` 格式下载（本期只做 DASH）。
- 番剧 / PGC（`pgc/player/web/playurl`）。
- 弹幕、字幕、封面、AI 字幕等周边下载。
- B 站登录流程 / 扫码 / 刷新 cookie —— `SESSDATA` 由用户以配置形式提供。
- 前端页面美化 / 完整复刻 you2php 的 UI 主题。
- wbi 签名端点（`x/player/wbi/playurl`）—— 仅当实测普通端点 403 时才升级（见技术方案「待验证项」）。

## 4. 验收场景（Acceptance Scenarios，Given/When/Then + 证据）

> 每条场景追溯到需求，并指明可观测证据。这些将成为实现阶段每个任务的 `acceptance` 与端到端 `final_verification`。

- **AC1 → R1**：Given 一个有效 `bvid`；When 调 `GET /api/info?bvid=<id>`；Then 返回 JSON 含 `qualities[]`（qn + 名称）、`current_qn`、`codec`、`aid`、`cid`。
  证据：真实 bvid 的响应 JSON。
- **AC2 → R2**：Given `bvid` + `qn`；When 触发下载；Then 产出 mp4，`ffprobe` 显示同时含 1 路 video + 1 路 audio 流，且时长 ≈ 视频原时长（误差 < 1s）。
  证据：`ffprobe -show_streams` 输出 + 文件大小/时长。
- **AC3 → R3**：Given 未配置 `SESSDATA` 且请求 `qn=80`；Then 系统降级到实际可用清晰度并在响应/日志中说明；配置 `SESSDATA` 后同一请求能拿到 qn=80。
  证据：两次运行的日志/响应对比。
- **AC4 → R4**：Given 一个多 P `bvid` + `page=2`；Then 下载的是第 2 P（`cid` 与 `view` 返回的 `pages[1].cid` 一致）。
  证据：cid 对照。
- **AC5 → R5**：Given 某 qn 下不存在 AVC 编码；Then 按 fallback 编码选流并在日志记录所选 `codecid`。
  证据：日志中所选编码。
- **AC6 → R6**：Given 同一 `bvid`+`qn`；Then CLI `bili2go dl -bvid <id> -qn 32 -o out.mp4` 与 `GET /download?bvid=<id>&qn=32` 均能产出可播放 mp4。
  证据：两条命令的运行输出与产物。

## 5. 约束与前置

- 语言：Go（本机 go 1.24.0 已确认）。
- **ffmpeg 为运行前置**：本机当前未安装 —— 实现阶段需 `brew install ffmpeg`，否则 AC2 无法验证（已记入台账 gaps）。
- 高清晰度需用户提供 `SESSDATA`（环境变量 / 配置文件）。
- 验证：可用真实视频地址跑端到端脚本（用户可提供一个测试视频 URL）。

## 6. 需用户确认的点

- 4K+ 明确降级为非目标（已按用户指示：优先级低）。✔ 已确认
- HTTP + CLI 均要（R6）。若只需其一，可缩减 T5/AC6。
- 测试视频 URL：实现到验证阶段时需要你提供一个（最好含多 P，用于 AC4）。
