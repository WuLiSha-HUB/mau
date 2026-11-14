namespace Mau.Contracts
{
    /// <summary>
    /// 语音合成服务——文本转 WAV 文件（云端 TTS，无本地运行时依赖）。
    /// 契约面只用 BCL 类型（string）——实现（HTTP 客户端 / 音频字节处理）不泄漏到此。
    /// </summary>
    public interface ITtsService
    {
        /// <summary>
        /// 产物目录——语音 WAV 固定落在数据根 Data/tts（前端点播端点与轮末总结共用同一处）。
        /// 空串表示宿主未完成注入（调用方应按「目录不可用」处理并出声）。
        /// </summary>
        string OutputDir { get; }

        /// <summary>
        /// 合成——把文本写成一个 WAV 文件（16 位 PCM；采样率由服务端决定）。
        /// </summary>
        /// <param name="text">待合成文本（非空）</param>
        /// <param name="voice">音色名（空 = 服务默认音色）</param>
        /// <param name="outPath">产物 WAV 全路径（父目录不存在则创建）</param>
        /// <returns>成功返回空串；失败返回 "ERR|CODE|说明"（可见，不静默）</returns>
        string Synthesize(string text, string voice, string outPath);
    }
}
