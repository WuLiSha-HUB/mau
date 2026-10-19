using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;

namespace Mau.Runtime
{
    /// <summary>
    /// 工作区配置——workspace.json 解析（受控根 + 注入清单）。design-ch4-workspace §三/§五——Mau 基座标准能力。
    /// 语义：文件缺失 / roots 为空 = 默认单根兜底（全局白名单语义——空=未配置白名单）；JSON 损坏 / 读取失败 → 备份坏档 + 默认单根 + LoadDegrades；
    /// 根不可达分级：常驻根（workspace/runtime）先重建目录、失败回落数据根；其余根跳过 + LoadWarnings 告警。
    /// 🔴 A260 起本类永不抛——降级事实经 LoadDegrades 交给宿主登记（配置 / 环境异常不得阻断启动）。
    /// </summary>
    public sealed class WorkspaceConfig
    {
        /// <summary>
        /// 认路注释最大长度——超长截断（前端限 20 字；宿主侧兜底不信任前端）
        /// </summary>
        public const int NoteMaxLength = 20;

        /// <summary>
        /// 受控根条目——id + 路径 + 可写标志（writable=false 拒绝写操作）
        /// </summary>
        public sealed class RootEntry
        {
            /// <summary>
            /// 根标识——命名空间寻址前缀（ccbp:L1/Tree.md 的 ccbp）
            /// </summary>
            public string Id = "";

            /// <summary>
            /// 规范绝对路径
            /// </summary>
            public string Path = "";

            /// <summary>
            /// 是否允许写入——false 时 file.write/append/replace/delete 拒绝
            /// </summary>
            public bool Writable;

            /// <summary>
            /// 认路注释——给 LLM 的语义提示（≤20 字，超长截断；info 输出可见）
            /// </summary>
            public string Note = "";
        }

        /// <summary>
        /// 注入清单条目——文件寻址 + 可选标志 + 来源标注
        /// </summary>
        public sealed class InjectEntry
        {
            /// <summary>
            /// 文件寻址——id:relative（映射受控根）或纯路径
            /// </summary>
            public string File = "";

            /// <summary>
            /// 缺失是否可跳过——true 缺失仅警告（inject.missing 审计）
            /// </summary>
            public bool Optional;

            /// <summary>
            /// 来源标注——持久区可见（"系统前文来自哪"）
            /// </summary>
            public string Label = "";
        }

        /// <summary>
        /// 受控根列表——有序；首根为相对路径解析基准（FindOwningRoot 最长前缀匹配）
        /// </summary>
        public RootEntry[] Roots = new RootEntry[0];

        /// <summary>
        /// 注入清单——有序（按列表顺序拼接）
        /// </summary>
        public InjectEntry[] Inject = new InjectEntry[0];

        /// <summary>
        /// 加载告警——不可达的非常驻根（跳过不注册；宿主启动后落 WORKSPACE 审计，不静默）
        /// </summary>
        public List<string> LoadWarnings = new List<string>();
        /// <summary>
        /// 加载降级——已在加载期自动处理的配置 / 环境问题（格式 code|state|detail）。
        /// 与 LoadWarnings 的分工：Warnings 是「跳过的非常驻根」（无需处理）；Degrades 是「触发过兜底动作」（须登记进启动健康面）。
        /// </summary>
        public List<string> LoadDegrades = new List<string>();

