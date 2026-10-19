# Kokoro TTS —— 知识台账（选型 / 本体 / 性能 / 装机 / 集成）

> 定位：Kokoro 的**一次性知识**——换机重建、换引擎决策、性能对照时消费。新机部署照 `README.md` 走，遇「为什么这样配」的问题查本文件。
> 边界：操作面（协议 / 启动开关 / 文件清单 / 部署步骤 / 用法）→ `README.md`；本文件答「为什么是它 / 它跑成什么样 / 环境怎么长出来的」。
> 来源：2026-10-17 自 CCBP 归档迁入——`Project/CH4/research-ollv.md` §2.7 / §2.8 / §七 + `Project/CH4/design-ch4-pytools.md` §六（原处留指针，不再双写）。

---

## 一、选型（为什么是 Kokoro）

### 1.1 三条落地路径（2026-10-09 调研 · 莎定本机部署）

| 路径 | 代表 | 代价 | 约束 |
|:--|:--|:--|:--|
| 商用 API | ElevenLabs · OpenAI TTS · Azure · 国内：豆包 / 阿里 / 腾讯 / 讯飞 | 按字符计费（OpenAI $15/百万字符 · ElevenLabs $54–198 · 托管 Kokoro $0.62） | 文本与音频**出域** |
| **开源模型 + 本机推理**（选定） | **Kokoro** · Piper · CosyVoice2 · Qwen3-TTS · Chatterbox | 一次性硬件 | 需自己补服务层 |
| 开源模型 + 托管推理 | DeepInfra / Replicate / Speaches | 按字符（低于一线商用） | 仍出域 |

判据：商用 API 自然度仍占优，但成本是自托管的 **5–20 倍**且必须出域；本机推理把**授权 / 隐私 / 批量成本**变成决定性优势。

### 1.2 本机部署资源需求（2026-10-09 调研）

| 模型 | 磁盘 | 显存 | 纯 CPU | 声音克隆 |
|:--|:--|:--|:--|:--|
| **Kokoro-82M**（选定） | **~0.3 GB**（ONNX q8 仅 92 MB） | **~150 MB** | ✅ 实时（RTF≈0.05） | ❌ 固定音色 |
| CosyVoice2-0.5B | ~2.6 GB（另需磁盘 ≥10 GB） | 加载 3.2–3.5 GB · 峰值 ~5 GB | ⚠️ 很慢 | ✅ 零样本 |
| Qwen3-TTS 0.6B / 1.7B | 2.5 / 4.5 GB | 3.5–4 / 5–6 GB | ⚠️ 慢 10–50× | ✅ |
| Qwen3-TTS 1.7B GGUF Q4_K_M | 1.2 GB | 1.8–2.1 GB | — | ✅ |

实测机（决策基线）：RTX 3080 **10 GB**（当时余 ~7.9 GB）· i7-12700F · RAM **32 GB** → 上表全部可跑。

⚠️ 价格与显存区间有多源分歧（Azure Neural 报 $4 与 $16；CosyVoice2 显存「<3 GB」至「峰值 5 GB」；Qwen3-TTS 1.7B「4 GB」至「8 GB+」）——换引擎前按目标仓库 README 与厂商价目表复核。

### 1.3 三引擎同句实测（2026-10-15 · 收敛回 Kokoro 单走）

触发：验证「换更大模型能否拿到更丰富的情感 / 音色控制」。
测试文本（79 字 · 中英混排 + 数字）：`Qwen3-TTS ONNX int4 与 ChatTTS 均已在本机 RTX 3080 跑通并各生成一段音频，建议先做同句三引擎对比、再试参考音频克隆选型。`

