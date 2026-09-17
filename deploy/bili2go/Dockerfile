# bili2go serve 镜像：自托管「请求→下载→转写→摘要」服务。
# 分析链路需系统级 ffmpeg + whisper.cpp（转写），故用 debian-slim 而非 distroless。
# whisper 模型不进镜像（体积大）——用 compose 挂 ./models 卷，WHISPER_MODEL 指向挂载路径。
#
# 构建上下文为仓根（见 docker-compose.yml：context: ..）。

# --- 1. 构建 bili2go ---
FROM golang:1.24 AS gobuild
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bili2go ./cmd/bili2go

# --- 2. 静态编译 whisper.cpp 的 whisper-cli（-DBUILD_SHARED_LIBS=OFF 免拷 .so）---
FROM debian:bookworm-slim AS whisper
RUN apt-get update && apt-get install -y --no-install-recommends \
      git build-essential cmake ca-certificates && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 https://github.com/ggerganov/whisper.cpp /w
# GGML_NATIVE=OFF 避免容器内 -march=native 误判；ARM 显式带 fp16 修 ggml 在 gcc-12 下的
# vfmaq_f16 "target specific option mismatch" 编译错。
RUN cmake -S /w -B /w/build -DBUILD_SHARED_LIBS=OFF -DGGML_NATIVE=OFF \
      -DGGML_CPU_ARM_ARCH=armv8.2-a+fp16 \
 && cmake --build /w/build -j --target whisper-cli
FROM debian:bookworm-slim
# ffmpeg（音视频/抽帧）+ whisper 运行时（libgomp）+ tesseract（画面 OCR，中英语言包）
RUN apt-get update && apt-get install -y --no-install-recommends \
      ffmpeg libgomp1 ca-certificates \
      tesseract-ocr tesseract-ocr-eng tesseract-ocr-chi-sim \
      && rm -rf /var/lib/apt/lists/*
COPY --from=gobuild /out/bili2go /usr/local/bin/bili2go
COPY --from=whisper /w/build/bin/whisper-cli /usr/local/bin/whisper-cli
ENV WHISPER_MODEL=/models/ggml-small.bin \
    BILI_SUMMARIZER_URL=http://summarizer:8091
EXPOSE 8090
ENTRYPOINT ["bili2go"]
CMD ["serve", "-addr", ":8090"]
