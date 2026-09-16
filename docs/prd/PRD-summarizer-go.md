# bili2go — summarizer 单语言化（Go 重写）需求规格

> spec-driven「Requirements + Acceptance scenarios + 冻结契约」层。对应 requirement-orchestrator 迭代 6。
> 状态：规格冻结，待 execute。日期：2026-09-16。builds_on：迭代 4（analyze 链路）、迭代 5（WS-D 鉴权）。

## 1. 背景与目标

分析链路的摘要服务 `deploy/summarizer` 当前用 Python 标准库实现（`app.py` HTTP 边界 + `summarize.py`
五节模板/LLM 客户端）。它是**本仓唯一的非 Go 源码**（Go 33 文件 vs Python 3 文件/478 行），且**零第三方依赖**
——LLM 调用是裸 `urllib` 打 OpenAI 兼容端点，没有用到 Python 生态。

多语言并存抬高维护成本：CI 第二条 toolchain、Docker 带 Python 运行时、两套测试 runner、两套约定、
贡献者需懂两门。**目标**：把该服务直译为 Go，使**在仓源码单语言化**，同时**行为、HTTP 契约、鉴权、
离线回退逐字不变**。

**边界不拆（硬约束）**：摘要仍是一个独立、可容器化、可离线回退、可独立部署的服务——本迭代**只换实现语言**，
不把摘要塞回 Go 核心（那会污染 domain/app 的零依赖，且丢掉独立演进/离线回退，已在迭代 4 技术设计否决）。
`video-digest` 仍是宿主原生二进制（macOS 框架不可容器化），不在本迭代范围。

## 2. 需求（结果导向，可观测可测）

- **RG1 行为等价（parity）**：给定同一 `digest.json`，Go 服务的确定性回退五节 `markdown` 与现 Python 输出
  **逐字一致**（节标题、分段时间码、关键字表、gaps 展开、静音分支）。
- **RG2 HTTP 契约不变**：`GET /health → {"status":"ok","llm":<bool>}`；`POST /summarize[?top=N]`（body 为
  digest.json）成功 `200 {"markdown","model","fallback"[,"reason"]}`；digest 非法 JSON / 缺必需字段 / `top`
  非整数 → `400 {"error"}`；未知路径 → `404`。
- **RG3 鉴权不变（承接迭代 5 / WS-D）**：设 `SUMMARIZER_TOKEN` 时 `POST /summarize` 要
  `Authorization: Bearer <token>`（恒定时间比较），缺/错 → `401 + WWW-Authenticate: Bearer + {"error":"unauthorized"}`；
  未设 → 放行（向后兼容）；`GET /health` 永不鉴权。
- **RG4 LLM 路径不变**：配置 `OPENAI_BASE_URL + OPENAI_API_KEY` 时走 `/chat/completions`（`temperature:0`、
  同一 system prompt），失败回退确定性模板并置 `fallback:true` + `reason`；未配置直接回退。
- **RG5 单语言与可部署**：`deploy/summarizer` 不再含 `.py`；`docker compose up --build` 产出可用镜像
  （静态 Go 二进制）；`go build/vet/test ./...` 全绿；analyze 端 `HTTPSummarizer` 无需改动即可对接。

### 非目标

- 不改 `summary.md` 的五节结构/措辞（parity 就是冻结它）。
- 不把 `video-digest` Go 化或容器化（原生框架约束不变）。
- 不改 analyze 编排（`internal/analyze`）、下载/缓存/并发治理行为。
- 不引入任何 Go 第三方依赖或 LLM SDK。

## 3. 验收场景（Given/When/Then + 证据）

- **ACG1 → RG1**：Given `internal/summarizer/testdata/digest.fixture.json`；When Go `Summarize(d, 25)` 走回退；
  Then 输出 `markdown` 与现 Python `summarize.py` 对同一 fixture 的输出**字节一致**。
  **证据**：`go test` 内嵌 golden 对拍（基准由现 Python 生成后固化为 `testdata/summary.fixture.golden.md`）。
- **ACG2 → RG2**：Given 运行中的 Go 服务；When `curl /health`、`POST /summarize` 合法/非法 JSON/缺字段/`?top=abc`/未知路径；
  Then 分别得 `{status,llm}` / `200` / `400` / `400` / `400 top 必须为整数` / `404`。
  **证据**：`server_test.go` + 冒烟。
- **ACG3 → RG3**：Given `SUMMARIZER_TOKEN=s3cret`；Then 无/错 token `POST /summarize` → `401 + WWW-Authenticate`，
  对 token → `200`，`GET /health` → `200`；未设 token → `POST` `200`。
  **证据**：`server_test.go`（复刻迭代 5 `test_app_auth.py` 五例）+ 真实服务冒烟。
- **ACG4 → RG1（静音）**：Given `engine=none / has_audio=false` 的 `digest.silent.json`；Then 第②④节写「无音轨，无口述内容」，
  第③节仍给画面关键字，无编造口述，且与 Python 逐字一致。**证据**：golden 对拍（silent fixture）。
- **ACG5 → RG5**：Given `cd deploy && docker compose up -d --build`；Then 镜像内为静态二进制、`deploy/summarizer` 无 `.py`、
  容器 `/health` 健康。**证据**：`docker compose ps` + `curl` + `git ls-files 'deploy/**/*.py'` 为空。

## 4. 约束与前置

- **Go**：本机 go 1.24（module `bili2go`）。仅标准库。
- **Docker**：多阶段构建（`golang` 构建 → distroless 静态镜像）；compose build context = 仓根。
- **LLM（可选）**：`OPENAI_BASE_URL + OPENAI_API_KEY (+ OPENAI_MODEL，默认 gpt-4o-mini)`；不配则回退。
- **鉴权（可选）**：`SUMMARIZER_TOKEN`；不设则不鉴权（localhost/离线兼容）。

## 5. 冻结契约（下游依赖，不得漂移）

- HTTP 契约（RG2/RG3）与迭代 4/5 完全一致 —— `internal/analyze/HTTPSummarizer` 是消费方，字段/状态码/响应头
  任一漂移即回归。
- env 口径：`OPENAI_BASE_URL/OPENAI_API_KEY/OPENAI_MODEL/SUMMARY_TOP/SUMMARIZER_TOKEN/PORT`（默认值见 §4）。
- 五节 markdown 的逐字格式即 parity 基准（golden 文件），是 RG1 的可测锚点。