| | Kokoro 82M | ChatTTS 0.2.5 | Qwen3-TTS 0.6B ONNX int4 |
|:--|:--|:--|:--|
| 时长 | 12.70 s | 10.99 s | 14.80 s |
| 停顿 | 2.40 s / 9 段 | 0.76 s / 5 段 | 2.04 s / 10 段 |
| 基频（全句→尾段） | 255→227 Hz | 139→137 Hz | 224→205 Hz |
| 生成耗时 | **1.56 s**（RTF 0.12） | 16.67 s | ~3 s |
| 显存 | ~1 GB | 1.16 GB | 1.54 GB 权重 + ~2.7 GB 依赖 |
| 中英混读 | ✅ 准确 | ⚠ 数字 / 连字符被判无效字符 | ✅ |
| 音色来源 | 103 个固定音色 | 抽卡（不可定向，须存 `spk_emb`） | 参考音频克隆 |

**被否路径与原因**

| 路径 | 结论 | 关键事实 |
|:--|:--|:--|
| **ChatTTS** | 「有特色，但音质一般」 | 音色靠 `sample_random_speaker()` 抽卡、不可定向；阿拉伯数字与连字符判为无效字符（日志 `found invalid characters`）；自回归慢（~50 it/s，16.7 s/句）；中文口语对话导向、非角色音。实测抽卡 33 个音色（社区推荐少女系 seed `9405/6653/4099/5099/3333` + 少女化参数 `speed 6 / temp 0.6 / oral_3+laugh_2`）——基频区间 116–343 Hz，未达要求 |
| **Qwen3-TTS ONNX int4** | 低内存克隆质量一般 | `cuda_int4` 全套 1.54 GB、CUDA EP 通、显存友好；但 0.6B **Base 无 instruct**（VoiceDesign / CustomVoice 是 1.7B 专属）；用 Kokoro 合成声做参考的克隆音高对得上（参考 259→244 Hz vs 克隆 267→258 Hz，≈3%），**真实素材未验证即否** |
| **GPT-SoVITS / Style-Bert-VITS2** | 未启动（留纸面） | 社区二次元角色音色的正解路线（GSVI 4000+ 角色库 / SBV2 日语萌系），本轮未部署 |

**为什么 Kokoro 赢（四条判据）**

1. **音质纯净度 + 吐字准确度**是听觉第一判据——直接胜出（决策人实测判断）。
2. **中英混排**：挂 `EspeakG2P` 后 0 越界（见 `README.md` §七 坑 6）；ChatTTS 对数字 / 符号有硬缺陷。
3. **速度差一个数量级**：1.56 s vs 16.67 s——同一条播报链上不可忽略。
4. **「情绪控制」是假需求**：Kokoro 无 SSML / 情绪标签，**标点 + 音色 + 语速即全部可动面**——为它换引擎不划算。

---

## 二、本体

### 2.1 出身与架构

| 项 | 内容 |
|:--|:--|
| 出身 | **hexgrad**（独立研究者）· 底子 = **StyleTTS 2 + ISTFTNet**（哥大 `yl4579/StyleTTS2-LJSpeech`） |
| 命名 | 日语「心 / 精神」；兼致敬《终结者》系列 AI 角色 Kokoro |
| 发布节奏 | 2024-12-25 v0.19（英文 · 训练数据 <100h）→ 2025-01-27 v1.0（8 语言 / 54 音色）→ 2025-02-26 v1.1-zh（中英 · 103 音色） |
| 训练成本 | 三段合计约 **1120 A100 小时 / 约 1110 美元**（算力自 Vast.ai 租） |
| 架构 | decoder-only · **非自回归**（无 diffusion 采样循环）· 单次前向出音频 · 输出 **24 kHz** · **82M** |
| 音色机制 | 每音色 = **256 维风格向量**（前 128 给解码器 / 后 128 给韵律）；**支持音色插值混合** |
| G2P | **misaki** 主引擎，非专用语言回退 **espeak-ng** |
| 现状 | OpenRouter 上架（$0.62/百万字符）· 维基媒体「Listening to Wikipedia」MVP 采用 · 移植生态：ONNX / CoreML(ANE) / OpenVINO / GGUF / MLX / ExecuTorch / Kokoro-FastAPI |
| 中文音色（8 个） | `zf_xiaoxiao` · `zf_xiaobei` · `zf_xiaoyi` · `zf_xiaoni` / `zm_yunxi` · `zm_yunyang` · `zm_yunjian` · `zm_yunxia` |
| 作者自述局限 | ① **无声音克隆**（固定音色）② 依赖外部 G2P 带来一类失败模式 ③ 训练集偏长文朗读 / 叙述、**缺对话** ④ 82M 参数量上限——遇训练良好的 1B+ 模型会处劣势 |

