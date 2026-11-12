#!/usr/bin/env python3
"""fake-kokoro —— CatHome5 轮末总结实机验收用的 TTS 桩。

用途：本机无 Kokoro 运行环境（KOKORO_HOME 缺）时，按 py stdio 严格协议提供
「speak → 产出 WAV」的等价回执，让「合成产物存在 → audio.Join 拼接 → audio.Enqueue 入队」
全链在真实宿主里跑一遍。产出是 440 Hz 正弦音，不是语音——只作链路验收，不伪称 TTS。

协议（与 mau:tools/kokoro/stdio_server.py 同形）：
  首行 ready → 每行请求一行回执；op = speak（默认）/ ping / quit。
"""

import io
import json
import math
import os
import struct
import sys

SAMPLE_RATE = 24000
SECONDS = 0.4


def write_tone(path):
    """写一段 440 Hz 正弦 WAV（16bit / 单声道 / 24 kHz）。"""
    frames = int(SAMPLE_RATE * SECONDS)
    data = bytearray()
    for i in range(frames):
        value = int(12000 * math.sin(2 * math.pi * 440.0 * i / SAMPLE_RATE))
        data += struct.pack("<h", value)
    header = (
        b"RIFF"
        + struct.pack("<I", 36 + len(data))
        + b"WAVE"
        + b"fmt "
        + struct.pack("<IHHIIHH", 16, 1, 1, SAMPLE_RATE, SAMPLE_RATE * 2, 2, 16)
        + b"data"
        + struct.pack("<I", len(data))
    )
    directory = os.path.dirname(path)
    if directory:
        os.makedirs(directory, exist_ok=True)
    with open(path, "wb") as handle:
        handle.write(header)
        handle.write(bytes(data))


def reply(stream, payload):
    stream.write(json.dumps(payload, ensure_ascii=False) + "\n")
    stream.flush()


def main():
    # 协议面固定 UTF-8；stdout 只出协议行（第三方输出归 stderr）
    stdin = io.TextIOWrapper(sys.stdin.buffer, encoding="utf-8")
    out = io.TextIOWrapper(sys.stdout.buffer, encoding="utf-8", newline="\n")
    sys.stdout = sys.stderr
    reply(out, {"ok": True, "event": "ready", "loadMs": 1.0})
    for line in stdin:
        line = line.strip()
        if not line:
            continue
        try:
            request = json.loads(line)
        except json.JSONDecodeError:
            reply(out, {"ok": False, "error": "BAD_JSON"})
            continue
        op = request.get("op", "speak")
        request_id = request.get("id", "")
        if op == "quit":
            reply(out, {"id": request_id, "ok": True, "quit": True})
            return
        if op == "ping":
            reply(out, {"id": request_id, "ok": True, "model": "fake-kokoro", "providers": ["CPUExecutionProvider"]})
            continue
        text = request.get("text", "")
        out_path = request.get("out", "")
        if not text:
            reply(out, {"id": request_id, "ok": False, "error": "text 为空"})
            continue
        if not out_path:
            reply(out, {"id": request_id, "ok": False, "error": "out 为空"})
            continue
        try:
            write_tone(out_path)
        except OSError as exc:
            reply(out, {"id": request_id, "ok": False, "error": str(exc)})
            continue
        reply(out, {
            "id": request_id,
            "ok": True,
            "out": out_path,
            "ms": 1.0,
            "audioSeconds": SECONDS,
            "sampleRate": SAMPLE_RATE,
        })


if __name__ == "__main__":
    main()
