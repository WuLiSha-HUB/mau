using System;
using System.Collections.Generic;
using System.IO;

namespace Mau.Runtime
{
    /// <summary>
    /// WAV 装配器——把多段 WAV 按顺序拼成单文件（统一 44.1 kHz / 16bit / 立体声）。
    /// 纯 BCL 实现：只解析 RIFF 容器，不引第三方音频库（A350 音频退场后拼接能力的替代）。
    /// 单声道源复制到双声道；采样率不同则线性插值重采样；位深非 16 或非 PCM 一律明示拒绝。
    /// </summary>
    public static class WavAssembler
    {
        /// <summary>
        /// 目标采样率——与既有播报产物口径一致（44.1 kHz）。
        /// </summary>
        private const int TargetSampleRate = 44100;

        /// <summary>
        /// 目标声道数——固定立体声。
        /// </summary>
        private const int TargetChannels = 2;

        /// <summary>
        /// WAV 头长度——标准 PCM 44 字节头。
        /// </summary>
        private const int HeaderBytes = 44;

        /// <summary>
        /// PCM 格式码——RIFF fmt 块里的 audioFormat 取值（1 = 线性 PCM）。
        /// </summary>
        private const ushort PcmFormat = 1;

        /// <summary>
        /// 拼接——按播放顺序读入各段并转换到目标格式，写成单文件。
        /// </summary>
        /// <param name="paths">源 WAV 路径（按播放顺序；不存在的段直接失败，不静默跳过）</param>
        /// <param name="outPath">目标 WAV 路径（覆盖写；目录不存在则创建）</param>
        /// <returns>成功空串；失败 "ERR|CODE|说明"</returns>
        public static string Join(string[] paths, string outPath)
        {
            if (paths == null || paths.Length == 0)
            {
                return "ERR|BAD_ARGS|paths 为空";
            }
            if (outPath == null || outPath.Length == 0)
            {
                return "ERR|BAD_ARGS|outPath 为空";
            }
            // [段1] 逐段读入并转换
            List<short[]> segments = new List<short[]>();
            int total = 0;
            for (int i = 0; i < paths.Length; i = i + 1)
            {
                short[] pcm = new short[0];
                string readError = ReadAsTarget(paths[i], out pcm);
                if (readError.Length > 0)
                {
                    return readError;
                }
                if (pcm.Length > 0)
                {
                    segments.Add(pcm);
                    total = total + pcm.Length;
                }
            }
            if (total <= 0)
            {
                return "ERR|JOIN_EMPTY|所有源段均为空";
            }
            // [段2] 合并成一条
            short[] merged = new short[total];
            int offset = 0;
            for (int i = 0; i < segments.Count; i = i + 1)
            {
                Array.Copy(segments[i], 0, merged, offset, segments[i].Length);
                offset = offset + segments[i].Length;
            }
            // [段3] 写成单文件
            try
            {
                string? dir = Path.GetDirectoryName(outPath);
                if (dir != null && dir.Length > 0 && !Directory.Exists(dir))
                {
                    Directory.CreateDirectory(dir);
                }
                WriteWav(outPath, merged);
            }
            catch (Exception ex)
            {
                return "ERR|JOIN_WRITE_FAIL|写出失败: " + ex.Message;
            }
            return "";
        }

        /// <summary>
        /// 读入单段并转换到目标格式——产出交错样本（L R L R …）。
        /// </summary>
        /// <param name="path">源路径</param>
        /// <param name="target">输出：目标格式样本</param>
        /// <returns>错误文本（空=成功）</returns>
        private static string ReadAsTarget(string path, out short[] target)
        {
            target = new short[0];
            if (path == null || path.Length == 0)
            {
                return "ERR|BAD_ARGS|源路径为空";
            }
            if (!File.Exists(path))
            {
                return "ERR|JOIN_NOT_FOUND|源文件不存在: " + path;
            }
            byte[] bytes = new byte[0];
            try
            {
                bytes = File.ReadAllBytes(path);
            }
            catch (Exception ex)
            {
                return "ERR|JOIN_READ_FAIL|读取失败: " + ex.Message + " | " + path;
            }
            short[] src = new short[0];
            int sampleRate = 0;
            int channels = 0;
            string parseError = Parse(bytes, path, out src, out sampleRate, out channels);
            if (parseError.Length > 0)
            {
                return parseError;
            }
            target = ToTarget(src, sampleRate, channels);
            return "";
        }

