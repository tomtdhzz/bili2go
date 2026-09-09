# bili2go — 下载后分析（Analyze）需求规格

> spec-driven「Requirements + Acceptance scenarios + 冻结契约」层。对应 requirement-orchestrator。
> 状态：已实现并端到端验收通过。日期：2026-09-09。

## 1. 背景与目标

在既有下载能力之上，新增「下载后分析」链路：把下载到的视频离线拆成带时间戳的口述转写 +
画面未口述关键字，再由 LLM 产出人读的 `summary.md`。

参考 `video-digest`（Apple 原生 Swift 离线工具，AVFoundation/Speech/Vision）：确定性层只做抽取、
对齐、打分，不写自然语言结论；自然语言摘要由 AI 层在 `digest.json` 上生成，每条结论挂时间码。

**两条腿（硬约束）**：

- **(a) 本地分析**：直接 exec 宿主上的 `video-digest` 二进制做转写 + 画面关键字。它依赖 macOS 原生
  框架，**无法进 Docker**，只能在宿主运行。
- **(b) LLM 摘要**：把转写 + 关键字喂 LLM 产出 `summary.md`，这部分基础设施用 **Docker** 搭建
  （可容器化、可离线回退）。

**零第三方依赖权衡**：bili2go 核心（domain/app）保持零第三方依赖。分析链路作为外围能力落在独立包
`internal/analyze`，仅用 Go 标准库（`os/exec` + `net/http`）；LLM 推理下沉到 Docker 服务，不塞进核心。

## 2. 需求（结果导向，可观测可测）

- **RA1 分析编排**：给定一个视频（`-bvid` 下载或 `-video` 本地文件），系统产出 `<stem>.digest/summary.md`，
  由「下载 → video-digest → 摘要服务」链路自动完成。
- **RA2 确定性可离线**：摘要服务未配置 LLM 时走确定性模板回退，全链路离线可跑、可验收；配置 OpenAI 兼容
  端点时走 LLM。
- **RA3 结构与纪律**：`summary.md` 固定五节（①一句话结论 / ②分段摘要 / ③画面未口述关键字表 / ④口述+画面
  双重印证 / ⑤缺口与置信度）；每条结论挂时间码，`gaps` 原样呈现，无口述数据时不得用画面文字编造口述。
- **RA4 Docker 边界**：LLM 摘要服务在容器内运行并暴露 HTTP 契约；`video-digest` 留在宿主。bili2go 宿主进程
  同时 exec 宿主 `video-digest` 与 HTTP 调用容器摘要服务。

### 非目标

- 不把 `video-digest` 塞进 Docker（macOS 原生框架不可容器化）。
- 不在 bili2go 核心引入 LLM SDK / 第三方依赖。
- 不自建 LLM 推理（Docker 服务通过 OpenAI 兼容端点接入外部模型，或走确定性回退）。
- 不改动既有下载/缓存/并发治理行为。

## 3. 验收场景（Given/When/Then + 证据）

- **ACA1 → RA1/RA3**：Given 本地视频 `/tmp/vd_fixture.mp4`（39s，3 张幻灯片 + 中文旁白）与运行中的摘要服务；
  When `bili2go analyze -video <mp4> -digest-out <dir> -summarizer http://127.0.0.1:8091`；
  Then 退出码 0，`<dir>/summary.md` 存在且含五节标题，分段时间码 `[00:00]/[00:13]/[00:26]` 与 `digest.json`
  的 `segments[].start_s` 一致，第③节含 `退款率 1.7%`、`履约时效 P95…` 等未口述关键字并按 score 降序。
  **证据**：本轮实跑输出 `summary.md`（见 §验收记录）。
- **ACA2 → RA2**：Given 摘要服务未配置 `OPENAI_API_KEY`；Then `/health` 返回 `{"llm":false}`，
  `/summarize` 返回 `fallback:true` 的合规五节 markdown。**证据**：`curl /health` + 服务返回 `model=fallback`。
- **ACA3 → RA3（无音轨）**：Given `has_audio=false / engine=none` 的 digest；Then 第②④节写「无音轨，无口述内容」，
  第③节仍给画面关键字，无编造口述。**证据**：`deploy/summarizer/test_summarize.py::test_silent_video_no_fabricated_speech`。
- **ACA4 → RA4**：Given `cd deploy && docker compose up -d --build`；Then 容器 `summarizer` 健康，宿主 8091 可达，
  bili2go 宿主进程完成 exec+HTTP 两跳。**证据**：`docker compose ps` + ACA1 全链路跑通。

## 4. 约束与前置

- **video-digest**：需 macOS 26+ 与已构建的 `bin/video-digest`（见其 SKILL.md）。缺失时 `analyze` 报错退出 1。
- **Docker**：摘要服务需 docker 引擎；`docker compose up -d --build` 起服务。
- **LLM（可选）**：`OPENAI_BASE_URL` + `OPENAI_API_KEY`（+ `OPENAI_MODEL`）；不配则回退。
- **ffmpeg**：仅下载合并阶段需要（既有前置），分析阶段不依赖。

## 5. 验收记录（本轮实跑）

- 全链路：`bili2go analyze -video /tmp/vd_fixture.mp4 …` → 退出 0，产出 `summary.md`（五节齐全，时间码对齐）。
- 服务健康：`GET /health` → `{"status":"ok","llm":false}`。
- 回退合规：`model=fallback fallback=true`。
- 单测：`go test ./...` 全绿；`python3 -m unittest deploy/summarizer/test_summarize.py` 9/9 通过。
