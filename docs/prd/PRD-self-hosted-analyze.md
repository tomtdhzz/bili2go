# bili2go — 自托管分析服务需求规格

> spec-driven「Requirements + Acceptance scenarios + 冻结契约」层。对应 requirement-orchestrator 迭代 7。
> 状态：阶段 1（转写）+ 阶段 2（画面 OCR）已实现，本地端到端验证通过。日期：2026-09-17。builds_on：迭代 4（analyze 链路）、5（鉴权）、6（Go summarizer）。

## 1. 背景与目标

现状：`analyze`（下载→digest→摘要→summary.md）**只在 CLI**，且 digest 那步是 macOS 原生 `video-digest`
二进制——**只能跑在 Mac，不是网络服务**。因此无法「部署到任意机器 + 发请求即下载并总结」。

**目标**：让 bili2go 成为**可部署到任意 Linux/Docker 机器、请求驱动**的服务——`POST` 一个 bvid，服务端
自动下载、转写、摘要，返回 `summary.md`。用 **whisper**（可容器化）换掉 `video-digest`，且**仓库保持
单语言 Go**：digest 各步 exec CLI 二进制（`ffmpeg`/`whisper.cpp`/`tesseract`），二进制是运行时依赖
（与 `ffmpeg` 同性质），不是在仓源码。

**取舍（用户已确认）**：转写质量为 whisper 级，不及 Apple 原生；这是换可部署性付的代价。

**分阶段**（降低一次性风险）：
- **阶段 1（本迭代核心）**：whisper 转写 + HTTP `/api/analyze` 端点 + Docker 部署。`screen_keywords` 暂为空
  （摘要服务对空关键字已优雅降级）。交付「POST bvid → 口述内容总结」的可部署闭环。
- **阶段 2（画面 OCR，本次补上）**：**要解决的问题**——whisper 只捕获「口述」，但视频里大量关键信息**只在画面上、
  没被念出来**（幻灯片的数字/图表/标题/产品名，如「履约时效 P95=4.2h」「预算 240万」「退款率 1.7%」）；纯转写
  摘要会**漏掉**这些 on-screen-only 信息，第 ③④ 节空缺。用 `tesseract` OCR 抽帧识别画面文字，挑出**未口述**部分
  填入 `screen_keywords`，让摘要覆盖画面独有事实，并支持「口述+画面双重印证」。

## 2. 需求（结果导向，可观测可测）

- **RS1 请求驱动的分析端点**：`serve` 暴露 `POST /api/analyze?bvid=<BV|url>[&page&qn&codec]`，服务端完成
  下载→digest→摘要，返回 `200 {markdown, model, fallback, title, cid, chosenQuality, codec}`。
- **RS2 本地可容器化 digest**：新增 `LocalDigester`（实现既有 `Digester` 端口），用 `ffmpeg` 抽音频、`whisper.cpp`
  转写，产出与 `video-digest` **同 schema** 的 `digest.json`（`transcript.engine="whisper"`，segments 带
  `start_s/end_s/text`）。阶段 1 `screen_keywords=[]`。
- **RS3 单语言与不侵入**：仓库无新增非 Go 源码；不改 `domain`/`app`/下载/缓存/并发/摘要契约，只加一个
  `Digester` 适配器 + 一个 HTTP 端点。`go build/vet/test ./...` 全绿。
- **RS4 一键部署**：`docker compose up` 起两服务——`bili2go serve`（镜像含 `ffmpeg`+`whisper.cpp`+模型）与既有
  `summarizer`——单机可用，宿主 `POST /api/analyze` 跑通完整链路（Linux 容器，无 macOS 依赖）。
- **RS5 优雅降级与治理**：无音轨/whisper 失败 → `transcript.engine="none"`，摘要仍产出（②④写「无口述」），不 500；
  `/api/analyze` 为重操作，走既有 WS-A 限流（饱和 429）与客户端取消/超时。
- **RS6 画面未口述关键字（阶段 2）**：`LocalDigester` 抽帧 + `tesseract` OCR 产出 `screen_keywords[]`
  （`term`/`first_t_s`/`occurrences`/`score`/`evidence_frames`），并判定 `spoken_in_transcript`（画面文字是否也被
  口述，供第④节双重印证）与 `is_chrome`（跨大量帧常驻的界面装饰/水印 → 排除出结论）；`unspoken` 模式只留未口述项。
  `tesseract` 不可用或单帧失败时优雅跳过（`screen_keywords=[]`，退回阶段 1 行为）。质量为 tesseract 级。

### 非目标

- 不追求 OCR 画面关键字的 video-digest 同等质量（阶段 2 再做，且质量为 tesseract 级）。
- 不在仓库引入 Python 源码或 ML SDK（whisper/OCR 走 exec 二进制）。
- 不自训模型、不做说话人分离/字幕对齐等增强。
- 不改既有 CLI `analyze`（它仍可 `-digest-bin` 走 mac `video-digest`）。

## 3. 验收场景（Given/When/Then + 证据）

- **ACS1 → RS1/RS4**：Given `docker compose up` 起 bili2go+summarizer；When 宿主
  `curl -X POST 'http://127.0.0.1:8090/api/analyze?bvid=<BV>'`；Then `200`，body 含五节 `markdown` 与
  `title/cid`。**证据**：容器内端到端跑通日志 + 返回体。
- **ACS2 → RS2**：Given 一段样例音频/短视频；When `LocalDigester.Digest`；Then 产出 `digest.json` 满足摘要
  消费方 schema，`transcript.engine="whisper"`，`segments[].start_s` 单调、`text` 非空。**证据**：Go 测试对
  whisper JSON→digest 的映射断言（whisper 输出用 fixture 桩）。
- **ACS3 → RS3**：Given 本迭代改动；Then `git ls-files` 无新增 `*.py`/非 Go 源；`go build/vet/test ./...` 全绿；
  `internal/analyze` 既有测试不改仍绿。**证据**：命令输出。
- **ACS4 → RS5**：Given 无音轨视频 / whisper 不可用；Then `/api/analyze` 仍 `200`，`fallback` 合理，摘要含
  「无音轨，无口述内容」；饱和时新请求 `429`。**证据**：Go handler 测试（fake digester/limiter）。

## 4. 约束与前置

- **Go**：module `bili2go`，go 1.24，仅标准库。
- **运行时二进制（镜像内）**：`ffmpeg`（抽音频/抽帧）、`whisper.cpp`（转写）+ ggml 模型；阶段 2 `tesseract`(+chi_sim/eng)。
- **摘要服务**：既有 Go `summarizer`（迭代 6），经 `BILI_SUMMARIZER_URL`(+`BILI_SUMMARIZER_TOKEN`) 调用。
- **模型**：默认 `ggml-base`（可配 size/path）；中文可换 `small`。
- **高清下载**：仍需 `BILI_SESSDATA`（既有约束，与本迭代无关）。

## 5. 冻结契约（下游依赖）

- `digest.json` schema 与 `video-digest`/迭代 6 摘要消费方**逐字一致**（`LocalDigester` 必须满足，否则 summarizer 400）。
- `POST /api/analyze` 响应语义与 CLI `analyze` 一致，底层复用 `analyze.Analyzer`。
- `Digester` 端口签名不变：`Digest(ctx, videoPath, outDir) (DigestOutput, error)`——新适配器与 `ExecDigester` 平行。
