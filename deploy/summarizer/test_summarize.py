"""summarize.py 单测：确定性回退在真实 digest.json 上产出合规五节。

跑法（无第三方依赖）：
    python3 -m unittest deploy/summarizer/test_summarize.py -v
"""
import json
import os
import unittest

import summarize as S

FIXTURE = os.path.join(os.path.dirname(__file__), "testdata", "digest.fixture.json")
SILENT = os.path.join(os.path.dirname(__file__), "testdata", "digest.silent.json")

SECTIONS = ["## ①", "## ②", "## ③", "## ④", "## ⑤"]


def load(p):
    with open(p, encoding="utf-8") as fh:
        return json.load(fh)


class TestFallback(unittest.TestCase):
    def setUp(self):
        # 确保不误用宿主 LLM 配置，回退路径可测。
        for k in ("OPENAI_BASE_URL", "OPENAI_API_KEY"):
            os.environ.pop(k, None)

    def test_five_sections_present(self):
        md = S.summarize(load(FIXTURE))["markdown"]
        for s in SECTIONS:
            self.assertIn(s, md, f"缺少节标题 {s}")

    def test_fallback_flagged(self):
        res = S.summarize(load(FIXTURE))
        self.assertTrue(res["fallback"])
        self.assertEqual(res["model"], "fallback")

    def test_segments_have_timecodes(self):
        d = load(FIXTURE)
        md = S.summarize(d)["markdown"]
        # 每个转写分段的 [mm:ss] 必须出现在分段摘要里。
        for seg in d["transcript"]["segments"]:
            code = S.clock(seg["start_s"])
            self.assertIn(f"[{code}]", md)

    def test_unspoken_keywords_in_table(self):
        d = load(FIXTURE)
        md = S.summarize(d)["markdown"]
        for k in d["screen_keywords"]:
            if not k["spoken_in_transcript"] and not k["is_chrome"]:
                self.assertIn(k["term"], md, f"未口述关键字缺失: {k['term']}")

    def test_keywords_sorted_by_score_desc(self):
        d = load(FIXTURE)
        md = S.summarize(d)["markdown"]
        terms = [k for k in d["screen_keywords"] if not k["spoken_in_transcript"] and not k["is_chrome"]]
        terms.sort(key=lambda k: k["score"], reverse=True)
        positions = [md.index(k["term"]) for k in terms]
        self.assertEqual(positions, sorted(positions), "第③节未按 score 降序")

    def test_gaps_verbatim(self):
        d = load(FIXTURE)
        d["gaps"] = ["帧数触顶 --max-frames 600", "locale 降级"]
        md = S.summarize(d)["markdown"]
        for g in d["gaps"]:
            self.assertIn(g, md)

    def test_empty_gaps_note(self):
        d = load(FIXTURE)
        d["gaps"] = []
        md = S.summarize(d)["markdown"]
        self.assertIn("无缺口", md)

    def test_silent_video_no_fabricated_speech(self):
        md = S.summarize(load(SILENT))["markdown"]
        self.assertIn("无音轨，无口述内容", md)
        self.assertIn("## ③", md)  # 无音轨仍给画面关键字

    def test_validate_rejects_missing_keys(self):
        with self.assertRaises(ValueError):
            S.summarize({"source": {}})


if __name__ == "__main__":
    unittest.main()
