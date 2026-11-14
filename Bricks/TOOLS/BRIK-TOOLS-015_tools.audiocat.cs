// ═══════════════════════════════════════════════════
// 积木: tools.audiocat
// ID:   BRIK-TOOLS-015
// 类别: TOOLS
// 作用: 返回 AudioCat 工具组全部工具（audio-play / audio-speak 2 件）的 OpenAI 兼容定义 JSON（宿主工具池拼装原料）
// 依赖: 无
// 引用: 无（纯文本常量）
// 原理: GetToolsJson() 返回本组工具定义整包 JSON——改描述只动本文件，重建即生效
// 常用: FL_AudioCat.GetToolsJson() 自曝调用面（IFlow 接口）
// ═══════════════════════════════════════════════════
namespace Mau.Bricks
{
    /// <summary>
    /// 工具定义积木——AudioCat 组工具定义 JSON（OpenAI 兼容拼装原料）
    /// </summary>
    public static class ToolsAudiocatBrick
    {
        /// <summary>
        /// 获取本组工具定义 JSON——{"group":"AudioCat","tools":[{name,description,parameters}]}
        /// </summary>
        /// <returns>工具定义 JSON 字符串</returns>
        public static string GetToolsJson()
        {
            return "{\"group\":\"AudioCat\",\"tools\":[" +
                "{\"name\":\"audio-play\",\"description\":\"播放本地 WAV 音频文件——同 key 的音频按先后串行播放、不同 key 并发；path 必填，key 用于串行分组（省略则所有音频默认串行），volume 0~1 可选（省略=用全局配置音量）\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\",\"description\":\"WAV 文件路径\"},\"key\":{\"type\":\"string\",\"description\":\"串行分组键（同 key 排队；省略 = 默认串行组）\"},\"volume\":{\"type\":\"number\",\"description\":\"会话音量 0~1（省略=用全局配置 audio.volume）\"}},\"required\":[\"path\"]}}," +
                "{\"name\":\"audio-speak\",\"description\":\"文本转语音并播放——调云端 TTS 合成 WAV 后立即入队播放；text 必填（播报稿规范化：技术缩写拆成大写单字母加空格，如 C U D A、L L M；非数量数字写汉字，如 三零八零、一点八点一四九；标识符去下划线、数字转汉字，如 zf 零七二；链接与网址一律改口语说明、不要念 URL 原文；不要写 Markdown 符号），voice（stella 女声 / tide 男声，省略用服务默认）、key（播放串行分组）可选。speed 参数保留兼容但云端不支持语速，传入即忽略；合成同步等待约 1~3 秒\",\"parameters\":{\"type\":\"object\",\"properties\":{\"text\":{\"type\":\"string\",\"description\":\"要合成的文本\"},\"voice\":{\"type\":\"string\",\"description\":\"云端音色（stella 女声 / tide 男声；省略=服务默认）\"},\"speed\":{\"type\":\"number\",\"description\":\"已忽略（云端 TTS 不支持语速调节）\"},\"key\":{\"type\":\"string\",\"description\":\"播放串行分组键（省略=默认串行组）\"}},\"required\":[\"text\"]}}" +
                "]}";
        }
    }
}
// #MAU_CHECKSUM:SHA256:BE26F5C0D996CCD60DEFFD67003E9A1B5912686FCCC26EAF7D598E6237D0A470
