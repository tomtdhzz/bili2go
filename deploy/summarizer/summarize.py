"""digest.json -> summary.md (五节结构).

两条路径：
- LLM：配置 OPENAI_BASE_URL + OPENAI_API_KEY 时，把 digest 压成简报喂给 OpenAI 兼容
  的 /v1/chat/completions，产出自然语言五节摘要。
- 回退：未配置或调用失败时，用确定性模板直接从 digest 字段拼装五节结构（全部挂时间码，
  不编造结论），保证离线可跑、可验收。

仅依赖 Python 标准库（http、json、urllib）。纪律来自 video-digest SKILL.md「AI 摘要流程」。
"""
from __future__ import annotations

import json
import os
import urllib.error
import urllib.request

REQUIRED_KEYS = ("source", "transcript", "screen_keywords", "gaps")

SYSTEM_PROMPT = """你是视频摘要助手。输入是 video-digest 的确定性简报（口述转写分段 + 画面未口述关键字 + 缺口）。
产出固定五节的 Markdown，节标题必须逐字为：
## ① 一句话结论
## ② 分段摘要
## ③ 画面未口述关键字表
## ④ 口述与画面双重印证的要点
## ⑤ 缺口与置信度
纪律（违反视为错误）：
- 每条结论必须引用证据：口述结论引用分段的 [mm:ss]；画面结论引用关键字的首次出现 [mm:ss] 与证据帧。
- 分段摘要每条以 [mm:ss] 开头，时间码直接来自分段 start，不得四舍五入到别的段。
- 无口述数据（转写引擎为 none 或无分段）时，第②④节写「无口述内容」，禁止用画面文字反推「他说了…」。
- 第③节按 score 降序把画面未口述关键字逐条落表（term | 首次出现 | 出现帧数 | 证据帧）。
- 第⑤节把 gaps 原样展开，一条不漏；gaps 为空则明确写「无缺口」，不得改写成「基本没问题」。
- 数字只能来自简报，不得自行估算或编造。只输出 Markdown，不要额外说明。"""


def clock(s) -> str:
    s = int(float(s))
    return f"{s // 60:02d}:{s % 60:02d}"


def validate(d: dict) -> None:
    missing = [k for k in REQUIRED_KEYS if k not in d]
    if missing:
        raise ValueError(f"digest 缺少必需字段: {missing}")


def _partition(kws: list) -> tuple[list, list, list]:
    unspoken = [k for k in kws if not k.get("spoken_in_transcript") and not k.get("is_chrome")]
    spoken = [k for k in kws if k.get("spoken_in_transcript") and not k.get("is_chrome")]
    chrome = [k for k in kws if k.get("is_chrome")]
    unspoken.sort(key=lambda k: k.get("score", 0), reverse=True)
    return unspoken, spoken, chrome


def build_brief(d: dict, top: int = 25) -> str:
    """把 digest 压成 LLM 可直接读的简报（对齐 scripts/brief.py 口径）。"""
    src, run, tr = d["source"], d.get("run", {}), d["transcript"]
    unspoken, spoken, chrome = _partition(d["screen_keywords"])
    mode = run.get("params", {}).get("keywords", "?")
    lines = [
        f"# {src.get('path', '(unknown)')}",
        f"时长 {clock(src['duration_s'])} · {src.get('width')}x{src.get('height')} · "
        f"音轨 {'有' if src.get('has_audio') else '无'} · 关键帧 {len(d.get('frames', []))} · "
        f"keywords 模式 {mode}",
        f"转写 {tr.get('engine')}/{tr.get('locale')} · {len(tr.get('segments', []))} 段",
        f"gaps: {d['gaps'] if d['gaps'] else '(无)'}",
        "",
        "## 转写分段",
    ]
    for s in tr.get("segments", []):
        lines.append(f"[{clock(s['start_s'])}] {s['text']}")
    if not tr.get("segments"):
        lines.append("(无分段)")
    lines.append(f"\n## 画面未口述关键字（{len(unspoken)} 条，取前 {min(top, len(unspoken))}）")
    for k in unspoken[:top]:
        ev = ["frames/f%05d.jpg" % i for i in k.get("evidence_frames", [])[:3]]
        lines.append(
            f"[{clock(k['first_t_s'])}] {k['term']}  score={k.get('score')} "
            f"frames={k.get('occurrences')} ev={ev}"
        )
    if spoken:
        lines.append(f"\n## 口述+画面双重印证（{len(spoken)} 条）")
        for k in spoken[:top]:
            lines.append(f"[{clock(k['first_t_s'])}] {k['term']}  frames={k.get('occurrences')}")
    if chrome:
        lines.append("\n## 已排除的常驻文本（不进结论）")
        lines.append("、".join(k["term"] for k in chrome[:top]))
    return "\n".join(lines)