        /// <summary>
        /// RIFF 解析——取出 16bit PCM 样本与格式参数；结构与支持面不符时报具名错误。
        /// </summary>
        /// <param name="bytes">文件字节</param>
        /// <param name="path">源路径（错误定位用）</param>
        /// <param name="samples">输出：原始交错样本</param>
        /// <param name="sampleRate">输出：采样率</param>
        /// <param name="channels">输出：声道数</param>
        /// <returns>错误文本（空=成功）</returns>
        private static string Parse(byte[] bytes, string path, out short[] samples, out int sampleRate, out int channels)
        {
            samples = new short[0];
            sampleRate = 0;
            channels = 0;
            if (bytes == null || bytes.Length < HeaderBytes)
            {
                return "ERR|JOIN_BAD_WAV|文件过短: " + path;
            }
            if (!Match(bytes, 0, "RIFF") || !Match(bytes, 8, "WAVE"))
            {
                return "ERR|JOIN_BAD_WAV|不是 RIFF/WAVE 容器: " + path;
            }
            bool hasFormat = false;
            ushort audioFormat = 0;
            ushort bitsPerSample = 0;
            int dataOffset = -1;
            int dataLength = 0;
            // [段1] 遍历 chunk——fmt 与 data 各自定位
            int pos = 12;
            while (pos + 8 <= bytes.Length)
            {
                uint declared = ReadUInt32(bytes, pos + 4);
                int body = pos + 8;
                if (Match(bytes, pos, "fmt "))
                {
                    if (body + 16 > bytes.Length)
                    {
                        return "ERR|JOIN_BAD_WAV|fmt 块被截断: " + path;
                    }
                    audioFormat = ReadUInt16(bytes, body);
                    channels = ReadUInt16(bytes, body + 2);
                    sampleRate = (int)ReadUInt32(bytes, body + 4);
                    bitsPerSample = ReadUInt16(bytes, body + 14);
                    hasFormat = true;
                }
                else if (Match(bytes, pos, "data"))
                {
                    dataOffset = body;
                    long remain = bytes.Length - body;
                    dataLength = declared > remain ? (int)remain : (int)declared;
                    break;
                }
                long next = (long)body + declared + (declared % 2);
                if (next <= pos || next > bytes.Length)
                {
                    break;
                }
                pos = (int)next;
            }
            if (!hasFormat)
            {
                return "ERR|JOIN_BAD_WAV|缺少 fmt 块: " + path;
            }
            if (dataOffset < 0)
            {
                return "ERR|JOIN_BAD_WAV|缺少 data 块: " + path;
            }
            if (audioFormat != PcmFormat)
            {
                return "ERR|JOIN_UNSUPPORTED|非 PCM 编码（audioFormat=" + audioFormat.ToString() + "）: " + path;
            }
            if (bitsPerSample != 16)
            {
                return "ERR|JOIN_UNSUPPORTED|仅支持 16bit 源（当前 " + bitsPerSample.ToString() + "bit）: " + path;
            }
            if (channels <= 0 || sampleRate <= 0)
            {
                return "ERR|JOIN_BAD_WAV|格式参数非法: " + path;
            }
            if (dataLength <= 0)
            {
                return "";
            }
            // [段2] data 块 → 16bit 样本
            int frameBytes = channels * 2;
            int frames = dataLength / frameBytes;
            if (frames <= 0)
            {
                return "";
            }
            samples = new short[frames * channels];
            int baseOffset = dataOffset;
            for (int i = 0; i < samples.Length; i = i + 1)
            {
                int off = baseOffset + i * 2;
                samples[i] = (short)(bytes[off] | (bytes[off + 1] << 8));
            }
            return "";
        }

        /// <summary>
        /// 转换到目标格式——单声道复制成双声道，采样率不同走线性插值。
        /// </summary>
        /// <param name="src">源交错样本</param>
        /// <param name="sampleRate">源采样率</param>
        /// <param name="channels">源声道数</param>
        /// <returns>目标交错样本（44.1 kHz / 2ch）</returns>
        private static short[] ToTarget(short[] src, int sampleRate, int channels)
        {
            if (src == null || src.Length == 0)
            {
                return new short[0];
            }
            if (channels <= 0)
            {
                channels = 1;
            }
            int srcFrames = src.Length / channels;
            if (srcFrames <= 0)
            {
                return new short[0];
            }
            bool sameRate = sampleRate == TargetSampleRate;
            int dstFrames = srcFrames;
            if (!sameRate)
            {
                dstFrames = (int)Math.Round(srcFrames * (double)TargetSampleRate / sampleRate);
            }
            if (dstFrames <= 0)
            {
                return new short[0];
            }
            short[] dst = new short[dstFrames * TargetChannels];
            for (int i = 0; i < dstFrames; i = i + 1)
            {
                double pos = i;
                if (!sameRate)
                {
                    pos = i * (double)sampleRate / TargetSampleRate;
                }
                int index0 = (int)pos;
                if (index0 >= srcFrames)
                {
                    index0 = srcFrames - 1;
                }
                int index1 = index0 + 1;
                if (index1 >= srcFrames)
                {
                    index1 = srcFrames - 1;
                }
                double frac = pos - index0;
                for (int c = 0; c < TargetChannels; c = c + 1)
                {
                    int sourceChannel = c;
                    if (sourceChannel >= channels)
                    {
                        sourceChannel = 0;
                    }
                    short v0 = src[index0 * channels + sourceChannel];
                    short v1 = src[index1 * channels + sourceChannel];
                    int value = (int)Math.Round(v0 + (v1 - v0) * frac);
                    dst[i * TargetChannels + c] = (short)value;
                }
            }
            return dst;
        }

