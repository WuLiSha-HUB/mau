// NRT 抑制（CSharp exp §1.16）——落盘面：路径构造与文件句柄在分支内判空；
// 逐处加 ? 会把 append 链淹掉，语义判空在消费点（空路径 = 落档不可用，出声）。
#nullable disable warnings

using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using System.Text.Json;
using Mau.Runtime;

namespace Mau.Providers
{
    /// <summary>
    /// QQ 消息留档（A379）——三处落盘面：
    /// ① 群全量：`Data/qq/groups/&lt;group_openid&gt;/&lt;yyyy-MM-dd&gt;.jsonl`（该群当天收到的全部消息）
    /// ② @ 收件：`Data/qq/bots/&lt;appId&gt;/mention-in.jsonl`（收到的 @ 消息）
    /// ③ @ 发出：`Data/qq/bots/&lt;appId&gt;/mention-out.jsonl`（发出的回复与主动发言）
    /// 另有 `Data/qq/bots/&lt;appId&gt;/groups.jsonl` 记本 bot 见过的群（去重读，供 botinfo 使用）。
    /// 🔴 每次 append 开-写-关（同视图层 JSONL 口径），不持有长句柄；跨日按行内时间戳归日。
    /// </summary>
    public sealed class QqBotStore
    {
        /// <summary>落盘编码——无 BOM UTF-8（逐行追加，BOM 会污染首行）</summary>
        private static readonly UTF8Encoding _fileEncoding = new UTF8Encoding(false);

        /// <summary>数据根下的 QQ 留档根目录（空=未注入，落档停用并出声）</summary>
        private readonly string _root;

        /// <summary>落档根目录路径（空串=未注入）</summary>
        public string RootPath
        {
            get { return _root; }
        }

        /// <summary>
        /// 构造留档面。
        /// </summary>
        /// <param name="qqRoot">留档根目录（数据根下 Data/qq 的绝对路径；空=停用）</param>
        public QqBotStore(string qqRoot)
        {
            _root = qqRoot ?? "";
        }

        /// <summary>
        /// 追加一条群全量消息。
        /// </summary>
        /// <param name="groupOpenId">群 OpenID（目录名）</param>
        /// <param name="dayStamp">归日戳（yyyy-MM-dd，取自事件 timestamp 的本机时区日期）</param>
        /// <param name="line">已序列化的一行 JSON（不含换行）</param>
        /// <returns>空串成功；"ERR|QUOTE|说明"失败（不抛异常）</returns>
        public string AppendGroupMessage(string groupOpenId, string dayStamp, string line)
        {
            if (_root.Length == 0)
            {
                return "ERR|STORE_NOT_READY|留档根未注入";
            }
            if (string.IsNullOrEmpty(groupOpenId) || string.IsNullOrEmpty(dayStamp))
            {
                return "ERR|BAD_ARGS|群 OpenID 或归日戳为空";
            }
            string dir = Path.Combine(_root, "groups", SafeSegment(groupOpenId));
            string path = Path.Combine(dir, SafeSegment(dayStamp) + ".jsonl");
            return AppendLine(path, line);
        }

        /// <summary>
        /// 追加一条 @ 收件记录。
        /// </summary>
        /// <param name="appId">机器人 AppID（目录名）</param>
        /// <param name="line">已序列化的一行 JSON</param>
        /// <returns>空串成功；"ERR|…|说明"失败</returns>
        public string AppendMentionIn(string appId, string line)
        {
            return AppendBotLine(appId, "mention-in.jsonl", line);
        }

        /// <summary>
        /// 追加一条 @ 发出记录（命令回复 / 主动发言 / 提示回复共用）。
        /// </summary>
        /// <param name="appId">机器人 AppID</param>
        /// <param name="line">已序列化的一行 JSON</param>
        /// <returns>空串成功；"ERR|…|说明"失败</returns>
        public string AppendMentionOut(string appId, string line)
        {
            return AppendBotLine(appId, "mention-out.jsonl", line);
        }

