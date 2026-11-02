using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using System.Threading;
using Mau.Runtime;
using Mau.Providers;
using CatHome4.Contracts;
using CatHome4.Core;

namespace CH4
{
    /// <summary>
    /// Program 路径解析面分部——仓库根探测/数据根解析/HTTP 端口解析。
    /// P7b partial 拆分——自 Program.cs 原样搬移，逻辑零改动。
    /// </summary>
    public static partial class Program
    {
        /// <summary>
        /// 仓库根探测——从当前目录向上找含 Mau.sln 的目录（部署区定位用；CatHome4 可从任意工作目录启动）
        /// </summary>
        /// <param name="startDir">起始目录</param>
        /// <returns>仓库根或空字符串</returns>
        private static string FindRepoRoot(string startDir)
        {
            string dir = new DirectoryInfo(startDir).FullName;
            while (true)
            {
                if (File.Exists(Path.Combine(dir, "Mau.sln")))
                {
                    return dir;
                }
                DirectoryInfo parentInfo = Directory.GetParent(dir);
                if (parentInfo == null)
                {
                    return "";
                }
                string parent = parentInfo.FullName;
                dir = parent;
            }
        }

        /// <summary>
        /// 数据根解析——三级锚定（design-ch4-release.md §三）：
        /// 1. CH4_DATA_ROOT 环境变量（显式覆盖——多实例/CI/自定义落位逃逸口）
        /// 2. 仓库根（找到 Mau.sln → 测试区 Data 隔离，可自由清）
        /// 3. %LOCALAPPDATA%/CatHome4（部署实例持久数据——secrets 不进漫游域）
        /// 返回值为 Data 的父级目录——调用方以 Path.Combine(dataRoot, "Data", ...) 拼实际路径。
        /// </summary>
        /// <returns>数据根目录</returns>
        private static string ResolveDataRoot()
        {
            // [段1] 显式覆盖——CH4_DATA_ROOT 环境变量（行业标准逃逸口）；不可用时降级到下一级锚点
            string envRoot = Environment.GetEnvironmentVariable("CH4_DATA_ROOT");
            if (envRoot != null && envRoot.Length > 0)
            {
                if (EnsureDataRoot(envRoot))
                {
                    return envRoot;
                }
                StartupDegradation.Report("DATA_ROOT_UNUSABLE", StartupDegradation.KindEnv,
                    "数据根（配置 / 会话 / 观测）", StartupDegradation.StateFallback,
                    "CH4_DATA_ROOT 不可用：" + envRoot,
                    "修正或清除 CH4_DATA_ROOT 环境变量后重启", "S0");
                BootLog.Add("S0", "CH4_DATA_ROOT 不可用，已回落下一级锚点：" + envRoot);
            }
            // [段2] 测试区隔离——仓库根（Data 挂仓库根，可自由清）
            string root = FindRepoRoot(AppContext.BaseDirectory);
            if (root.Length > 0)
            {
                return root;
            }
            // [段3] 机器级默认——部署实例持久数据
            return Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "CatHome4");
        }
        /// <summary>
        /// 数据根可用性探测——建 Data 子目录判定可写（成功即视为可用；失败不抛）。
        /// </summary>
        /// <param name="root">候选数据根（Data 的上级目录）</param>
        /// <returns>可用返回 true</returns>
        private static bool EnsureDataRoot(string root)
        {
            if (root == null || root.Trim().Length == 0)
            {
                return false;
            }
            try
            {
                Directory.CreateDirectory(Path.Combine(root, "Data"));
                return true;
            }
            catch (Exception)
            {
                return false;
            }
        }

        /// <summary>端口段——Bootstrap 段0 解析（机器级端口空间按区划分：开发区向下 / 部署区向上——A74）</summary>
        private static PortBand _portBand;