        /// <summary>
        /// 写出标准 PCM WAV——44 字节头 + 交错样本。
        /// </summary>
        /// <param name="outPath">目标路径</param>
        /// <param name="samples">交错样本（44.1 kHz / 2ch）</param>
        private static void WriteWav(string outPath, short[] samples)
        {
            int dataBytes = samples.Length * 2;
            byte[] buffer = new byte[HeaderBytes + dataBytes];
            WriteAscii(buffer, 0, "RIFF");
            WriteUInt32(buffer, 4, (uint)(HeaderBytes - 8 + dataBytes));
            WriteAscii(buffer, 8, "WAVE");
            WriteAscii(buffer, 12, "fmt ");
            WriteUInt32(buffer, 16, 16);
            WriteUInt16(buffer, 20, PcmFormat);
            WriteUInt16(buffer, 22, TargetChannels);
            WriteUInt32(buffer, 24, TargetSampleRate);
            WriteUInt32(buffer, 28, (uint)(TargetSampleRate * TargetChannels * 2));
            WriteUInt16(buffer, 32, (ushort)(TargetChannels * 2));
            WriteUInt16(buffer, 34, 16);
            WriteAscii(buffer, 36, "data");
            WriteUInt32(buffer, 40, (uint)dataBytes);
            for (int i = 0; i < samples.Length; i = i + 1)
            {
                buffer[HeaderBytes + i * 2] = (byte)(samples[i] & 0xFF);
                buffer[HeaderBytes + i * 2 + 1] = (byte)((samples[i] >> 8) & 0xFF);
            }
            File.WriteAllBytes(outPath, buffer);
        }

        /// <summary>
        /// 比较 4 字节标识。
        /// </summary>
        /// <param name="buffer">字节数组</param>
        /// <param name="offset">偏移</param>
        /// <param name="token">四字符标识</param>
        /// <returns>是否匹配</returns>
        private static bool Match(byte[] buffer, int offset, string token)
        {
            if (offset + 4 > buffer.Length)
            {
                return false;
            }
            for (int i = 0; i < 4; i = i + 1)
            {
                if (buffer[offset + i] != (byte)token[i])
                {
                    return false;
                }
            }
            return true;
        }

        /// <summary>
        /// 写四字符标识。
        /// </summary>
        /// <param name="buffer">字节数组</param>
        /// <param name="offset">偏移</param>
        /// <param name="token">四字符标识</param>
        private static void WriteAscii(byte[] buffer, int offset, string token)
        {
            for (int i = 0; i < 4; i = i + 1)
            {
                buffer[offset + i] = (byte)token[i];
            }
        }

        /// <summary>
        /// 读小端 uint32。
        /// </summary>
        /// <param name="buffer">字节数组</param>
        /// <param name="offset">偏移</param>
        /// <returns>取值</returns>
        private static uint ReadUInt32(byte[] buffer, int offset)
        {
            return (uint)(buffer[offset] | (buffer[offset + 1] << 8)
                          | (buffer[offset + 2] << 16) | (buffer[offset + 3] << 24));
        }

        /// <summary>
        /// 读小端 uint16。
        /// </summary>
        /// <param name="buffer">字节数组</param>
        /// <param name="offset">偏移</param>
        /// <returns>取值</returns>
        private static ushort ReadUInt16(byte[] buffer, int offset)
        {
            return (ushort)(buffer[offset] | (buffer[offset + 1] << 8));
        }

        /// <summary>
        /// 写小端 uint32。
        /// </summary>
        /// <param name="buffer">字节数组</param>
        /// <param name="offset">偏移</param>
        /// <param name="value">取值</param>
        private static void WriteUInt32(byte[] buffer, int offset, uint value)
        {
            buffer[offset] = (byte)(value & 0xFF);
            buffer[offset + 1] = (byte)((value >> 8) & 0xFF);
            buffer[offset + 2] = (byte)((value >> 16) & 0xFF);
            buffer[offset + 3] = (byte)((value >> 24) & 0xFF);
        }

        /// <summary>
        /// 写小端 uint16。
        /// </summary>
        /// <param name="buffer">字节数组</param>
        /// <param name="offset">偏移</param>
        /// <param name="value">取值</param>
        private static void WriteUInt16(byte[] buffer, int offset, ushort value)
        {
            buffer[offset] = (byte)(value & 0xFF);
            buffer[offset + 1] = (byte)((value >> 8) & 0xFF);
        }
    }
}
