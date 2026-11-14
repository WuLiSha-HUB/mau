# ASR 工具（DeepSeek 语音识别）

把音频文件转写为文字。一次请求完成，**不走 Files API**。

## 用法

```bash
python asr.py <绝对路径> [更多路径...]
```

- 逐个文件输出一行 JSON：`{file, status, ok, elapsedMs, bytes, text, durationMs, requestId}`
- 失败行带 `error`，退出码 1（全成功为 0）
- 🔴 路径写**绝对路径**——`py-run` 的 `args` 原样透传，不解析 `mau:` / `data:` 这类受控根前缀（实测：传 `mau:CatTemp\...` 会报 `ERR|NOT_FOUND`）

## 能力与限制

| 项 | 值 |
|:--|:--|
| 输入格式 | 16 位 PCM WAV / Ogg Opus（服务端按文件内容判定） |
| 采样率 | 8000 / 16000 / 22050 / 24000 / 32000 / 44100 / 48000 Hz |
| 声道 | 单或双（双声道只识别左声道） |
| 单文件上限 | 8 MiB · 时长 120 秒 |
| 语种 | 21 种，**自动识别，无语种参数**（中 / 粤 / 英 / 日 / 韩 / 德 / 法 / 西 / 葡 / 意 / 俄 / 阿 / 印地 / 印尼 / 越 / 泰 / 土 / 马来 / 菲 / 波 / 匈） |
| 计费 | 0.78 元/小时（限时 5 折 0.39），按秒计 |

本地前置校验：不存在 / 超 8 MiB / 超 120 秒 / 非 16 位，一律 `ERR|` 出声拒绝，不把可预判的失败交给服务端。

## key 来源

从 CH4 数据根 `secrets/llm-api.cfg` 读取（与 `config/llm-api.json` 同源）：

| 环境变量 | 默认 |
|:--|:--|
| `CH4_SECRETS` | `%LOCALAPPDATA%\CatHome4\Data\secrets` |
| `CH4_ASR_KEY_ID` | `dc5af3664cdc48b28caf3276efabc6b9`（即 DSCatHome 端点） |

key 不进脚本、不进命令行。

## 典型用途

**TTS 验收对照**——播报稿改动后跑一次「TTS 生成 → ASR 回读 → 与原文比对」，字准率掉了即改坏。实测能力与限制（含缩写拆字母、URL、markdown 的念法）：

| 输入 | 回读 | 结论 |
|:--|:--|:--|
| `U C U D A`（拆字母写法） | `UC UDA` | ❌ 拆字母反倒制造断词错误 |
| `https://api.deepseek.com/docs` | `HTTPS padopsf doc` | ❌ URL 念不对，提示词须禁止念链接 |
| `## 标题 **加粗**` | `标题，这是加粗` | ✅ `filter_markdown=true` 已清干净 |
| `1.8.260` | `1.8260` | ⚠️ 三段版本号被合并，须改口语写法 |

> 判据来源：2026-11-14 探针实测（`CatTemp/ttsprobe/probe_loop.py`），服务行为随版本可能变，接线上线前复核。