        /// <summary>
        /// 端口段解析——区域判定与数据根解析同源（FindRepoRoot 命中 = exe 位于仓库树内 = 开发区）。
        /// 开发区主端口 8079（端口族 8078 向下至 8070）、部署区主端口 8080（端口族 8081 向上至 8180）——
        /// 两区端口空间完全分离，开发区实例与部署区实例可同时运行。结论打印一行（启动观测：本实例落在哪个区段）。
        /// </summary>
        /// <returns>端口段</returns>
        private static PortBand ResolvePortBand()
        {
            bool devArea = FindRepoRoot(AppContext.BaseDirectory).Length > 0;
            PortBand band = PortBand.Resolve(devArea);
            Console.WriteLine("[CMD] 端口段 " + band.Area + " | 主端口 " + band.MainPort.ToString() + " | 端口族 " + band.FamilyFrom.ToString() + "→" + band.FamilyTo.ToString() + " | 前端测试 " + band.FrontendTestPort.ToString());
            return band;
        }

        /// <summary>
        /// HTTP 端口解析——显式 http.port 配置优先；缺省取区段主端口；非法/越界回退区段主端口（告警不静默）
        /// </summary>
        /// <param name="config">配置存储</param>
        /// <returns>监听端口</returns>
        private static int ResolveHttpPort(ConfigStore config)
        {
            int fallback = _portBand.MainPort;
            string raw = config.Get("http.port", "");
            if (raw.Length == 0)
            {
                return fallback;
            }
            int port;
            if (!int.TryParse(raw, out port))
            {
                port = 0;
            }
            if (port < 1024 || port > 65535)
            {
                Console.WriteLine("[CMD] http.port 配置非法(" + raw + ")——回退区段主端口 " + fallback.ToString());
                port = fallback;
            }
            return port;
        }

        /// <summary>
        /// 回收站根——数据根在受控根内且可写则用数据根；否则第一个可写根（回收站必须可写——P8.5 配置群）
        /// </summary>
        /// <param name="workspace">工作区配置</param>
        /// <param name="dataRoot">数据根</param>
        /// <returns>回收站基底目录</returns>
        private static string WorkspaceRecycleRoot(WorkspaceConfig workspace, string dataRoot)
        {
            for (int i = 0; i < workspace.Roots.Length; i++)
            {
                WorkspaceConfig.RootEntry entry = workspace.Roots[i];
                if (entry.Writable && string.Equals(entry.Path, dataRoot, StringComparison.OrdinalIgnoreCase))
                {
                    return entry.Path;
                }
            }
            for (int i = 0; i < workspace.Roots.Length; i++)
            {
                WorkspaceConfig.RootEntry entry = workspace.Roots[i];
                if (entry.Writable)
                {
                    return entry.Path;
                }
            }
            return dataRoot;
        }

        /// <summary>
        /// 可写根条目数组——cs.* 编码工具只碰可写根（只读知识根不参与项目扫描——P8.5 配置群；保留 id 供命名空间寻址）
        /// </summary>
        /// <param name="workspace">工作区配置</param>
        /// <returns>可写根条目数组</returns>
        private static WorkspaceConfig.RootEntry[] WritableRootPaths(WorkspaceConfig workspace)
        {
            List<WorkspaceConfig.RootEntry> entries = new List<WorkspaceConfig.RootEntry>();
            for (int i = 0; i < workspace.Roots.Length; i++)
            {
                if (workspace.Roots[i].Writable)
                {
                    entries.Add(workspace.Roots[i]);
                }
            }
            return entries.ToArray();
        }

        /// <summary>
        /// 前端 html 根解析——源码区优先（仓库根/CatHome4/html——唯一事实源，改即生效）；回退部署区（AppContext.BaseDirectory/html——发布包）。
        /// </summary>
        /// <returns>html 根目录</returns>
        internal static string ResolveHtmlRoot()
        {
            string root = FindRepoRoot(AppContext.BaseDirectory);
            if (root.Length > 0)
            {
                string src = System.IO.Path.Combine(root, "CatHome4", "html");
                if (System.IO.Directory.Exists(src))
                {
                    return src;
                }
            }
            return System.IO.Path.Combine(AppContext.BaseDirectory, "html");
        }

