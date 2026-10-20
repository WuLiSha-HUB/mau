using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using Mau.Runtime;

namespace CatHome4.Admin
{
    /// <summary>
    /// Program 管理端点面分部——受控根固定命名表与写入校验（design-ch4-workspace §三 / design-ch4-cat-admin §十）。
    /// 固定命名根（5 个）：id + note + writable 由系统固定，管理员面只能改 path；其余根自由增删改。
    /// 判定三处同源——服务读面 fixed 标记 / 写入校验 / 前端渲染。
    /// </summary>
    internal static partial class AdminService
    {
        /// <summary>
        /// 固定命名根定义——id / note / writable 三列固定（note ≤20 字）。
        /// </summary>
        internal sealed class FixedRootDef
        {
            /// <summary>根标识（小写）</summary>
            public string Id;

            /// <summary>固定认路注释（≤20 字）</summary>
            public string Note;

            /// <summary>固定读写标志</summary>
            public bool Writable;
        }

        /// <summary>
        /// 固定命名根表——单一真相源（读面标记 / 写面校验 / 前端渲染同源）。
        /// </summary>
        internal static readonly FixedRootDef[] FixedRoots = new FixedRootDef[]
        {
            new FixedRootDef() { Id = "workspace", Note = "系统工作区（必选）", Writable = true },
            new FixedRootDef() { Id = "mau", Note = "Mau 仓库（工具组仓库根）", Writable = true },
            new FixedRootDef() { Id = "mauout", Note = "运行实例部署区（重启目标）", Writable = true },
            new FixedRootDef() { Id = "data", Note = "CH4 应用数据（appdata）", Writable = false },
            new FixedRootDef() { Id = "db", Note = "数据库（Smriti 条目库）", Writable = false }
        };

        // [段1] 归一与判定
        /// <summary>
        /// 根 id 归一——去空白 + 大写转小写（写面统一小写；读面按小写识别）。
        /// </summary>
        /// <param name="id">原始根标识</param>
        /// <returns>归一后标识（空输入返回空串）</returns>
        internal static string NormalizeRootId(string id)
        {
            if (id == null)
            {
                return "";
            }
            return id.Trim().ToLowerInvariant();
        }

