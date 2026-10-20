using System;
using System.Collections.Generic;
using System.IO;
using Mau.Contracts;
using NAudio.Wave;

namespace Mau.Audio
{
    /// <summary>
    /// 音频播放服务——per-Key 串行 + 组间并发（投递即返回，只进不出；design-mau-audio §四）。
    /// 调度全在本类内部自洽：主线程只做「入队」一个原子动作，播完由 NAudio 回调驱动队列前进——
    /// 不碰 OA / CommandBus / FlowRunner（主线程守卫面）。
    /// </summary>
    public sealed class AudioService : IAudioService
    {
        /// <summary>
        /// 默认 Key——空 Key 归一（不带 Key 的所有音频自动串行）
        /// </summary>
        public const string DefaultKey = "_default";

        /// <summary>
        /// 调度锁——入队与回调（队列前进）共用的临界区
        /// </summary>
        private readonly object _gate = new object();

        /// <summary>
        /// 活跃 Key 集合——某 Key 在集合中即表示有音频正在播（同 Key 后续入队）
        /// </summary>
        private readonly HashSet<string> _playing = new HashSet<string>();

        /// <summary>
        /// 待播队列——Key → 同 Key 未起播的条目（先来后到）
        /// </summary>
        private readonly Dictionary<string, Queue<AudioItem>> _queues = new Dictionary<string, Queue<AudioItem>>();

        /// <summary>
        /// 活跃播放器表——Key → 当前播放器（防 GC；串联 reader 释放）
        /// </summary>
        private readonly Dictionary<string, WasapiPlayer> _players = new Dictionary<string, WasapiPlayer>();

        /// <summary>
        /// 活跃读取器表——Key → 当前 WAV 读取器
        /// </summary>
        private readonly Dictionary<string, WaveStream> _readers = new Dictionary<string, WaveStream>();

        /// <summary>
        /// 入队——主线程调用（积木转调）；起播或排队，立即返回。
        /// </summary>
        /// <param name="key">串行分组键（空 = 默认 Key）</param>
        /// <param name="path">WAV 文件路径</param>
        /// <param name="volume">会话音量（&lt; 0 = 用全局配置 audio.volume；≥ 0 = 覆盖）</param>
        /// <returns>""=已受理；"ERR|CODE|说明"=失败（可见）</returns>
        public string Enqueue(string key, string path, float volume)
        {
            if (path == null || path.Length == 0)
            {
                return "ERR|BAD_ARGS|path 为空";
            }
            // A258——路径权威落点：入口幂等展开根寻址（已解析绝对路径原样返回）；
            // 消费者不止积木一层（轮末总结经 ChatSession 直调），契约钉在服务自身
            string resolved = ResolvePath(path);
            if (!File.Exists(resolved))
            {
                return "ERR|NOT_FOUND|文件不存在: " + resolved;
            }
            string k = (key == null || key.Length == 0) ? DefaultKey : key;
            lock (_gate)
            {
                if (_playing.Contains(k))
                {
                    Queue<AudioItem> q;
                    if (!_queues.TryGetValue(k, out q))
                    {
                        q = new Queue<AudioItem>();
                        _queues[k] = q;
                    }
                    q.Enqueue(new AudioItem(resolved, volume));
                    return "";
                }
                _playing.Add(k);
                StartPlay(k, resolved, volume);
            }
            return "";
        }

        /// <summary>
        /// 活跃 Key 数——诊断哨兵
        /// </summary>
        /// <returns>当前有音频在播的 Key 数</returns>
        public int ActiveKeyCount()
        {
            lock (_gate)
            {
                return _playing.Count;
            }
        }

        /// <summary>
        /// 多段 WAV 拼接（A265）——按顺序读入，统一重采样到 44.1 kHz / 16bit / 立体声后写成单文件。
        /// 单声道源先转立体声再重采样；失败不中断既有播放面（返回 ERR 由调用方出声）。
        /// </summary>
        /// <param name="paths">源 WAV 路径（按播放顺序）</param>
        /// <param name="outPath">目标 WAV 路径（覆盖写；目录不存在则创建）</param>
        /// <returns>成功空串；失败 "ERR|CODE|说明"</returns>
        public string Join(string[] paths, string outPath)
        {
            if (paths == null || paths.Length == 0)
            {
                return "ERR|BAD_ARGS|paths 为空";
            }
            if (outPath == null || outPath.Length == 0)
            {
                return "ERR|BAD_ARGS|outPath 为空";
            }
            List<string> resolved = new List<string>();
            for (int i = 0; i < paths.Length; i = i + 1)
            {
                string one = ResolvePath(paths[i]);
                if (!File.Exists(one))
                {
                    return "ERR|NOT_FOUND|源文件不存在: " + one;
                }
                resolved.Add(one);
            }
            WaveFormat target = new WaveFormat(44100, 16, 2);
            try
            {
                string dir = Path.GetDirectoryName(outPath);
                if (dir != null && dir.Length > 0 && !Directory.Exists(dir))
                {
                    Directory.CreateDirectory(dir);
                }
                WriteJoined(resolved, outPath, target);
                return "";
            }
            catch (Exception ex)
            {
                return "ERR|JOIN_FAIL|拼接失败: " + ex.Message;
            }
        }

