using System;
using System.Collections.Generic;
using System.Text;
using System.Text.Json;
using Mau.Runtime;

namespace CatHome4.QQ
{
    /// <summary>
    /// QQ /info 解码器——把 info 工具返回的分类 JSON 块投影为 Markdown（可见根走表格）。
    /// 定位：info 返回体以 LLM 可读为第一目的（分类 JSON 块）；QQ 侧是人读通道，故在此做一次投影，
    /// 不回改工具返回体本身。纯函数——零状态零 IO；解析失败原样返回（失败可见，不静默丢内容）。
    /// </summary>
    internal static class QqInfoFormatter
    {
        /// <summary>
        /// info 分类 JSON 块 → Markdown。
        /// </summary>
        /// <param name="infoJson">info 工具返回体（整块 JSON）</param>
        /// <returns>Markdown 文本；空输入 → 空串；非 JSON / 解析失败 → 原文</returns>
        internal static string ToMarkdown(string infoJson)
        {
            if (infoJson == null || infoJson.Length == 0)
            {
                return "";
            }
            JsonElement root;
            if (!TryParseObject(infoJson, out root))
            {
                return infoJson;
            }
            StringBuilder sb = new StringBuilder();
            // [段1] 键值列表——版本(含编译时刻) / 当前时间 / LLM / 本地端点 / 前文 / 加载包 / QQBot
            AppendKeyValue(sb, "版本", VersionText(Obj(root, "version")));
            AppendKeyValue(sb, "当前时间", Str(Obj(root, "time"), "now"));
            AppendKeyValue(sb, "LLM", LlmText(Obj(root, "llm")));
            // 本地端点——分类对象存在才输出（键缺失 = 无该分类，不写「未监听」；对象在但地址空 = 未监听）
            JsonElement endpoint = Obj(root, "endpoint");
            if (endpoint.ValueKind == JsonValueKind.Object)
            {
                AppendKeyValue(sb, "本地端点", EndpointText(endpoint));
            }
            JsonElement tokens = Obj(root, "tokens");
            string ctx = Num(tokens, "context");
            if (ctx.Length > 0)
            {
                // 前文长度 = 请求级实时值；最近前文变动时刻（绝对毫秒戳）在此格式化为「距今」——展示层各自格式化
                string ctxLine = ctx + " tokens";
                string ago = AgoText(NumLong(tokens, "lastContextChangeAt"));
                if (ago.Length > 0)
                {
                    ctxLine = ctxLine + " · 前文变动 " + ago;
                }
                AppendKeyValue(sb, "前文", ctxLine);
            }
            AppendKeyValue(sb, "加载包", PacksText(root));
            AppendKeyValue(sb, "QQBot", Str(Obj(root, "qqbot"), "usage"));
            // [段2] 可见根——表格
            AppendRootsTable(sb, root);
            return sb.ToString().TrimEnd('\n');
        }

        // ═══════════════════════════════════════════
        // 各分类投影
        // ═══════════════════════════════════════════

        /// <summary>版本分类——版本号 + 编译时刻（与当前时间分列两行）。</summary>
        private static string VersionText(JsonElement version)
        {
            string ver = Str(version, "version");
            string build = Str(version, "build");
            if (ver.Length == 0)
            {
                return "";
            }
            if (build.Length == 0)
            {
                return ver;
            }
            return ver + " · 编译 " + build;
        }

        /// <summary>LLM 分类——协议 · 主机 · model=… · 来源。</summary>
        private static string LlmText(JsonElement llm)
        {
            List<string> parts = new List<string>();
            AddIfAny(parts, Str(llm, "protocol"));
            AddIfAny(parts, Str(llm, "host"));
            string model = Str(llm, "model");
            if (model.Length > 0)
            {
                parts.Add("model=" + model);
            }
            AddIfAny(parts, Str(llm, "source"));
            return string.Join(" · ", parts);
        }

        /// <summary>本地端点分类——对话页 + 管理面板（皆未监听时显式标注）。</summary>
        private static string EndpointText(JsonElement endpoint)
        {
            List<string> parts = new List<string>();
            string chat = Str(endpoint, "chat");
            if (chat.Length > 0)
            {
                parts.Add("对话 " + chat);
            }
            string panel = Str(endpoint, "panel");
            if (panel.Length > 0)
            {
                parts.Add("管理面板 " + panel);
            }
            if (parts.Count == 0)
            {
                return "（未监听）";
            }
            return string.Join(" · ", parts);
        }

        /// <summary>加载包分类——key(描述) 逐项连接（desc 空则只列 key）。</summary>
        private static string PacksText(JsonElement root)
        {
            JsonElement packs;
            if (root.ValueKind != JsonValueKind.Object || !root.TryGetProperty("packs", out packs) || packs.ValueKind != JsonValueKind.Array)
            {
                return "";
            }
            List<string> parts = new List<string>();
            foreach (JsonElement item in packs.EnumerateArray())
            {
                string key = Str(item, "key");
                if (key.Length == 0)
                {
                    continue;
                }
                string desc = Str(item, "desc");
                if (desc.Length > 0)
                {
                    parts.Add(key + "(" + desc + ")");
                }
                else
                {
                    parts.Add(key);
                }
            }
            return string.Join(" · ", parts);
        }