        /// <summary>
        /// 记录本 bot 见过的群（同一群重复见到时追加，读取侧去重）。
        /// </summary>
        /// <param name="appId">机器人 AppID</param>
        /// <param name="groupOpenId">群 OpenID</param>
        /// <param name="displayName">群显示名（事件不带群名时为空）</param>
        /// <returns>空串成功；"ERR|…|说明"失败</returns>
        public string AppendKnownGroup(string appId, string groupOpenId, string displayName)
        {
            if (string.IsNullOrEmpty(groupOpenId))
            {
                return "";
            }
            string line = "{\"at\":" + JsonUtil.Str(DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss"))
                + ",\"groupOpenId\":" + JsonUtil.Str(groupOpenId)
                + ",\"displayName\":" + JsonUtil.Str(displayName ?? "") + "}";
            return AppendBotLine(appId, "groups.jsonl", line);
        }

        /// <summary>
        /// 读取本 bot 见过的群（按 groupOpenId 去重，保留首次见到顺序）。
        /// </summary>
        /// <param name="appId">机器人 AppID</param>
        /// <returns>群 OpenID 列表（文件缺失返回空列表）</returns>
        public List<string> ReadKnownGroups(string appId)
        {
            List<string> result = new List<string>();
            if (_root.Length == 0 || string.IsNullOrEmpty(appId))
            {
                return result;
            }
            string path = Path.Combine(_root, "bots", SafeSegment(appId), "groups.jsonl");
            string[] lines;
            try
            {
                if (!File.Exists(path))
                {
                    return result;
                }
                lines = File.ReadAllLines(path, _fileEncoding);
            }
            catch (Exception ex)
            {
                LogStore.Add("QQBot", 2, "QQBot | 读群清单失败: " + ex.Message + " | " + path, "QQBOT");
                return result;
            }
            HashSet<string> seen = new HashSet<string>(StringComparer.Ordinal);
            for (int i = 0; i < lines.Length; i = i + 1)
            {
                string raw = lines[i];
                if (raw == null || raw.Trim().Length == 0)
                {
                    continue;
                }
                string gid = "";
                try
                {
                    using (JsonDocument d = JsonUtil.ParseStrict(raw))
                    {
                        JsonElement e;
                        if (d.RootElement.TryGetProperty("groupOpenId", out e))
                        {
                            gid = e.GetString() ?? "";
                        }
                    }
                }
                catch (Exception ex)
                {
                    LogStore.Add("QQBot", 2, "QQBot | 群清单行解析失败（跳过）: " + ex.Message, "QQBOT");
                    continue;
                }
                if (gid.Length > 0 && seen.Add(gid))
                {
                    result.Add(gid);
                }
            }
            return result;
        }

        /// <summary>
        /// 追加一行到本 bot 目录下的指定文件。
        /// </summary>
        /// <param name="appId">机器人 AppID</param>
        /// <param name="fileName">文件名</param>
        /// <param name="line">一行 JSON</param>
        /// <returns>空串成功；"ERR|…|说明"失败</returns>
        private string AppendBotLine(string appId, string fileName, string line)
        {
            if (_root.Length == 0)
            {
                return "ERR|STORE_NOT_READY|留档根未注入";
            }
            if (string.IsNullOrEmpty(appId))
            {
                return "ERR|BAD_ARGS|AppID 为空";
            }
            string dir = Path.Combine(_root, "bots", SafeSegment(appId));
            return AppendLine(Path.Combine(dir, fileName), line);
        }

        /// <summary>
        /// 追加一行并落盘——目录按需创建；失败出声不抛。
        /// </summary>
        /// <param name="path">目标文件绝对路径</param>
        /// <param name="line">一行文本（不含换行）</param>
        /// <returns>空串成功；"ERR|WRITE_FAIL|说明"失败</returns>
        private static string AppendLine(string path, string line)
        {
            try
            {
                string dir = Path.GetDirectoryName(path);
                if (dir != null && dir.Length > 0 && !Directory.Exists(dir))
                {
                    Directory.CreateDirectory(dir);
                }
                File.AppendAllText(path, (line ?? "") + "\n", _fileEncoding);
                return "";
            }
            catch (Exception ex)
            {
                LogStore.Add("QQBot", 3, "QQBot | 留档写入失败: " + ex.Message + " | " + path, "QQBOT");
                return "ERR|WRITE_FAIL|" + ex.Message;
            }
        }

        /// <summary>
        /// 路径段消毒——OpenID / AppID 作为目录名，去掉路径分隔符与保留字符。
        /// </summary>
        /// <param name="segment">原始段</param>
        /// <returns>可作目录名的段</returns>
        private static string SafeSegment(string segment)
        {
            if (string.IsNullOrEmpty(segment))
            {
                return "unknown";
            }
            char[] bad = Path.GetInvalidFileNameChars();
            StringBuilder sb = new StringBuilder(segment.Length);
            for (int i = 0; i < segment.Length; i = i + 1)
            {
                char c = segment[i];
                bool invalid = false;
                for (int j = 0; j < bad.Length; j = j + 1)
                {
                    if (bad[j] == c)
                    {
                        invalid = true;
                        break;
                    }
                }
                if (invalid || c == '/' || c == '\\')
                {
                    sb.Append('_');
                }
                else
                {
                    sb.Append(c);
                }
            }
            return sb.ToString();
        }
    }
}