        // [段1] 加载
        /// <summary>
        /// 从 workspace.json 加载——文件缺失或 roots 为空返回默认配置（单根 defaultRoot 可写 + 空注入）。
        /// 🔴 永不抛：读取失败 / JSON 损坏 → 备份坏档 + 默认单根；根问题逐项降级，事实记入 LoadDegrades / LoadWarnings。
        /// </summary>
        /// <param name="path">workspace.json 路径</param>
        /// <param name="defaultRoot">缺文件时的兜底根（数据根，可写）</param>
        /// <returns>工作区配置</returns>
        public static WorkspaceConfig Load(string path, string defaultRoot)
        {
            WorkspaceConfig cfg = new WorkspaceConfig();
            // [段0] 文件缺失 / 路径为空 = 未配置白名单 → 默认单根（既有语义）
            if (path == null || path.Length == 0 || !File.Exists(path))
            {
                cfg.Roots = SingleRoot(defaultRoot);
                cfg.Inject = new InjectEntry[0];
                return cfg;
            }
            // [段0b] 读取失败（权限 / 占用）→ 降级为默认单根（不阻断启动）
            string text;
            try
            {
                text = File.ReadAllText(path);
            }
            catch (Exception ex)
            {
                cfg.Roots = SingleRoot(defaultRoot);
                cfg.Inject = new InjectEntry[0];
                cfg.LoadDegrades.Add("CFG_UNREADABLE|fallback|" + path + " | " + ex.Message);
                return cfg;
            }
            // [段0c] JSON 损坏 → 备份坏档 + 默认单根（不阻断启动）
            JsonDocument doc;
            try
            {
                doc = JsonUtil.ParseStrict(text);
            }
            catch (Exception ex)
            {
                string backup = ConfigFileGuard.BackupCorrupt(path);
                cfg.Roots = SingleRoot(defaultRoot);
                cfg.Inject = new InjectEntry[0];
                string backupNote = "";
                if (backup.Length > 0)
                {
                    backupNote = "（坏档备份 " + backup + "）";
                }
                cfg.LoadDegrades.Add("CFG_CORRUPT|fallback|" + path + backupNote + " | " + ex.Message);
                return cfg;
            }
            using (doc)
            {
                JsonElement root = doc.RootElement;
                // [段1a] roots 解析——逐项容错：非法项跳过；常驻根不可达先重建、再回落数据根
                List<RootEntry> roots = new List<RootEntry>();
                JsonElement rootsEl;
                if (root.TryGetProperty("roots", out rootsEl) && rootsEl.ValueKind == JsonValueKind.Array)
                {
                    for (int i = 0; i < rootsEl.GetArrayLength(); i = i + 1)
                    {
                        JsonElement item = rootsEl[i];
                        string id = GetProp(item, "id");
                        string raw = GetProp(item, "path");
                        bool writable = true;
                        JsonElement wEl;
                        if (item.TryGetProperty("writable", out wEl) && wEl.ValueKind == JsonValueKind.False)
                        {
                            writable = false;
                        }
                        if (id.Length == 0 || raw.Length == 0)
                        {
                            cfg.LoadDegrades.Add("ROOT_ITEM_INVALID|empty|roots[" + i.ToString() + "] 缺 id/path");
                            continue;
                        }
                        string norm;
                        try
                        {
                            norm = NormalizeRoot(raw);
                        }
                        catch (Exception ex)
                        {
                            cfg.LoadDegrades.Add("ROOT_ITEM_INVALID|empty|roots[" + i.ToString() + "] 路径非法：" + raw + " | " + ex.Message);
                            continue;
                        }
                        if (!Directory.Exists(norm))
                        {
                            bool resident = string.Equals(id, "workspace", StringComparison.OrdinalIgnoreCase)
                                || string.Equals(id, "runtime", StringComparison.OrdinalIgnoreCase);
                            if (resident)
                            {
                                // 常驻根：先尝试重建目录（换机 / 误删的常见形态）
                                bool rebuilt = false;
                                try
                                {
                                    Directory.CreateDirectory(norm);
                                    rebuilt = true;
                                }
                                catch (Exception)
                                {
                                    rebuilt = false;
                                }
                                if (rebuilt)
                                {
                                    cfg.LoadDegrades.Add("ROOT_REBUILT|rebuilt|常驻根不存在已重建：" + id + " → " + norm);
                                }
                                else
                                {
                                    // 重建失败 → 回落数据根（可写兜底）；数据根也不可用则保留原值（工具面按缺失根出声）
                                    string fallback = "";
                                    try
                                    {
                                        fallback = NormalizeRoot(defaultRoot);
                                    }
                                    catch (Exception)
                                    {
                                        fallback = "";
                                    }
                                    if (fallback.Length > 0)
                                    {
                                        cfg.LoadDegrades.Add("ROOT_UNREACHABLE|fallback|常驻根不可达（" + id + " → " + norm + "），已回落 " + fallback);
                                        norm = fallback;
                                    }
                                    else
                                    {
                                        cfg.LoadDegrades.Add("ROOT_UNREACHABLE|unavailable|常驻根不可达且无可写回落：" + id + " → " + norm);
                                    }
                                }
                            }
                            else
                            {
                                // 分级容错：非常驻根不可达 → 跳过 + 告警（宿主照常启动，管理员可进面板修正路径）
                                cfg.LoadWarnings.Add("根不可达已跳过: " + id + " → " + norm);
                                continue;
                            }
                        }
                        string note = GetProp(item, "note");
                        roots.Add(new RootEntry() { Id = id, Path = norm, Writable = writable, Note = TruncateNote(note) });
                    }
                }
                if (roots.Count == 0)
                {
                    // 空 roots = 未配置白名单——默认单根 workspace 兜底（同文件缺失语义；FileSystemService 需至少一个根）
                    cfg.Roots = SingleRoot(defaultRoot);
                    cfg.Inject = new InjectEntry[0];
                    return cfg;
                }
                cfg.Roots = roots.ToArray();
                // [段1b] inject 解析
                List<InjectEntry> inject = new List<InjectEntry>();
                JsonElement injectEl;
                if (root.TryGetProperty("inject", out injectEl) && injectEl.ValueKind == JsonValueKind.Array)
                {
                    for (int i = 0; i < injectEl.GetArrayLength(); i = i + 1)
                    {
                        JsonElement item = injectEl[i];
                        string file = GetProp(item, "file");
                        if (file.Length == 0)
                        {
                            continue;
                        }
                        bool optional = false;
                        JsonElement optEl;
                        if (item.TryGetProperty("optional", out optEl) && optEl.ValueKind == JsonValueKind.True)
                        {
                            optional = true;
                        }
                        string label = GetProp(item, "label");
                        inject.Add(new InjectEntry() { File = file, Optional = optional, Label = label });
                    }
                }
                cfg.Inject = inject.ToArray();
            }
            return cfg;
        }
        /// <summary>
        /// 构造默认单根配置——文件缺失 / 配置损坏 / roots 全空时的统一兜底（workspace 根 = 数据根，可写）。
        /// </summary>
        /// <param name="defaultRoot">兜底根（数据根；不可规范化时原样使用）</param>
        /// <returns>单根数组</returns>
        private static RootEntry[] SingleRoot(string defaultRoot)
        {
            RootEntry entry = new RootEntry();
            entry.Id = "workspace";
            entry.Writable = true;
            string norm = "";
            try
            {
                norm = NormalizeRoot(defaultRoot);
            }
            catch (Exception)
            {
                if (defaultRoot != null)
                {
                    norm = defaultRoot;
                }
            }
            entry.Path = norm;
            RootEntry[] roots = new RootEntry[] { entry };
            return roots;
        }