        /// <summary>
        /// 拼接写出体——逐段读入并转换到目标格式，字节直接追加（拼接的唯一 IO 落点）。
        /// </summary>
        /// <param name="resolved">已解析的源路径</param>
        /// <param name="outPath">目标路径</param>
        /// <param name="target">目标格式（44.1 kHz / 16bit / 2ch）</param>
        private static void WriteJoined(List<string> resolved, string outPath, WaveFormat target)
        {
            using (WaveFileWriter writer = new WaveFileWriter(outPath, target))
            {
                for (int i = 0; i < resolved.Count; i = i + 1)
                {
                    using (WaveFileReader reader = new WaveFileReader(resolved[i]))
                    {
                        WaveFormat src = reader.WaveFormat;
                        if (src.BitsPerSample != 16)
                        {
                            throw new InvalidOperationException("仅支持 16bit 源: " + resolved[i]);
                        }
                        byte[] raw = ReadAll(reader);
                        byte[] pcm = ToStereo44100(raw, src);
                        writer.Write(pcm, 0, pcm.Length);
                    }
                }
            }
        }

        /// <summary>
        /// 读完整个读取器——源文件体积小（播报音频），一次性读入便于重采样索引。
        /// </summary>
        /// <param name="reader">读取器</param>
        /// <returns>原始字节</returns>
        private static byte[] ReadAll(WaveFileReader reader)
        {
            long total = reader.Length;
            if (total <= 0)
            {
                return new byte[0];
            }
            byte[] raw = new byte[total];
            int got = 0;
            while (got < raw.Length)
            {
                int n = reader.Read(raw, got, raw.Length - got);
                if (n <= 0)
                {
                    break;
                }
                got = got + n;
            }
            return raw;
        }

        /// <summary>
        /// 任意 16bit 源 → 44.1 kHz / 16bit / 2ch PCM——单声道复制到双声道，采样率线性插值重采样。
        /// </summary>
        /// <param name="raw">源字节</param>
        /// <param name="src">源格式（16bit）</param>
        /// <returns>目标 PCM 字节</returns>
        private static byte[] ToStereo44100(byte[] raw, WaveFormat src)
        {
            int ch = src.Channels;
            if (ch <= 0)
            {
                ch = 1;
            }
            int frames = (raw.Length / 2) / ch;
            if (frames <= 0)
            {
                return new byte[0];
            }
            int dstFrames = (int)Math.Round(frames * 44100.0 / src.SampleRate);
            byte[] outp = new byte[dstFrames * 4];
            for (int i = 0; i < dstFrames; i = i + 1)
            {
                double pos = i * (double)src.SampleRate / 44100.0;
                int i0 = (int)pos;
                if (i0 >= frames)
                {
                    i0 = frames - 1;
                }
                int i1 = i0 + 1;
                if (i1 >= frames)
                {
                    i1 = frames - 1;
                }
                double frac = pos - i0;
                for (int c = 0; c < 2; c = c + 1)
                {
                    int sc = c < ch ? c : 0;
                    short v0 = ReadShort(raw, i0 * ch + sc);
                    short v1 = ReadShort(raw, i1 * ch + sc);
                    int v = (int)Math.Round(v0 + (v1 - v0) * frac);
                    int off = (i * 2 + c) * 2;
                    outp[off] = (byte)(v & 0xFF);
                    outp[off + 1] = (byte)((v >> 8) & 0xFF);
                }
            }
            return outp;
        }

        /// <summary>
        /// 读一个 16bit 小端样本。
        /// </summary>
        /// <param name="buf">字节缓冲</param>
        /// <param name="sampleIndex">样本下标</param>
        /// <returns>样本值</returns>
        private static short ReadShort(byte[] buf, int sampleIndex)
        {
            int off = sampleIndex * 2;
            if (off + 1 >= buf.Length)
            {
                return 0;
            }
            return (short)(buf[off] | (buf[off + 1] << 8));
        }

        /// <summary>
        /// 起播——构造读取器 + 播放器，注册停止回调；须在锁内调用（调用方持 _gate）
        /// </summary>
        /// <param name="key">分组键</param>
        /// <param name="path">WAV 路径</param>
        /// <param name="volume">会话音量</param>
        private void StartPlay(string key, string path, float volume)
        {
            WaveStream reader = null;
            WasapiPlayer player = null;
            try
            {
                reader = new WaveFileReader(path);
                player = new WasapiPlayerBuilder().Build();
                player.Init(reader);
                player.Volume = ResolveVolume(volume);
                player.PlaybackStopped += delegate (object sender, StoppedEventArgs e)
                {
                    OnPlaybackStopped(key);
                };
                _readers[key] = reader;
                _players[key] = player;
                player.Play();
            }
            catch (Exception ex)
            {
                // 起播失败——释放半成品 + 出声；该 Key 复位并尝试下一个（失败可见，不静默）
                LogFail(key, "起播失败: " + ex.Message);
                DisposeCurrent(key);
                _playing.Remove(key);
                Queue<AudioItem> q;
                if (_queues.TryGetValue(key, out q) && q.Count > 0)
                {
                    AudioItem next = q.Dequeue();
                    if (q.Count == 0)
                    {
                        _queues.Remove(key);
                    }
                    _playing.Add(key);
                    StartPlay(key, next.Path, next.Volume);
                }
            }
        }

