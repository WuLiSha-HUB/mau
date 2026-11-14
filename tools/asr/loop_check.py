# -*- coding: utf-8 -*-
"""TTS → ASR 闭环探针——客观测「云端怎么念」与「念出来还是不是原意」。

用途：A348（自动总结提示词去掉强制转化）的判据来源。
强制转化原本是为了让本地 Kokoro 念对缩写 / 数字 / 符号；
云端 TTS 到底怎么处理这些，用 ASR 回读给出可核验的答案。
"""
import http.client
import json
import os
import struct
import sys
import time
import uuid
from pathlib import Path

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8")

HOST = "api.deepseek.com"
SECRETS = Path(os.environ.get("CH4_SECRETS", r"C:\Users\yufei\AppData\Local\CatHome4\Data\secrets"))
KEY_ID = "dc5af3664cdc48b28caf3276efabc6b9"
OUT = Path(__file__).resolve().parent.joinpath("out")

# 每项 = (标签, 原文, TTS 额外参数)
CASES = [
    ("number", "第 3 号样本，共 12 条，耗时 1.5 秒，版本 1.8.260。", None),
    ("abbr", "这条语音由 U C U D A 加速，走 L L M 接口。", None),
    ("url", "详见官网 https://api.deepseek.com/docs 上的说明。", None),
    ("code", "调用 audio-speak 函数，参数是 text 和 voice。", None),
    ("md_on", "## 标题\n这是**加粗**与`行内代码`，还有[链接](http://x.y)。", None),
    ("md_off", "## 标题\n这是**加粗**与`行内代码`，还有[链接](http://x.y)。",
     {"filter_markdown": False}),
    ("plain", "任务已经完成，桌面宠物皮肤的渲染问题也一并修好了，请查看。", None),
]


def load_key():
    """读 key。"""
    return json.loads(SECRETS.joinpath("llm-api.cfg").read_text(encoding="utf-8"))[KEY_ID]


def fix_wav(data):
    """重写 RIFF / data 长度（云端返回的是占位头）。"""
    raw = bytearray(data)
    raw[4:8] = struct.pack("<I", len(raw) - 8)
    pos = 12
    while pos + 8 <= len(raw):
        cid = bytes(raw[pos:pos + 4])
        size = struct.unpack("<I", raw[pos + 4:pos + 8])[0]
        if cid == b"data":
            raw[pos + 4:pos + 8] = struct.pack("<I", len(raw) - (pos + 8))
            break
        pos += 8 + size + (size & 1)
    return bytes(raw)


def tts(key, text, extra, dest):
    """合成并落盘（修头）。"""
    body = {"text": text, "voice_id": "stella"}
    if extra:
        body.update(extra)
    payload = json.dumps(body, ensure_ascii=False).encode("utf-8")
    conn = http.client.HTTPSConnection(HOST, timeout=120)
    t0 = time.perf_counter()
    conn.request("POST", "/v1/tools/tts", body=payload, headers={
        "Authorization": "Bearer " + key,
        "Content-Type": "application/json; charset=utf-8"})
    resp = conn.getresponse()
    data = resp.read()
    ms = (time.perf_counter() - t0) * 1000.0
    conn.close()
    if resp.status != 200:
        return {"status": resp.status, "body": data[:200].decode("utf-8", "replace")}
    fixed = fix_wav(data)
    dest.write_bytes(fixed)
    return {"status": 200, "tts_ms": round(ms, 1), "tts_bytes": len(fixed),
            "audio_s": round((len(fixed) - 44) / 48000.0, 2)}


def asr(key, path):
    """上传音频，返回转写文本。"""
    boundary = "----probe" + uuid.uuid4().hex
    raw = path.read_bytes()
    head = ("--" + boundary + "\r\n"
            + 'Content-Disposition: form-data; name="file"; filename="' + path.name + '"\r\n'
            + "Content-Type: audio/wav\r\n\r\n").encode("utf-8")
    body = head + raw + ("\r\n--" + boundary + "--\r\n").encode("utf-8")
    conn = http.client.HTTPSConnection(HOST, timeout=180)
    t0 = time.perf_counter()
    conn.request("POST", "/v1/tools/asr", body=body, headers={
        "Authorization": "Bearer " + key,
        "Content-Type": "multipart/form-data; boundary=" + boundary,
        "Content-Length": str(len(body))})
    resp = conn.getresponse()
    payload = resp.read()
    ms = (time.perf_counter() - t0) * 1000.0
    conn.close()
    rec = {"status": resp.status, "asr_ms": round(ms, 1)}
    try:
        data = json.loads(payload.decode("utf-8"))
        rec["text"] = data.get("text")
        rec["duration_ms"] = data.get("duration_ms")
    except Exception:  # 探针：解析失败带原文
        rec["raw"] = payload[:300].decode("utf-8", "replace")
    return rec


def main():
    key = load_key()
    OUT.mkdir(parents=True, exist_ok=True)
    rows = []
    for tag, text, extra in CASES:
        wav = OUT.joinpath("loop_" + tag + ".wav")
        t = tts(key, text, extra, wav)
        if t.get("status") != 200:
            print("[%s] TTS 失败 %s %s" % (tag, t.get("status"), t.get("body")))
            continue
        a = asr(key, wav)
        row = {"tag": tag, "original": text, "tts": t, "asr": a}
        rows.append(row)
        print("=" * 80)
        print("[%s] audio=%ss tts=%sms | asr=%sms dur=%sms"
              % (tag, t["audio_s"], t["tts_ms"], a.get("asr_ms"), a.get("duration_ms")))
        print("  原文 : " + text.replace("\n", " ⏎ "))
        print("  回读 : " + str(a.get("text", a.get("raw", ""))).replace("\n", " ⏎ "))
    OUT.joinpath("loop_report.json").write_text(
        json.dumps(rows, ensure_ascii=False, indent=2), encoding="utf-8")
    print("REPORT " + str(OUT.joinpath("loop_report.json")))


if __name__ == "__main__":
    main()