        /// <summary>
        /// 根 id 字符集校验——仅小写字母 + 数字（下划线退役；大写由归一化处理）。
        /// </summary>
        /// <param name="id">归一后的根标识</param>
        /// <returns>true=合法</returns>
        internal static bool IsValidRootIdChars(string id)
        {
            if (id == null || id.Length == 0)
            {
                return false;
            }
            for (int i = 0; i < id.Length; i = i + 1)
            {
                char ch = id[i];
                bool ok = (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9');
                if (!ok)
                {
                    return false;
                }
            }
            return true;
        }

        /// <summary>
        /// PS 内置驱动器保留名——PowerShell 会话内建（根 id 重名则 New-PSDrive 失败，根寻址失效）。
        /// 来源：Get-PSDrive 实测并集（5.1 + 7；2026-10-01）；盘符单字母（A-Z）另行判定。
        /// </summary>
        private static readonly string[] ReservedPsDriveNames = new string[]
        {
            "alias", "cert", "env", "function", "hkcu", "hklm", "variable", "wsman", "temp"
        };

        /// <summary>
        /// PS 保留驱动器名判定——固定名（小写比对）或单字母盘符（a-z）。
        /// </summary>
        /// <param name="id">根标识（任意大小写）</param>
        /// <returns>true=保留名（应拒绝）</returns>
        internal static bool IsReservedPsDriveName(string id)
        {
            string norm = NormalizeRootId(id);
            if (norm.Length == 1)
            {
                char ch = norm[0];
                if (ch >= 'a' && ch <= 'z')
                {
                    return true;
                }
            }
            for (int i = 0; i < ReservedPsDriveNames.Length; i = i + 1)
            {
                if (string.Equals(ReservedPsDriveNames[i], norm, StringComparison.Ordinal))
                {
                    return true;
                }
            }
            return false;
        }

        /// <summary>
        /// 固定根定义查找——判定与校验共用。
        /// </summary>
        /// <param name="id">根标识（任意大小写）</param>
        /// <returns>定义；非固定根 null</returns>
        internal static FixedRootDef FindFixedRoot(string id)
        {
            string norm = NormalizeRootId(id);
            for (int i = 0; i < FixedRoots.Length; i = i + 1)
            {
                if (string.Equals(FixedRoots[i].Id, norm, StringComparison.Ordinal))
                {
                    return FixedRoots[i];
                }
            }
            return null;
        }

        // [段2] 写入校验
        /// <summary>
        /// 受控根提交校验——id 字符集（小写字母数字）+ 非 PS 内置驱动器名 + 重名（忽略大小写）+ path 绝对且存在 + 固定根齐备。
        /// 语义：缺任一固定根 → 拒绝（不静默造根）；固定根 note/writable 由系统权威覆盖——提交值不参与判定（存量配置缺 note / 直调 API 传错值一律归一，不构成保存死锁）。
        /// </summary>
        /// <param name="rootsIn">提交条目（Id 须已归一为小写）</param>
        /// <param name="existingPaths">现存根 path 映射（id → path；与提交值等值的存量根跳过存在性校验——目录离线/搬迁不构成改根死锁）</param>
        /// <returns>错误文案（空 = 通过）</returns>
        private static string ValidateRootsInput(List<WorkspaceRootInput> rootsIn, Dictionary<string, string> existingPaths)
        {
            List<string> ids = new List<string>();
            for (int i = 0; i < rootsIn.Count; i = i + 1)
            {
                WorkspaceRootInput input = rootsIn[i];
                if (input.Id.Length == 0)
                {
                    return "roots[" + i.ToString() + "] id 为空";
                }
                if (!IsValidRootIdChars(input.Id))
                {
                    return "roots[" + i.ToString() + "] id 非法（仅小写字母/数字）: " + input.Id;
                }
                if (IsReservedPsDriveName(input.Id))
                {
                    return "roots[" + i.ToString() + "] id 与 PowerShell 内置驱动器同名（ps 根寻址会失效）: " + input.Id;
                }
                bool dup = false;
                for (int k = 0; k < ids.Count; k = k + 1)
                {
                    if (string.Equals(ids[k], input.Id, StringComparison.OrdinalIgnoreCase))
                    {
                        dup = true;
                        break;
                    }
                }
                if (dup)
                {
                    return "roots id 重复（大小写不敏感）: " + input.Id;
                }
                ids.Add(input.Id);
                if (input.Path.Length == 0)
                {
                    return "roots[" + i.ToString() + "] path 为空";
                }
                if (!Path.IsPathRooted(input.Path))
                {
                    return "roots[" + i.ToString() + "] path 非绝对路径: " + input.Path;
                }
                if (!Directory.Exists(input.Path))
                {
                    // 存量容留——与现存同 id 根 path 等值（规范化后）时不要求可达：目录离线/搬迁后仍可原样提交以移除或改路径
                    string oldPath = null;
                    if (existingPaths != null)
                    {
                        existingPaths.TryGetValue(input.Id, out oldPath);
                    }
                    bool retained = oldPath != null && oldPath.Length > 0 && string.Equals(Mau.Runtime.WorkspaceConfig.NormalizeRoot(input.Path), Mau.Runtime.WorkspaceConfig.NormalizeRoot(oldPath), StringComparison.OrdinalIgnoreCase);
                    if (!retained)
                    {
                        return "roots[" + i.ToString() + "] 目录不存在: " + input.Path;
                    }
                }
                FixedRootDef def = FindFixedRoot(input.Id);
                if (def != null)
                {
                    // 固定列系统权威——note / writable 取定义值（不以提交值为准）；存量配置缺 note、直调 API 传错值一律归一，不构成保存死锁
                    input.Note = def.Note;
                    input.Writable = def.Writable;
                }
            }
            for (int f = 0; f < FixedRoots.Length; f = f + 1)
            {
                bool found = false;
                for (int k = 0; k < ids.Count; k = k + 1)
                {
                    if (string.Equals(ids[k], FixedRoots[f].Id, StringComparison.OrdinalIgnoreCase))
                    {
                        found = true;
                        break;
                    }
                }
                if (!found)
                {
                    return "缺少系统根 " + FixedRoots[f].Id + "（固定命名根不可移除）";
                }
            }
            return "";
        }

        // [段3] db 根自愈与落盘出口
        /// <summary>
        /// db 受控根自愈（A266）——配置缺 db 根或目录不可达时补建，内存与 workspace.json 一并就位。
        /// 语义：幂等——已就位返回空串；库根固定为 &lt;数据根&gt;/Data/db（只读，仅供库工具寻路）。
        /// 🔴 失败具名返回（不静默）：目录建不出来整个动作中止；落盘失败时内存已就位，返回值说明分叉。
        /// </summary>
        /// <param name="workspace">已加载的受控根配置（就地更新 Roots 数组）</param>
        /// <param name="dataRoot">数据根（三级锚定的父级）</param>
        /// <returns>动作说明（空 = 无需动作；非空 = 已发生的动作或失败原因）</returns>
        internal static string EnsureDbRoot(WorkspaceConfig workspace, string dataRoot)
        {
            if (workspace == null)
            {
                return "db 根自愈跳过：受控根配置未加载";
            }
            FixedRootDef def = FindFixedRoot("db");
            if (def == null)
            {
                return "db 根自愈跳过：固定根表未登记 db（实现缺陷）";
            }
            string dbPath = Path.Combine(dataRoot, "Data", "db");
            try
            {
                Directory.CreateDirectory(dbPath);
            }
            catch (Exception ex)
            {
                return "db 根目录创建失败：" + dbPath + " | " + ex.Message;
            }
            string dbNorm = WorkspaceConfig.NormalizeRoot(dbPath);
            int existing = -1;
            for (int i = 0; i < workspace.Roots.Length; i = i + 1)
            {
                if (string.Equals(workspace.Roots[i].Id, "db", StringComparison.OrdinalIgnoreCase))
                {
                    existing = i;
                    break;
                }
            }
            if (existing >= 0)
            {
                string oldNorm = "";
                try
                {
                    oldNorm = WorkspaceConfig.NormalizeRoot(workspace.Roots[existing].Path);
                }
                catch (Exception)
                {
                    oldNorm = "";
                }
                if (string.Equals(oldNorm, dbNorm, StringComparison.OrdinalIgnoreCase))
                {
                    return "";
                }
            }
            // 内存面先就位——工具寻路不因落盘失败而不可用（分叉在返回值里出声）
            List<WorkspaceConfig.RootEntry> entries = new List<WorkspaceConfig.RootEntry>();
            for (int i = 0; i < workspace.Roots.Length; i = i + 1)
            {
                if (string.Equals(workspace.Roots[i].Id, "db", StringComparison.OrdinalIgnoreCase))
                {
                    continue;
                }
                entries.Add(workspace.Roots[i]);
            }
            WorkspaceConfig.RootEntry dbEntry = new WorkspaceConfig.RootEntry();
            dbEntry.Id = def.Id;
            dbEntry.Path = dbNorm;
            dbEntry.Writable = def.Writable;
            dbEntry.Note = def.Note;
            entries.Add(dbEntry);
            workspace.Roots = entries.ToArray();
            // 落盘面——以磁盘原文为基并入 db（加载会跳过不可达根，全量回写会静默丢根）
            string wsPath = Path.Combine(dataRoot, "Data", "config", "workspace.json");
            string writeErr = MergeDbRootIntoWorkspace(wsPath, dbEntry);
            if (writeErr.Length > 0)
            {
                return "db 根已补入内存，但 workspace.json 写回失败：" + writeErr;
            }
            return "db 根已补建：" + dbNorm + "（只读）";
        }
        /// <summary>
        /// 把 db 根并入 workspace.json 磁盘原文——其余根条目与 inject 段原样保留，原子写。
        /// 不以加载后的内存 roots 为基：加载会跳过不可达根，全量回写会静默丢根。
        /// </summary>
        /// <param name="wsPath">workspace.json 绝对路径</param>
        /// <param name="dbEntry">待并入的 db 根条目</param>
        /// <returns>错误文案（空 = 成功）</returns>
        private static string MergeDbRootIntoWorkspace(string wsPath, WorkspaceConfig.RootEntry dbEntry)
        {
            List<string> rootItems = new List<string>();
            string injectJson = "[]";
            if (File.Exists(wsPath))
            {
                try
                {
                    using (JsonDocument old = JsonUtil.ParseStrict(File.ReadAllText(wsPath)))
                    {
                        JsonElement injectEl;
                        if (old.RootElement.TryGetProperty("inject", out injectEl))
                        {
                            injectJson = injectEl.GetRawText();
                        }
                        JsonElement rootsEl;
                        if (old.RootElement.TryGetProperty("roots", out rootsEl) && rootsEl.ValueKind == JsonValueKind.Array)
                        {
                            for (int i = 0; i < rootsEl.GetArrayLength(); i = i + 1)
                            {
                                JsonElement item = rootsEl[i];
                                string id = "";
                                JsonElement idEl;
                                if (item.ValueKind == JsonValueKind.Object && item.TryGetProperty("id", out idEl) && idEl.ValueKind == JsonValueKind.String)
                                {
                                    id = idEl.GetString();
                                }
                                if (string.Equals(NormalizeRootId(id), "db", StringComparison.OrdinalIgnoreCase))
                                {
                                    continue;
                                }
                                rootItems.Add(item.GetRawText());
                            }
                        }
                    }
                }
                catch (Exception ex)
                {
                    return "workspace.json 读取失败（拒绝覆盖）: " + ex.Message;
                }
            }
            rootItems.Add(JsonUtil.Object(("id", dbEntry.Id), ("path", dbEntry.Path.Replace('\\', '/')), ("writable", dbEntry.Writable), ("note", dbEntry.Note)));
            string wsJson = JsonUtil.Object(("roots", JsonUtil.RawArray(rootItems.ToArray())), ("inject", JsonUtil.Raw(injectJson)));
            try
            {
                ConfigStore.AtomicWrite(wsPath, wsJson);
            }
            catch (Exception ex)
            {
                return "落盘失败: " + ex.Message;
            }
            return "";
        }

        /// <summary>
        /// 受控根落盘——roots 全量写回 workspace.json（保留 inject 段；原子写）。
        /// 单一写出口：管理端点保存与 db 根自愈共用（避免两套写路径分叉）。
        /// </summary>
        /// <param name="wsPath">workspace.json 绝对路径</param>
        /// <param name="rootsIn">待写入根条目（Id 已归一）</param>
        /// <returns>错误文案（空 = 成功）</returns>
        private static string WriteWorkspaceRoots(string wsPath, List<WorkspaceRootInput> rootsIn)
        {
            string injectJson = "[]";
            if (File.Exists(wsPath))
            {
                try
                {
                    using (JsonDocument old = JsonUtil.ParseStrict(File.ReadAllText(wsPath)))
                    {
                        JsonElement injectEl;
                        if (old.RootElement.TryGetProperty("inject", out injectEl))
                        {
                            injectJson = injectEl.GetRawText();
                        }
                    }
                }
                catch (Exception ex)
                {
                    // 读不出旧 inject 即拒绝覆盖——覆盖会静默丢掉注入清单
                    return "注入清单读取失败（拒绝覆盖，避免丢 inject）: " + ex.Message;
                }
            }
            System.Collections.Generic.List<string> rootItems = new System.Collections.Generic.List<string>();
            for (int i = 0; i < rootsIn.Count; i++)
            {
                WorkspaceRootInput input = rootsIn[i];
                string norm = WorkspaceConfig.NormalizeRoot(input.Path);
                if (input.Note.Length > 0)
                {
                    rootItems.Add(JsonUtil.Object(("id", input.Id), ("path", norm.Replace('\\', '/')), ("writable", input.Writable), ("note", input.Note)));
                }
                else
                {
                    rootItems.Add(JsonUtil.Object(("id", input.Id), ("path", norm.Replace('\\', '/')), ("writable", input.Writable)));
                }
            }
            string wsJson = JsonUtil.Object(("roots", JsonUtil.RawArray(rootItems.ToArray())), ("inject", JsonUtil.Raw(injectJson)));
            try
            {
                ConfigStore.AtomicWrite(wsPath, wsJson);
            }
            catch (Exception ex)
            {
                return "落盘失败: " + ex.Message;
            }
            return "";
        }
    }
}