⚠️ 口径提醒：语言数「8 种」为官方计数（美 / 英音合并，音色包实际按 9 组；espeak-ng 回退可扩韩 / 德 / 俄但质量明显更低）· 音色数权威 = **54**（v1.0，见到 41 是某模板只装了 41 个）· 部分来源标注 2026 年日期，当「撰写时最新」看。

### 2.2 许可分级与分发义务（2026-10-14 一手核实）

| 层 | 功能 | 许可 | 级别 |
|:--|:--|:--|:--|
| 权重 | Kokoro-82M 模型文件本体 | Apache-2.0 | 宽松（可商用） |
| 推理代码 | `kokoro-onnx` / `misaki-fork` | MIT / Apache-2.0 | 宽松 |
| **G2P** | **espeak-ng + phonemizer**（文本→音素；非专用语言回退） | **GPL-3.0** | 强 copyleft |

🔴 GPL 义务挂在**分发**、不挂在使用——本机自用不触发；将来打包对外分发时这一环才成为问题点（进程边界通常算独立程序，但同 venv 打包情形存疑）。

### 2.3 体积构成——「100 MB 模型配 500 MB 壳」的真相

模型只占约 1/3，其余是**运行时三笔税**：

| 税 | 内容 | 本机实测 |
|:--|:--|:--|
| 推理引擎 | `onnxruntime` | CPU 版 15 MB · **GPU 版 168 MB**，另需 CUDA 运行库（cublas 423 MB + cudnn 437 MB） |
| **G2P** | jieba 19 MB + pypinyin-dict 9.5 MB + espeak-ng 9.4 MB | **中文特有**——英文靠规则转音素，中文必须查词典（分词 + 多音字） |
| Python 生态 | 每包一个 wheel + 零散依赖（regex / protobuf / cffi…） | ~15 MB |

对照 Smriti（向量库）为何轻：C# 原生——零 Python 运行时、零 wheel 生态、零外部 C 库、词表自持。**轻量的来源是没有外部生态依赖，不是算法简单。** 模型体积 ∝ 参数量；运行时体积 ∝ 生态，二者无关——82M 这个尺寸下运行时 ≈ 模型量级属正常。

---

## 三、性能

### 3.1 三档实测（RTX 3080 10 GB / i7-12700F / 32 GB · 2026-10-14）

| 模型 | 后端 | 10 字 | 30 字 | 100 字 | RTF |
|:--|:--|--:|--:|--:|:--|
| `fp32` 310 MB | **GPU（CUDA）** | **61 ms** | **219 ms** | **1749 ms** | 0.027 / 0.037 / 0.092 |
| `fp16` 156 MB | CPU | 366 ms | 1406 ms | 5149 ms | ~0.25 |
| `int8` 109 MB | CPU | 2301 ms | 7882 ms | 26485 ms | ~1.4 |

- 冷启 GPU 2.4 s / CPU 2.5 s · 进程内存 ~1.2 GB（GPU 档显存另计，约 1 GB）
- **耗时对字数超线性**（≈ n^1.4）——长播报单位成本更高
- **出音延迟**（用户感知的开口前停顿）：10 字 61 ms（无感）· 30 字 219 ms（无感）· 100 字 1.75 s（可察觉）
- 播报稿规格本就是「一句结论 + 一句指向」（≈30 字内）→ 落在无感区间

### 3.2 模型档位——只两种组合可用