        /// <summary>
        /// 播放停止回调——队列前进的驱动信号（非业务回执）；锁内取下一个或复位
        /// </summary>
        /// <param name="key">分组键</param>
        private void OnPlaybackStopped(string key)
        {
            lock (_gate)
            {
                DisposeCurrent(key);
                Queue<AudioItem> q;
                if (_queues.TryGetValue(key, out q) && q.Count > 0)
                {
                    AudioItem next = q.Dequeue();
                    if (q.Count == 0)
                    {
                        _queues.Remove(key);
                    }
                    StartPlay(key, next.Path, next.Volume);
                }
                else
                {
                    _queues.Remove(key);
                    _playing.Remove(key);
                }
            }
        }

        /// <summary>
        /// 释放该 Key 当前读取器与播放器——须在锁内调用
        /// </summary>
        /// <param name="key">分组键</param>
        private void DisposeCurrent(string key)
        {
            WasapiPlayer player;
            if (_players.TryGetValue(key, out player))
            {
                _players.Remove(key);
                try
                {
                    player.Dispose();
                }
                catch (Exception ex)
                {
                    LogFail(key, "播放器释放异常: " + ex.Message);
                }
            }
            WaveStream reader;
            if (_readers.TryGetValue(key, out reader))
            {
                _readers.Remove(key);
                try
                {
                    reader.Dispose();
                }
                catch (Exception ex)
                {
                    LogFail(key, "读取器释放异常: " + ex.Message);
                }
            }
        }
        /// <summary>
        /// 路径展开（幂等）——根寻址 &lt;根id&gt;:&lt;相对&gt; 映射受控根；已解析绝对路径 / 非根寻址原样返回。
        /// A258——路径权威落点在服务入口（消费者含积木与宿主直调两层）；根表未注入时原样返回（失败可见归下游 File.Exists）。
        /// </summary>
        /// <param name="path">输入路径（根寻址形态或普通路径）</param>
        /// <returns>展开后的路径</returns>
        private static string ResolvePath(string path)
        {
            Mau.Runtime.WorkspaceConfig workspace = null;
            if (Mau.Runtime.DataBox.TryResolve<Mau.Runtime.WorkspaceConfig>(out workspace) && workspace != null)
            {
                Mau.Runtime.RootResolveResult rr = Mau.Runtime.RootPathResolver.Expand(path, workspace.Roots);
                if (rr.Ok && rr.FullPath.Length > 0)
                {
                    return rr.FullPath;
                }
            }
            return path;
        }

        /// <summary>
        /// 解析音量——requested &lt; 0 表示未指定，采用全局配置 audio.volume（百分比→0~1 系数）；否则用传入值。
        /// 全局配置由宿主注入（DataBox.Bind&lt;ConfigStore&gt;）；未注入 / 读取失败回落 100%。
        /// </summary>
        /// <param name="requested">调用方音量（&lt; 0 = 用全局配置；≥ 0 = 覆盖）</param>
        /// <returns>会话音量系数（0~2）</returns>
        private static float ResolveVolume(float requested)
        {
            if (requested >= 0f)
            {
                return requested;
            }
            float pct = 100f;
            Mau.Runtime.ConfigStore store = null;
            if (Mau.Runtime.DataBox.TryResolve<Mau.Runtime.ConfigStore>(out store) && store != null)
            {
                string text = store.Get("audio.volume", "100");
                float parsed;
                if (float.TryParse(text, out parsed))
                {
                    pct = parsed;
                }
            }
            if (pct < 0f)
            {
                pct = 0f;
            }
            if (pct > 200f)
            {
                pct = 200f;
            }
            return pct / 100f;
        }

        /// <summary>
        /// 失败出声——失败必须可见（AGENTS §一-4）
        /// </summary>
        /// <param name="key">分组键</param>
        /// <param name="message">失败说明</param>
        private static void LogFail(string key, string message)
        {
            Mau.Runtime.LogStore.Add("Audio", 3, "[" + key + "] " + message, "AUDIO");
        }

        /// <summary>
        /// 待播条目——路径 + 音量
        /// </summary>
        private sealed class AudioItem
        {
            /// <summary>
            /// WAV 文件路径
            /// </summary>
            public readonly string Path;

            /// <summary>
            /// 会话音量（0~1）
            /// </summary>
            public readonly float Volume;

            /// <summary>
            /// 构造待播条目
            /// </summary>
            /// <param name="path">WAV 路径</param>
            /// <param name="volume">会话音量</param>
            public AudioItem(string path, float volume)
            {
                Path = path;
                Volume = volume;
            }
        }
    }
}