def fallback_summary(d: dict, top: int = 25) -> str:
    """确定性模板：直接从 digest 字段拼装五节结构，全部挂时间码，不编造结论。"""
    src, tr = d["source"], d["transcript"]
    segs = tr.get("segments", [])
    has_spoken = tr.get("engine") not in (None, "", "none") and bool(segs)
    unspoken, spoken, chrome = _partition(d["screen_keywords"])
    dur = clock(src["duration_s"])
    out = []

    # ① 一句话结论
    out.append("## ① 一句话结论")
    if has_spoken:
        gist = segs[0]["text"].strip()
        out.append(f"全片时长 {dur}，口述开场：{gist}（完整脉络见第②节，画面独有信息见第③节）。")
    elif unspoken:
        out.append(
            f"全片时长 {dur}，无口述数据；画面关键信息以「{unspoken[0]['term']}」"
            f"（[{clock(unspoken[0]['first_t_s'])}]）为首，详见第③节。"
        )
    else:
        out.append(f"全片时长 {dur}，无口述数据、无画面未口述关键字，可总结素材有限。")

    # ② 分段摘要
    out.append("\n## ② 分段摘要")
    if has_spoken:
        for s in segs:
            out.append(f"- [{clock(s['start_s'])}] {s['text'].strip()}")
    else:
        out.append("无音轨，无口述内容。")

    # ③ 画面未口述关键字表
    out.append("\n## ③ 画面未口述关键字表")
    if unspoken:
        out.append("| term | 首次出现 | 出现帧数 | 证据帧 |")
        out.append("|---|---|---|---|")
        for k in unspoken[:top]:
            ev = "、".join("frames/f%05d.jpg" % i for i in k.get("evidence_frames", [])[:3]) or "(无)"
            out.append(f"| {k['term']} | [{clock(k['first_t_s'])}] | {k.get('occurrences')} | {ev} |")
    else:
        out.append("（无画面未口述关键字：画面文字或已被口述，或被判为界面装饰。）")

    # ④ 口述与画面双重印证的要点
    out.append("\n## ④ 口述与画面双重印证的要点")
    if not has_spoken:
        out.append("无口述数据，无法双重印证。")
    elif spoken:
        for k in spoken[:top]:
            out.append(f"- [{clock(k['first_t_s'])}] {k['term']}（口述与画面均出现，证据帧 {k.get('occurrences')} 帧）")
    else:
        out.append("本次以 `--keywords unspoken` 生成，已口述项未收录；如需本节请用 `--keywords all` 重跑。")

    # ⑤ 缺口与置信度
    out.append("\n## ⑤ 缺口与置信度")
    gaps = d.get("gaps", [])
    if gaps:
        for g in gaps:
            out.append(f"- {g}")
    else:
        out.append("本次运行无缺口（gaps 为空）。")
    if chrome:
        out.append(
            f"\n> 另有 {len(chrome)} 条常驻文本/界面装饰已按 is_chrome 排除，不进结论："
            + "、".join(k["term"] for k in chrome[:top])
        )
    return "\n".join(out) + "\n"


def _call_llm(brief: str, base_url: str, api_key: str, model: str, timeout: float = 60.0) -> str:
    url = base_url.rstrip("/") + "/chat/completions"
    payload = {
        "model": model,
        "temperature": 0,
        "messages": [
            {"role": "system", "content": SYSTEM_PROMPT},
            {"role": "user", "content": brief},
        ],
    }
    req = urllib.request.Request(
        url,
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json", "Authorization": f"Bearer {api_key}"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        body = json.loads(resp.read().decode("utf-8"))
    return body["choices"][0]["message"]["content"].strip() + "\n"


def summarize(d: dict, top: int = 25) -> dict:
    """返回 {markdown, model, fallback, reason?}。校验失败抛 ValueError。"""
    validate(d)
    base_url = os.environ.get("OPENAI_BASE_URL", "").strip()
    api_key = os.environ.get("OPENAI_API_KEY", "").strip()
    model = os.environ.get("OPENAI_MODEL", "gpt-4o-mini").strip()
    if base_url and api_key:
        try:
            md = _call_llm(build_brief(d, top), base_url, api_key, model)
            return {"markdown": md, "model": model, "fallback": False}
        except (urllib.error.URLError, KeyError, ValueError, TimeoutError) as e:
            return {
                "markdown": fallback_summary(d, top),
                "model": "fallback",
                "fallback": True,
                "reason": f"LLM 调用失败，已回退确定性模板: {e}",
            }
    return {"markdown": fallback_summary(d, top), "model": "fallback", "fallback": True}