        /// <summary>可见根分类——Markdown 表格（根 / 读写 / 说明；表格前留空行成段）。</summary>
        private static void AppendRootsTable(StringBuilder sb, JsonElement root)
        {
            JsonElement roots;
            if (root.ValueKind != JsonValueKind.Object || !root.TryGetProperty("roots", out roots) || roots.ValueKind != JsonValueKind.Array)
            {
                return;
            }
            if (roots.GetArrayLength() == 0)
            {
                return;
            }
            sb.Append('\n');
            sb.Append("**可见根**\n\n");
            sb.Append("| 根 | 读写 | 说明 |\n");
            sb.Append("| --- | --- | --- |\n");
            foreach (JsonElement item in roots.EnumerateArray())
            {
                string access = "ro";
                JsonElement writable;
                if (item.ValueKind == JsonValueKind.Object && item.TryGetProperty("writable", out writable) && writable.ValueKind == JsonValueKind.True)
                {
                    access = "rw";
                }
                sb.Append("| ");
                sb.Append(Cell(Str(item, "id")));
                sb.Append(" | ");
                sb.Append(access);
                sb.Append(" | ");
                sb.Append(Cell(Str(item, "note")));
                sb.Append(" |\n");
            }
        }

        // ═══════════════════════════════════════════
        // 共用小件
        // ═══════════════════════════════════════════

        /// <summary>键值行——「- **键** 值」；值空不写该行（不产空行）。</summary>
        private static void AppendKeyValue(StringBuilder sb, string key, string value)
        {
            if (value == null || value.Length == 0)
            {
                return;
            }
            sb.Append("- **");
            sb.Append(key);
            sb.Append("** ");
            sb.Append(value);
            sb.Append('\n');
        }

        /// <summary>序列——非空才追加。</summary>
        private static void AddIfAny(List<string> parts, string value)
        {
            if (value != null && value.Length > 0)
            {
                parts.Add(value);
            }
        }

        /// <summary>表格单元格——竖线转义 + 换行折叠；空值显式占位（不产空格子）。</summary>
        private static string Cell(string s)
        {
            if (s == null || s.Length == 0)
            {
                return "—";
            }
            return s.Replace("|", "\\|").Replace("\r", "").Replace("\n", " ");
        }

        /// <summary>取字符串字段——非对象 / 缺键 / 类型不符 → 空串。</summary>
        private static string Str(JsonElement obj, string key)
        {
            if (obj.ValueKind != JsonValueKind.Object)
            {
                return "";
            }
            JsonElement el;
            if (!obj.TryGetProperty(key, out el) || el.ValueKind != JsonValueKind.String)
            {
                return "";
            }
            string s = el.GetString();
            if (s == null)
            {
                return "";
            }
            return s;
        }

        /// <summary>取整数数值字段——非数值 → 空串。</summary>
        private static string Num(JsonElement obj, string key)
        {
            if (obj.ValueKind != JsonValueKind.Object)
            {
                return "";
            }
            JsonElement el;
            if (!obj.TryGetProperty(key, out el) || el.ValueKind != JsonValueKind.Number)
            {
                return "";
            }
            long v;
            if (!el.TryGetInt64(out v))
            {
                return "";
            }
            return v.ToString();
        }

        /// <summary>取长整数数值字段——非数值 / 缺键 → 0。</summary>
        private static long NumLong(JsonElement obj, string key)
        {
            if (obj.ValueKind != JsonValueKind.Object)
            {
                return 0;
            }
            JsonElement el;
            if (!obj.TryGetProperty(key, out el) || el.ValueKind != JsonValueKind.Number)
            {
                return 0;
            }
            long v;
            if (!el.TryGetInt64(out v))
            {
                return 0;
            }
            return v;
        }

        /// <summary>距今文本——绝对毫秒戳 → 人读时长（刚刚 / N 分钟前 / N 小时前 / N 天前）；0 或负值 → 空串（不显示）。</summary>
        private static string AgoText(long ms)
        {
            if (ms <= 0)
            {
                return "";
            }
            long delta = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds() - ms;
            if (delta < 0)
            {
                delta = 0;
            }
            long sec = delta / 1000;
            if (sec < 60)
            {
                return "刚刚";
            }
            long min = sec / 60;
            if (min < 60)
            {
                return min.ToString() + " 分钟前";
            }
            long hour = min / 60;
            if (hour < 24)
            {
                return hour.ToString() + " 小时前";
            }
            return (hour / 24).ToString() + " 天前";
        }

        /// <summary>取子对象——非对象 → Undefined 元素（下游 Str / Num 一律空串）。</summary>
        private static JsonElement Obj(JsonElement obj, string key)
        {
            JsonElement el;
            if (obj.ValueKind == JsonValueKind.Object && obj.TryGetProperty(key, out el) && el.ValueKind == JsonValueKind.Object)
            {
                return el;
            }
            return default(JsonElement);
        }

        /// <summary>整块解析——仅接受 JSON 对象（数组 / 标量 / 损坏 → false，调用方原样返回）。</summary>
        private static bool TryParseObject(string json, out JsonElement root)
        {
            root = default(JsonElement);
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(json))
                {
                    if (doc.RootElement.ValueKind != JsonValueKind.Object)
                    {
                        return false;
                    }
                    root = doc.RootElement.Clone();
                    return true;
                }
            }
            catch (Exception)
            {
                return false;
            }
        }
    }
}