| 组合 | 模型 | 后端 | 状态 |
|:--|:--|:--|:--|
| **推荐** | fp32（310 MB） | **CUDA** | ✅ 长短文本全稳 |
| 兜底 | fp16（156 MB） | CPU | ✅ 稳，但慢 4–5× |
| ❌ 禁 | fp16 | CUDA | **崩**——fp16 图含 CPU 无 kernel 的节点 |
| ❌ 弃 | int8（109 MB） | CPU | 最慢（x86 缺优化内核） |

> 反直觉但实测确凿：**int8 < fp16 < fp32**（CPU 侧），而 **GPU 侧只吃 fp32**。

### 3.3 四条实测反例（均反直觉，动手前必读）

| # | 反例 | 证据 |
|:--:|:--|:--|
| 1 | **int8 在 x86 CPU 上最慢** | 比 fp16 慢 5–6×——缺优化内核 + 逐算子反量化开销 |
| 2 | **fp16 + CUDA 必崩** | fp16 图含 CPU EP 无 kernel 的 fp16 节点（`Reciprocal`）；短文本侥幸通过（假绿），n≈12 字起 native crash——**GPU 只能配 fp32** |
| 3 | **onnxruntime-directml 与本模型不兼容** | 加载即崩，且版本落后 CPU 版 7 个 minor |
| 4 | **两个 nvidia pip 包命名规则相反** | `nvidia-cudnn` 是占位包（真名 `nvidia-cudnn-cu13`）；`nvidia-cublas-cu13` 反被标记废弃（用 `nvidia-cublas`）；代码侧须 `ort.preload_dlls()` 挂 dll 路径 |

### 3.4 服务形态与宿主集成成本

- 冷启 **2.4–2.6 s** / 常驻约 **1.2 GB**——**不是每轮都用，默认按需拉起**（`follow` 默认 false；本机因轮末总结播报改设 true）
- 本地 TTS 是**连续时间流**，宿主是**单线程 Tick + OA 工单串行**——合理形态是**独立服务进程 + 进程间协议**，不把音频链塞进宿主主循环
- 已知优化点：`create_stream` 分句流式（现为整段返回，长稿 1.75 s 出音可压）

---

## 四、环境与装机实录

### 4.1 目录布局

```
<KOKORO_HOME>/                 外部目录——纯净拉取物，不含任何自写代码
  .venv/                       venv（解释器 + CUDA 栈）
  models/                      模型三件
    kokoro-v1.1-zh.onnx        fp32 · 310 MB
    voices-v1.1-zh.bin         音色库 103 个
    kokoro-v1.1-zh-config.json 词表配置（新版模型已内嵌，缺失亦可跑）
```

🔴 **venv 里的 `python.exe` 是转发器**——启动真正的 base 解释器后等待，真实负载在**孙进程**。按单进程句柄计量内存会读到「壳」（实测 4.2 MB vs 真实 765 MB）——量内存须按**进程树**聚合。

### 4.2 家机装机实录（2026-10-17 · 换机重装）

| 项 | 实况 |
|:--|:--|
| 机器 | RTX 3080 10 GB / 驱动 617.14 · 系统 Python 3.12.10（`%LOCALAPPDATA%\Programs\Python\Python312`）· pip 25.0.1 |
| 环境位置 | `WorkSpace/Kokoro/`（`KOKORO_HOME`） |
| 基础依赖 | kokoro-onnx 0.6.1 · misaki-fork 0.9.6 · soundfile 0.14.0 · espeakng-loader 0.2.4 |
| GPU 依赖 | onnxruntime-gpu **1.31.0** · nvidia-cublas 13.8.0.4 · nvidia-cuda-runtime 13.4.92 · nvidia-cudnn-cu13 9.27.0.42 |
| 模型 | ONNX 310.4 MB + voices 51.3 MB + config 3.2 KB |
| 验证 | `selftest_stdio.py` → `pass: true` · providers `[CUDAExecutionProvider, CPUExecutionProvider]` · 4.5 s 音频合成 **257 ms**（RTF 0.057） |

**网络与源（国内环境三条实况）**