        /// <summary>
        /// schema 同步——模板为准（design-ch4-workspace §4.1）：运行区 schema.json 是派生副本，不是用户资产。
        /// 缺失 → 复制；存在但内容与模板不一致 → 备份 .bak 后整体覆盖；一致 → 零写入；模板两源皆缺 → 告警（不静默）。
        /// 模板源候选序：部署包区（AppContext.BaseDirectory/config/schema.json.example——deploy 段3b 复制）→ 仓库根（FindRepoRoot/config/schema.json.example——开发/测试区）。
        /// 调用点：Bootstrap 段2 末尾（ConfigSchema.Load 前；此时 LogStore 未 Configure——日志直打 Console 与早期 Bootstrap 风格一致）。
        /// </summary>
        /// <param name="configDir">Data/config 目录（绝对路径）</param>
        /// <param name="templatePath">schema 模板绝对路径（未找到为空串——出参，供声明通道写模板）</param>
        private static string EnsureSchemaSync(string configDir, out string templatePath)
        {
            templatePath = "";
            string target = Path.Combine(configDir, "schema.json");
            string template = "";
            string[] candidates = new string[]
            {
                Path.Combine(AppContext.BaseDirectory, "config", "schema.json.example"),
                Path.Combine(FindRepoRoot(AppContext.BaseDirectory), "config", "schema.json.example")
            };
            for (int i = 0; i < candidates.Length; i = i + 1)
            {
                if (File.Exists(candidates[i]))
                {
                    template = candidates[i];
                    break;
                }
            }
            templatePath = template;
            if (template.Length == 0)
            {
                Console.WriteLine("[CMD] schema 同步: 模板未找到——保持现有 schema（声明面可能过期，请检查部署包 config/）");
                return "schema.sync | 模板未找到 | 保持现有 schema（声明面可能过期）";
            }
            try
            {
                if (!File.Exists(target))
                {
                    Directory.CreateDirectory(configDir);
                    File.Copy(template, target, false);
                    Console.WriteLine("[CMD] schema 种子: " + template + " → " + target);
                    return "schema.sync | 首次种子 | " + template;
                }
                if (SameBytes(template, target))
                {
                    return "schema.sync | 已是最新（零写入）";
                }
                string backup = target + ".bak";
                File.Copy(target, backup, true);
                File.Copy(template, target, true);
                Console.WriteLine("[CMD] schema 同步: 模板更新已覆盖 " + target + "（旧文件备份 " + backup + "）");
                return "schema.sync | 模板更新已覆盖（旧文件备份 .bak）";
            }
            catch (Exception ex)
            {
                Console.WriteLine("[CMD] schema 同步失败: " + ex.Message);
                return "schema.sync | 失败: " + ex.Message;
            }
        }

        /// <summary>
        /// 字节级比对——schema 文件小，逐字节比较优于哈希（零额外依赖与临时文件）
        /// </summary>
        /// <param name="pathA">路径 A</param>
        /// <param name="pathB">路径 B</param>
        /// <returns>是否逐字节一致</returns>
        private static bool SameBytes(string pathA, string pathB)
        {
            byte[] a = File.ReadAllBytes(pathA);
            byte[] b = File.ReadAllBytes(pathB);
            if (a.Length != b.Length)
            {
                return false;
            }
            for (int i = 0; i < a.Length; i = i + 1)
            {
                if (a[i] != b[i])
                {
                    return false;
                }
            }
            return true;
        }

