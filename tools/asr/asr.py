# -*- coding: utf-8 -*-
"""DeepSeek ASR 工具——把音频文件转写为文字（一次请求，不走 Files API）。

用法：
    python asr.py <音频路径> [更多路径...]
    python asr.py --voice-check <音频路径>       # 只回显、不打印 JSON 原文

输入：16 位 PCM WAV 或 Ogg Opus（采样率 8k/16k/22.05k/24k/32k/44.1k/48k；单/双声道）
限制：单文件 ≤ 8 MiB、时长 ≤ 120 秒、语种自动识别（无需参数）
输出：stdout 逐文件一行 JSON（text / duration_ms / request_id / 本地耗时）

key 从 CH4 数据根 secrets 读（与探针同源），不进脚本、不进命令行：
    环境变量 CH4_SECRETS 可覆盖 secrets 目录；CH4_ASR_KEY_ID 可覆盖端点 key id。
"""
import http.client
import json
import os
import sys
import time
import uuid
import wave
from pathlib import Path

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8")

HOST = "api.deepseek.com"
PATH = "/v1/tools/asr"
DEFAULT_SECRETS = Path(os.environ.get(
    "CH4_SECRETS", r"C:\Users\yufei\AppData\Local\CatHome4\Data\secrets"))
DEFAULT_KEY_ID = "dc5af3664cdc48b28caf3276efabc6b9"
MAX_BYTES = 8 * 1024 * 1024
MAX_SECONDS = 120.0


def load_key():
    """从 secrets 读取本机 DeepSeek key。"""
    secrets = Path(os.environ.get("CH4_SECRETS", str(DEFAULT_SECRETS)))
    key_id = os.environ.get("CH4_ASR_KEY_ID", DEFAULT_KEY_ID)
    raw = json.loads(secrets.joinpath("llm-api.cfg").read_text(encoding="utf-8"))
    key = raw.get(key_id)
    if not key:
        raise SystemExit("ERR|KEY_MISSING|secrets 里没有 %s" % key_id)
    return key


def probe_wav(path):
    """读取 wav 元信息；非 wav 返回 None（交给服务端判定格式）。"""
    try:
        with wave.open(str(path), "rb") as w:
            frames = w.getnframes()
            rate = w.getframerate()
            width = w.getsampwidth()
            channels = w.getnchannels()
    except Exception:  # 非 wav / 头部异常——不做本地判定，交服务端
        return None
    if rate <= 0:
        return None
    return {"seconds": frames / float(rate), "rate": rate,
            "bits": width * 8, "channels": channels}


def precheck(path):
    """本地前置校验——超限即出声拒绝，不把可预判的失败交给服务端。"""
    if not path.exists():
        return "ERR|NOT_FOUND|%s" % path
    size = path.stat().st_size
    if size > MAX_BYTES:
        return "ERR|TOO_LARGE|%.1f MiB > 8 MiB" % (size / 1048576.0)
    info = probe_wav(path)
    if info is not None:
        if info["seconds"] > MAX_SECONDS:
            return "ERR|TOO_LONG|%.1f s > 120 s" % info["seconds"]
        if info["bits"] != 16:
            return "ERR|BAD_FORMAT|%d bit，需 16 位 PCM" % info["bits"]
    return None


def recognize(key, path):
    """上传一次音频并返回转写结果。"""
    boundary = "----asr" + uuid.uuid4().hex
    raw = path.read_bytes()
    suffix = path.suffix.lower()
    ctype = "audio/ogg" if suffix in (".ogg", ".opus") else "audio/wav"
    head = ("--" + boundary + "\r\n"
            + 'Content-Disposition: form-data; name="file"; filename="' + path.name + '"\r\n'
            + "Content-Type: " + ctype + "\r\n\r\n").encode("utf-8")
    body = head + raw + ("\r\n--" + boundary + "--\r\n").encode("utf-8")
    conn = http.client.HTTPSConnection(HOST, timeout=180)
    started = time.perf_counter()
    conn.request("POST", PATH, body=body, headers={
        "Authorization": "Bearer " + key,
        "Content-Type": "multipart/form-data; boundary=" + boundary,
        "Content-Length": str(len(body))})
    resp = conn.getresponse()
    payload = resp.read()
    elapsed = (time.perf_counter() - started) * 1000.0
    conn.close()
    out = {"file": str(path), "status": resp.status, "ok": resp.status == 200,
           "elapsedMs": round(elapsed, 1), "bytes": len(raw)}
    try:
        data = json.loads(payload.decode("utf-8"))
    except Exception:  # 非 JSON 响应——原文带回，不静默
        out["error"] = payload[:400].decode("utf-8", "replace")
        return out
    if resp.status == 200:
        out["text"] = data.get("text", "")
        out["durationMs"] = data.get("duration_ms")
        out["requestId"] = data.get("request_id")
    else:
        out["error"] = data.get("error", data)
    return out


def main():
    """命令行入口——逐文件转写并打印 JSON 行。"""
    args = [a for a in sys.argv[1:] if a != "--voice-check"]
    if not args:
        print("用法: python asr.py <音频路径> [更多路径...]")
        return 2
    key = load_key()
    failed = 0
    for item in args:
        path = Path(item)
        problem = precheck(path)
        if problem:
            print(json.dumps({"file": str(path), "ok": False, "error": problem},
                             ensure_ascii=False))
            failed = failed + 1
            continue
        rec = recognize(key, path)
        print(json.dumps(rec, ensure_ascii=False))
        if not rec.get("ok"):
            failed = failed + 1
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
