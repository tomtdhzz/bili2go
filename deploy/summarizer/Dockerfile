# bili2go summarizer：digest.json -> summary.md 的 LLM/回退摘要服务（Go）。
# 多阶段：golang 构建静态二进制 → distroless 静态镜像（无 Python、无 shell）。
# 构建上下文为仓根（见 docker-compose.yml：context: ..），Go 源在 cmd/ + internal/。
FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/summarizer ./cmd/summarizer

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/summarizer /summarizer
EXPOSE 8091
ENV PORT=8091 SUMMARY_TOP=25

# 健康检查走二进制自身 health 子命令（distroless 无 shell）
HEALTHCHECK --interval=10s --timeout=3s --retries=3 CMD ["/summarizer", "health"]

ENTRYPOINT ["/summarizer"]