        /// <summary>
        /// 受控根启动对账（A153）——回显生效根（id=path·可写/只读），并检出**同 path 多根**：
        /// 路径等值时解析取首个等值匹配，其余根的 writable / note 声明不生效（原为静默）——此处具名两组根 id。
        /// </summary>
        /// <param name="workspace">工作区配置（workspace.json 解析结果）</param>
        /// <param name="conflict">输出：是否检出同 path 多根</param>
        /// <returns>一行对账文本（进启动审计）</returns>
        internal static string RootsAuditNote(WorkspaceConfig workspace, out bool conflict)
        {
            conflict = false;
            System.Text.StringBuilder sb = new System.Text.StringBuilder();
            sb.Append("生效根 ");
            sb.Append(workspace.Roots.Length.ToString());
            sb.Append(" 个: ");
            for (int i = 0; i < workspace.Roots.Length; i = i + 1)
            {
                WorkspaceConfig.RootEntry entry = workspace.Roots[i];
                if (i > 0)
                {
                    sb.Append(" | ");
                }
                sb.Append(entry.Id);
                sb.Append("=");
                sb.Append(entry.Path);
                if (entry.Writable)
                {
                    sb.Append("(可写)");
                }
                else
                {
                    sb.Append("(只读)");
                }
            }
            System.Text.StringBuilder dup = new System.Text.StringBuilder();
            int dupCount = 0;
            for (int i = 0; i < workspace.Roots.Length; i = i + 1)
            {
                for (int j = i + 1; j < workspace.Roots.Length; j = j + 1)
                {
                    if (!string.Equals(workspace.Roots[i].Path, workspace.Roots[j].Path, StringComparison.OrdinalIgnoreCase))
                    {
                        continue;
                    }
                    if (dupCount > 0)
                    {
                        dup.Append(" / ");
                    }
                    dupCount = dupCount + 1;
                    dup.Append(workspace.Roots[i].Id);
                    dup.Append(" 与 ");
                    dup.Append(workspace.Roots[j].Id);
                }
            }
            if (dupCount > 0)
            {
                conflict = true;
                sb.Append(" | ⚠ 同 path 多根 ");
                sb.Append(dupCount.ToString());
                sb.Append(" 组: ");
                sb.Append(dup.ToString());
                sb.Append("——按 id 解析取首个等值匹配，其余根的 writable / note 声明不生效（请改路径或合并为单根）");
            }
            return sb.ToString();
        }

        /// <summary>
        /// schema 驱动段注册（design-ch4-workspace §4.2）——按 schema 项的「键前缀段 → 文件归属」聚集，
        /// 逐段 AddFile；宿主不再硬编码段清单（新增段 = 模板加项，零代码改动）。同前缀映射到不同文件 → 取首个 + 告警。
        /// </summary>
        /// <param name="store">配置存储</param>
        /// <param name="schema">配置 schema</param>
        /// <param name="configDir">Data/config 目录（绝对路径）</param>
        internal static string RegisterSchemaSegments(ConfigStore store, ConfigSchema schema, string configDir)
        {
            Dictionary<string, string> bySegment = new Dictionary<string, string>(StringComparer.Ordinal);
            int conflicts = 0;
            ConfigSchema.Item[] items = schema.All();
            for (int i = 0; i < items.Length; i = i + 1)
            {
                string key = items[i].Key;
                string file = items[i].File;
                if (file.Length == 0)
                {
                    continue;
                }
                int dot = key.IndexOf('.');
                if (dot <= 0)
                {
                    continue;
                }
                string segment = key.Substring(0, dot);
                string path = Path.Combine(configDir, file);
                string prev;
                if (bySegment.TryGetValue(segment, out prev) && prev != null)
                {
                    if (!string.Equals(prev, path, StringComparison.OrdinalIgnoreCase))
                    {
                        conflicts = conflicts + 1;
                        Console.WriteLine("[CMD] config.seg | 前缀冲突: " + segment + " → " + prev + " / " + path + "（取首个）");
                    }
                    continue;
                }
                bySegment[segment] = path;
            }
            foreach (KeyValuePair<string, string> pair in bySegment)
            {
                store.AddFile(pair.Key, pair.Value);
            }
            string note = "config.seg | schema 驱动段注册: " + bySegment.Count.ToString() + " 段";
            if (conflicts > 0)
            {
                note = note + " | 前缀冲突 " + conflicts.ToString() + " 处（取首个）";
            }
            Console.WriteLine("[CMD] " + note);
            return note;
        }
    }
}