| 源 | 实况 | 处置 |
|:--|:--|:--|
| `huggingface.co` | 直连失败（套接字不可达） | config 走 `hf-mirror.com` 成功 |
| `github.com` release | 直连失败（超时 / 请求发送失败） | 模型两件走 `ghfast.top` 代理（2.9 min / 59 s） |
| PyPI | 直连慢 | 基础包走清华源 `pypi.tuna.tsinghua.edu.cn`（15–22 MB/s）；nvidia 包走 `--extra-index-url https://pypi.nvidia.com`（实测解析到 `pypi.nvidia.cn`） |
| ModelScope | 多引擎探索期结论：**hf-mirror 严重限流（429 + Xet 401）→ 改走 ModelScope**（国内 30–60 MB/s） | 本次模型改走 GitHub 代理，ModelScope 仍是备选通道 |

🔴 **踩坑：`kokoro-onnx` 会把 CPU 版 `onnxruntime` 拉回来覆盖 GPU 版**（依赖声明是 `onnxruntime>=1.20.1`，两个发行包写同一模块目录）。
处置：先装 `onnxruntime-gpu` 再装其余 → 仍被覆盖时 `pip install --force-reinstall onnxruntime-gpu==1.31.0`。
判据：`ping` 回执的 `providers` 含 `CUDAExecutionProvider` 才算 GPU 生效——**不带这个检查，静默回落 CPU 在功能上「能跑」，只在慢 4–25× 上体现**。

🔴 **卸载 / 重装 onnxruntime 须先停服务**——服务进程持有 `capi/*.dll` 句柄，卸载留下 `~api` / `~umpy*` 临时残留目录（文件被移走一半）。顺序：`py-service stop` → 装 → 清残留 → `start` → 复验。

**冷启**：本次首装配后 7.75 s（旧机基线 2.5 s）——首跑含字典 / 图首次加载，稳态待复测。

### 4.3 多引擎探索期沉淀（2026-10-15）

| 项 | 沉淀 |
|:--|:--|
| 模型下载通道 | hf-mirror 限流（429 + Xet 401）→ ModelScope |
| 环境隔离 | `torch(cu128)` 与 `onnxruntime-gpu(CUDA 13)` **同 venv 互踩**——装 nvidia cu13 包后 torch 加载 cudnn 崩；须拆独立 venv |
| 启动开关 | 服务侧 `--model` / `--device`（`kokoro_env.apply_cli`）——宿主 `py.service.kokoro.args` 由此真实可用（见 `README.md` §四·B） |
| 环境处置 | 探索期环境（ChatTTS 9.02 GB / Qwen3TTS 4.20 GB / TTSCompare）**已删除**；Kokoro 保留为主线 |

---

## 五、CH4 宿主集成契约

### 5.1 配置项（`Data/config/py.cfg` · schema 声明见 CH4 侧）

| key | 本机值 | 说明 |
|:--|:--|:--|
| `py.python_path` | 系统 Python 全路径 | 全局解释器（py-run 用） |
| `py.service.kokoro.python` | `<KOKORO_HOME>/.venv/Scripts/python.exe` | 服务解释器覆盖——**CUDA 栈只在该 venv 里**，系统 Python 跑不动 |
| `py.service.kokoro.script` | `mau:tools/kokoro/stdio_server.py` | 服务入口（受控根寻址） |
| `py.service.kokoro.home` | `<KOKORO_HOME>` | 起进程时以 `--home` 传入 |
| `py.service.kokoro.args` | `--device cuda` | 启动开关（启动期消费一次，改后须重启服务） |
| `py.service.kokoro.out_dir` | `Data/tts` | WAV 落点（宿主侧全局目录，无每猫） |
| `py.service.kokoro.timeout_ms` | 120000 | 单次调用超时（含冷启动与首次 CUDA 图执行） |
| `py.service.kokoro.follow` | true | 随宿主启动拉起（**关闭仍无条件清理**） |

### 5.2 合成 / 播放解耦

