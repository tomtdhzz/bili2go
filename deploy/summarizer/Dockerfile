# bili2go summarizer：digest.json -> summary.md 的 LLM/回退摘要服务。
# 纯 Python 标准库，无 pip 依赖。
FROM python:3.12-slim

WORKDIR /app
COPY summarize.py app.py ./

EXPOSE 8091
ENV PORT=8091 SUMMARY_TOP=25

# 健康检查走服务自身 /health
HEALTHCHECK --interval=10s --timeout=3s --retries=3 \
  CMD python3 -c "import urllib.request,sys; sys.exit(0 if urllib.request.urlopen('http://127.0.0.1:8091/health',timeout=2).status==200 else 1)"

CMD ["python3", "app.py"]
