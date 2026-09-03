#!/usr/bin/env bash
# 端到端验证：info → dl → ffprobe 断言 video+audio 双流。
# 用法: scripts/verify.sh [bvid|url]   （默认单P示例）
set -euo pipefail

BVID="${1:-BV1NKNv69EZz}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/bili2go"
OUT="$(mktemp -t bili2go-verify-XXXX).mp4"

echo "== build =="
(cd "$ROOT" && go build -o "$BIN" ./cmd/bili2go)

echo "== info =="
"$BIN" info -bvid "$BVID"

echo "== download -> $OUT =="
"$BIN" dl -bvid "$BVID" -qn 80 -o "$OUT"

echo "== ffprobe 断言双流 =="
STREAMS=$(ffprobe -v error -show_entries stream=codec_type -of csv=p=0 "$OUT" | sort | tr '\n' ',')
echo "streams: $STREAMS"
case "$STREAMS" in
  *audio*video* | *video*audio*)
    echo "PASS: 同时含 video 与 audio" ;;
  *)
    echo "FAIL: 缺少 video 或 audio 流"; rm -f "$OUT"; exit 1 ;;
esac

DUR=$(ffprobe -v error -show_entries format=duration -of default=nk=1:nw=1 "$OUT")
echo "duration: ${DUR}s"
SIZE=$(wc -c < "$OUT")
echo "size: ${SIZE} bytes"
rm -f "$OUT"
echo "OK"