```
TTS 侧：  收参数 → 产出 WAV → 回执路径        （不知播放存在）
宿主侧：  拿回执路径 + key → 找播放服务         （不知 TTS 内部）
播放侧：  收路径 → 投递即返回                   （不知谁产出）
```

播放层不做工具（纯 C# 硬编码通知）；播放服务入口开放（供 TTS 之外消费方复用）；TTS 产出音频本身由 `audio-speak` 工具承载（AudioCat 组）。

### 5.3 契约判据

| # | 判据 |
|:--:|:--|
| 1 | **失败必须可见**——服务起不来报错、不静默降级；连续拉起失败 5 次置不可用态 |
| 2 | **超时不杀进程**（纯串行语义）——超时后调用方拿错误，服务端仍在跑、队列不推进 |
| 3 | **不静默回落**——`--device cuda` 而环境无 CUDA provider 即抛错，不悄悄转 CPU |
| 4 | **协议面 UTF-8 + stdout 独占**（见 `README.md` §四 三条铁律） |

---

## 六、播报文本规范化实测（2026-10-20）

> 口径：全部为本机 v1.1-zh 模型 + `ZHG2P(version="1.1")` 实测，指标来自合成音频的静音段与音素串，不靠听感推断。

### 6.1 标点 → 停顿时长

| 标点 | 停顿 | 备注 |
|:--|--:|:--|
| `，` / `、` / `——` | 140 ms | 三者等价（`、` 在 G2P 里被转成 `,`） |
| `；` | 200 ms | |
| `！` | 300 ms | |
| `。` / `？` / 换行 | 340 ms | 同一档 |
| `。。` | 480 ms | 标点数量可叠加拉长停顿 |
| `……` | **0 ms** | 被吞掉，不产生停顿——要停顿须替换成 `。` |
| 无标点 | 0 ms | 整句连读 |

骨架自带固定静音：句首 80 ms、句尾 240 ms（分段拼接时须裁掉，否则越拼越拖）。

### 6.2 数字与版本号

| 形态 | 实际读法 | 处理 |
|:--|:--|:--|
| `1.8.152` | 一点八点**一百五十二**（cn2an 按数值读尾段） | 逐位写汉字 `一点八点一五二`，或宿主侧正则兜底 |
| `8080` | 八千零八十 | 端口 / 编号须写汉字 `八零八零`；与「3 个文件」同形，无法自动判别 |
| `3 秒` / `12 个文件` | 三秒 / 十二个文件 | 数量读法正确，保持阿拉伯数字 |

### 6.3 多音字

「重」按 jieba 分词选音，**不是固定读 zhòng**：

| 词 | 读音 | 判定 |
|:--|:--|:--|
| `重来`（独词）/ `重做` / `重跑` | zhòng | ❌ 需同音转义（`虫来` / `虫做` / `虫跑`） |
| `重启` / `重新` / `重量` / `重要` | chóng / zhòng | ✅ 原字正确 |

转义副作用：同音替换会切断分词，语流变碎（`把这段虫来一遍` 每字独立成词）——只在明确读错时用。

### 6.4 无效干预（已实测排除）

| 猜想 | 实测 |
|:--|:--|
| 中英文之间加空格能改善混读 | ❌ 音素输出完全相同（空格被正则吸进英文段再 strip） |
| 专名加引号能制造停顿 | ❌ 引号不产生停顿（100/80 ms，与无停顿版同量级） |
| 全大写 + 空格能让缩写逐字母读 | ✅ `C U D A` → `sˈiː jˈuː dˈiː ˈA`；`CUDA` → `kjˈuːdə`（读成「酷达」） |

### 6.5 音色库实况

`voices-v1.1-zh.bin` 实际是 **103 个编号音色**（`zf_001…zf_099` 女声 / `zm_009…zm_100` 男声），不是早期记载的 8 个命名音色（`zf_xiaoxiao` 等属 v1.0）。基频分布：女声 136–296 Hz、男声 104–197 Hz。
