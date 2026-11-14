// ═══════════════════════════════════════════════════
// 积木: audio.speak
// ID:   BRIK-AUDIO-002
// 类别: AUDIO
// 作用: 文本转语音并播放——调云端 TTS 合成 WAV，再入队播放（LLM 工具 audio-speak 语料执行面）
// 依赖: 无
// 引用: Mau.Runtime（DataBox/JsonArgs/JsonUtil/ConfigStore/ConfigSchema/WorkspaceConfig）· Mau.Contracts（IAudioService/ITtsService）
// 原理: 解析 text/voice/speed/key/out → out 根寻址展开 → ITtsService.Synthesize(text,voice,out) → IAudioService.Enqueue
// 常用: audio_cat.mau 认领线——'audio.speak'[@adArgs] > @adRes
// 注意: 合成是云端同步往返（短句约 2 秒；本线在途期间不接新单）；播放入队即返回。语速参数当前 provider 不支持——回执显式出声忽略
// ═══════════════════════════════════════════════════
using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Text.Json;
using Mau.Contracts;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 音频积木——audio.speak 文本合成并播放（LLM 工具执行面：参数整包 argsJson）。
    /// </summary>
    public static class AudioSpeakBrick
    {
        /// <summary>
        /// 合成并播放——文本 → Kokoro WAV → 入队播放。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（text 必填；voice / speed / key / out 可选）</param>
        /// <param name="result">成功 "OK|产物路径"；失败 ERR| 文本</param>
        /// <returns>true=已受理</returns>
        public static bool Speak(string argsJson, out string result)
        {
            result = "";
            string badArgs = JsonArgs.Validate(argsJson, "text voice speed key out", "text", "", "");
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
            }
            string text = JsonArgs.Get(argsJson, "text");
            if (text.Length == 0 || text == "§PARSE_FAIL§")
            {
                result = "ERR|BAD_ARGS|缺少参数 text";
                return false;
            }
            string voice = Clean(JsonArgs.Get(argsJson, "voice"));
            if (voice.Length == 0)
            {
                // 缺省音色——全局配置 audio.voice（schema 值域四项；空则不下发，服务端兜底 zf_001）
                ConfigStore voiceCfg = null;
                if (DataBox.TryResolve<ConfigStore>(out voiceCfg) && voiceCfg != null)
                {
                    voice = Clean(voiceCfg.Get("audio.voice", ""));
                }
                if (voice.Length == 0)
                {
                    // 未落盘——回落 schema 声明的默认值（默认值不落盘是配置面设计，读面须自行兜底）
                    ConfigSchema voiceSchema = null;
                    if (DataBox.TryResolve<ConfigSchema>(out voiceSchema) && voiceSchema != null)
                    {
                        ConfigSchema.Item? voiceItem = voiceSchema.Find("audio.voice");
                        if (voiceItem != null)
                        {
                            voice = Clean(voiceItem.Default);
                        }
                    }
                }
            }
            string key = Clean(JsonArgs.Get(argsJson, "key"));
            string outArg = Clean(JsonArgs.Get(argsJson, "out"));
            float speed = ParseSpeed(JsonArgs.Get(argsJson, "speed"));
            // 根寻址展开——out 显式给定时按工作区根表展开；缺省产物路径已是绝对路径
            string outPath = outArg.Length > 0 ? ExpandPath(outArg) : BuildOutPath();

            // [段1] 合成——云端 TTS 同步往返
            ITtsService tts = null;
            if (!DataBox.TryResolve<ITtsService>(out tts) || tts == null)
            {
                result = "ERR|TTS_NO_SERVICE|宿主未注入 ITtsService";
                return false;
            }
            string synthError = tts.Synthesize(text, voice, outPath);
            if (synthError.Length > 0)
            {
                result = synthError;
                return false;
            }
            if (!File.Exists(outPath))
            {
                result = "ERR|TTS_FAIL|产物不存在: " + outPath;
                return false;
            }
            // 语速——当前 provider 不接受该参数：显式出声（不静默丢参数）
            string speedNote = "";
            if (speed > 1.001f || speed < 0.999f)
            {
                speedNote = "｜⚠ 当前 TTS 不支持语速调节，speed 已忽略";
            }

            // [段2] 播放——入队即返回
            IAudioService audio = null;
            if (!DataBox.TryResolve<IAudioService>(out audio) || audio == null)
            {
                result = "ERR|AUDIO_NO_SERVICE|宿主未注入 IAudioService";
                return false;
            }
            string enqueue = audio.Enqueue(key, outPath, -1f);
            if (enqueue != null && enqueue.Length > 0)
            {
                result = enqueue;
                return false;
            }
            result = "OK|" + outPath + speedNote;
            return true;
        }

        /// <summary>
        /// 根寻址展开——经工作区根表（WorkspaceConfig 全局绑定）；未绑定 / 未知根 id 原样返回。
        /// </summary>
        /// <param name="path">输入路径（根寻址形态或普通路径）</param>
        /// <returns>展开后的路径</returns>
        private static string ExpandPath(string path)
        {
            if (string.IsNullOrEmpty(path))
            {
                return "";
            }
            WorkspaceConfig workspace = null;
            if (DataBox.TryResolve<WorkspaceConfig>(out workspace) && workspace != null)
            {
                RootResolveResult rr = RootPathResolver.Expand(path, workspace.Roots);
                if (rr.Ok && rr.FullPath.Length > 0)
                {
                    return rr.FullPath;
                }
            }
            return path;
        }

        /// <summary>
        /// 归一取值——解析失败标记 / 空转空串。
        /// </summary>
        /// <param name="value">原始取值</param>
        /// <returns>归一后的字符串</returns>
        private static string Clean(string value)
        {
            if (value == null || value == "§PARSE_FAIL§")
            {
                return "";
            }
            return value;
        }

        /// <summary>
        /// 解析语速——非法 / 缺省回落 1.0。
        /// </summary>
        /// <param name="raw">原始取值</param>
        /// <returns>语速</returns>
        private static float ParseSpeed(string raw)
        {
            float parsed;
            if (raw != null && raw.Length > 0 && raw != "§PARSE_FAIL§" &&
                float.TryParse(raw, NumberStyles.Float, CultureInfo.InvariantCulture, out parsed) && parsed > 0f)
            {
                return parsed;
            }
            return 1.0f;
        }

        /// <summary>
        /// 生成产物路径——TTS 产物目录（数据根 Data/tts，由服务注入；未注入时落系统临时目录）。
        /// </summary>
        /// <returns>WAV 全路径</returns>
        private static string BuildOutPath()
        {
            ITtsService tts = null;
            string dir = "";
            if (DataBox.TryResolve<ITtsService>(out tts) && tts != null)
            {
                dir = tts.OutputDir;
            }
            if (dir.Length == 0)
            {
                dir = Path.Combine(Path.GetTempPath(), "cat-home4-tts");
            }
            if (!Directory.Exists(dir))
            {
                Directory.CreateDirectory(dir);
            }
            return Path.Combine(dir, "tts_" + DateTime.Now.ToString("yyyyMMdd_HHmmss_fff") + ".wav");
        }
    }
}
// #MAU_CHECKSUM:SHA256:315793BCA6081B71F1CF04AC862EA6D0DDC71860E71BE74BF62F77D495212D8B