        // [段2] 解析辅助
        /// <summary>
        /// 截断认路注释——去空白 + 超长截断到 NoteMaxLength（宿主侧兜底；前端另有 maxlength 限制）
        /// </summary>
        /// <param name="note">原始注释</param>
        /// <returns>截断后注释（空=无注释）</returns>
        public static string TruncateNote(string note)
        {
            if (string.IsNullOrEmpty(note))
            {
                return "";
            }
            string trimmed = note.Trim();
            if (trimmed.Length <= NoteMaxLength)
            {
                return trimmed;
            }
            return trimmed.Substring(0, NoteMaxLength);
        }

        /// <summary>
        /// 读取字符串属性——缺字段返回空串（防御式）
        /// </summary>
        /// <param name="obj">JSON 元素</param>
        /// <param name="prop">属性名</param>
        /// <returns>属性值</returns>
        private static string GetProp(JsonElement obj, string prop)
        {
            JsonElement value;
            if (obj.TryGetProperty(prop, out value) && value.ValueKind == JsonValueKind.String)
            {
                string? got = value.GetString();
                if (got != null)
                {
                    return got;
                }
            }
            return "";
        }

        /// <summary>
        /// 解析注入文件寻址——id:relative 映射受控根；纯路径 GetFullPath 直用（清单是管理员静态动作——不强制 roots 内，但约定 id: 命名空间）
        /// </summary>
        /// <param name="entry">注入条目</param>
        /// <returns>规范绝对路径</returns>
        public string ResolveInjectFile(InjectEntry entry)
        {
            string file = entry.File;
            // A257——复用公共服务展开段（点一·不判归属：清单是管理员静态动作，允许纯路径直用）
            RootResolveResult rr = RootPathResolver.Expand(file, Roots, Environment.CurrentDirectory);
            if (!rr.Ok)
            {
                throw new InvalidDataException("注入清单寻址非法: " + rr.ErrorCode + " | " + rr.Message);
            }
            return rr.FullPath;
        }

        /// <summary>
        /// 规范化根路径——与 FileSystemService 同规（GetFullPath + 去掉尾部分隔符）
        /// </summary>
        /// <param name="root">原始根路径</param>
        /// <returns>规范绝对路径</returns>
        public static string NormalizeRoot(string root)
        {
            if (string.IsNullOrWhiteSpace(root))
            {
                throw new ArgumentException("Workspace root is empty.", "root");
            }
            string full = Path.GetFullPath(root);
            if (full.EndsWith(Path.DirectorySeparatorChar.ToString(), StringComparison.Ordinal) && full.Length > 3)
            {
                full = full.TrimEnd(Path.DirectorySeparatorChar);
            }
            return full;
        }
    }
}