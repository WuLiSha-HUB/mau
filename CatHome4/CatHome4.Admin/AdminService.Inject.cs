using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Http;
using CatHome4.Contracts;
using Mau.Runtime;

using static CatHome4.Admin.CatHostService;

namespace CatHome4.Admin
{
    /// <summary>
    /// 项目挂载管理面分部——前文注入清单的「项目段」增删（inject-report 项目挂载）。
    /// 语义：挂载 = 把该项目 Job 三件 + 档案四件写进该猫 injectList，并立即 session.new 重建前文
    ///       （等同新会话重注入；不做运行时拼接）。取消 = 移除这些条目后再重建。
    /// 取数：项目清单与最后改动时间在**点击时**才扫盘（不在会话启动路径上）。
    /// 端点：主端口与每猫端口共用同一实现（每猫端口经 HttpHostOptions 回调注入）。
    /// </summary>
    internal static partial class AdminService
    {
        /// <summary>项目档案条目前缀——用于识别与移除上一次挂载留下的条目</summary>
        private const string ProjectSpecPrefix = "ccbp:Project/";

        /// <summary>项目 Job 条目前缀</summary>
        private const string JobSpecPrefix = "ccbp:L2/Job/";

        /// <summary>
        /// 候选项目清单——GET /api/v1/inject/projects（主端口薄壳；每猫端口走 BuildInjectProjectsJson 回调）。
        /// </summary>
        /// <returns>项目清单 JSON</returns>
        internal static IResult HandleInjectProjectsGet()
        {
            return Results.Text(BuildInjectProjectsJson(), "application/json");
        }

        /// <summary>
        /// 候选项目清单 JSON——GET /api/v1/inject/projects（每猫端口经回调复用本实现）。
        /// 扫 Project 根下每个项目目录的全部文件，取最新修改时间。
        /// </summary>
        /// <returns>项目清单 JSON 字符串</returns>
        internal static string BuildInjectProjectsJson()
        {
            string root = ResolveCcbpPath("ccbp:Project");
            if (root.Length == 0 || !Directory.Exists(root))
            {
                return JsonUtil.Object(("ok", false), ("reason", "Project 根不可解析：" + root));
            }
            List<(string name, DateTime ts, int files)> rows = new List<(string name, DateTime ts, int files)>();
            string[] dirs = Directory.GetDirectories(root);
            for (int i = 0; i < dirs.Length; i = i + 1)
            {
                string dir = dirs[i];
                DateTime latest = Directory.GetLastWriteTime(dir);
                int files = 0;
                try
                {
                    string[] all = Directory.GetFiles(dir, "*", SearchOption.AllDirectories);
                    for (int k = 0; k < all.Length; k = k + 1)
                    {
                        DateTime t = File.GetLastWriteTime(all[k]);
                        if (t > latest) { latest = t; }
                    }
                    files = all.Length;
                }
                catch (Exception ex)
                {
                    // 扫盘失败必须出声——静默会表现为「时间停在上一次」
                    LogStore.Add("CatHome4", 2, "项目扫盘失败：" + dir + "（" + ex.Message + "）", "INJECT");
                }
                rows.Add((Path.GetFileName(dir), latest, files));
            }
            // 排序：最后改动时间倒序（最新在前）；前端四列网格按 row 流填充 = 从左到右、再下一行
            rows.Sort((a, b) => b.ts.CompareTo(a.ts));
            List<string> items = new List<string>();
            for (int i = 0; i < rows.Count; i = i + 1)
            {
                items.Add(JsonUtil.Object(
                    ("name", rows[i].name),
                    ("lastWrite", rows[i].ts.ToString("yyyy-MM-ddTHH:mm:ss")),
                    ("files", rows[i].files)));
            }
            return JsonUtil.Object(
                ("ok", true),
                ("root", root),
                ("projects", JsonUtil.RawArray(items.ToArray())));
        }

        /// <summary>
        /// 项目挂载写面——POST /api/v1/inject/project（body: {"cat":"会话 id","project":"项目名；空=取消"}）。
        /// 写 cat.cfg 的 injectList 后立即 session.new：前文按新清单重注入。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleInjectProjectPost(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string catKey = "";
            string project = "";
            try
            {
                using (JsonDocument doc = JsonDocument.Parse(body))
                {
                    JsonElement root = doc.RootElement;
                    catKey = GetJsonString(root, "cat");
                    project = GetJsonString(root, "project");
                }
            }
            catch (Exception ex)
            {
                return Results.Json(new { ok = false, reason = "body 解析失败：" + ex.Message });
            }
            return Results.Text(ApplyInjectProject(catKey, project), "application/json");
        }

        /// <summary>
        /// 项目挂载执行体——校验猫与项目 → 重算 injectList → 落盘 → 立即 session.new（每猫端口经回调复用）。
        /// </summary>
        /// <param name="catKey">猫寻址键（会话 id / 显示名 / id 前缀）</param>
        /// <param name="project">项目名（空=取消挂载）</param>
        /// <returns>回执 JSON 字符串</returns>
        internal static string ApplyInjectProject(string catKey, string project)
        {
            string proj = project == null ? "" : project.Trim();
            // 主干猫不在多猫注册表（_cats 不含 majordomo，FindCat 不覆盖）——走默认猫专用分支
            if (string.Equals(catKey, "majordomo", StringComparison.Ordinal))
            {
                return ApplyInjectProjectDefault(proj);
            }
            CatEntry cat = FindCat(catKey);
            if (cat == null)
            {
                LogStore.Add("CatHome4", 2, "项目挂载失败：未找到猫 " + (catKey ?? ""), "INJECT");
                return JsonUtil.Object(("ok", false), ("reason", "未找到猫：" + (catKey ?? "")));
            }
            if (cat.Session == null)
            {
                LogStore.Add("CatHome4", 2, "项目挂载失败：猫已停机 " + cat.DisplayName, "INJECT");
                return JsonUtil.Object(("ok", false), ("reason", "猫已停机（无会话）：" + cat.DisplayName));
            }
            if (proj.Length > 0 && !ProjectDirExists(proj))
            {
                LogStore.Add("CatHome4", 2, "项目挂载失败：项目不存在 " + proj + "（cat=" + cat.DisplayName + "）", "INJECT");
                return JsonUtil.Object(("ok", false), ("reason", "项目不存在：" + proj));
            }

            string[] next = BuildInjectListWithMount(cat.InjectList, proj, cat.ProjectMount);
            cat.InjectList = next;
            cat.ProjectMount = proj;
            SaveCatCfg(cat);
            // 会话未开始对话 → 立即重建（等同新会话重注入）；已有对话 → 只落盘，下个新会话生效
            int msgCount = cat.Session.Context.GetMessages().Length;
            bool fresh = msgCount <= 1;
            if (fresh)
            {
                DispatchInput(CatInputTarget(cat), "session.new");
            }
            string msg;
            if (proj.Length == 0)
            {
                msg = fresh ? "已取消 Project 挂载——会话重建中" : "已取消 Project 挂载——新会话生效";
            }
            else
            {
                msg = fresh ? ("已挂载 " + proj + "——会话重建中") : ("已挂载 " + proj + "——新会话生效");
            }
            LogStore.Add("CatHome4", 1, "项目挂载：cat=" + cat.DisplayName + " project=" + (proj.Length == 0 ? "(空)" : proj)
                + " injectList=" + next.Length.ToString(), "INJECT");
            return JsonUtil.Object(
                ("ok", true),
                ("msg", msg),
                ("injectList", JsonUtil.Array(next)));
        }
        /// <summary>
        /// 默认猫（majordomo）项目挂载执行体——cat.cfg 合并写 + 静态面同步 + 主干 session.new。
        /// 主干不在多猫注册表（_cats 不含 majordomo），前文重建走 ChatBridge.DefaultInjectList /
        /// DefaultProjectMount 静态面（RouteSessionNew 的 main/major 分支），故落盘与静态面必须同步。
        /// </summary>
        /// <param name="project">项目名（空=取消挂载）</param>
        /// <returns>回执 JSON 字符串</returns>
        private static string ApplyInjectProjectDefault(string project)
        {
            string path = Path.Combine(_dataRoot, "Data", "sessions", "majordomo", "cat.cfg");
            CatHostService.CatCfgData cfg = LoadCatCfg(path);
            if (cfg == null)
            {
                LogStore.Add("CatHome4", 2, "项目挂载失败：majordomo cat.cfg 不可读（" + path + "）", "INJECT");
                return JsonUtil.Object(("ok", false), ("reason", "cat.cfg 不存在：majordomo"));
            }
            if (project.Length > 0 && !ProjectDirExists(project))
            {
                LogStore.Add("CatHome4", 2, "项目挂载失败：项目不存在 " + project + "（cat=majordomo）", "INJECT");
                return JsonUtil.Object(("ok", false), ("reason", "项目不存在：" + project));
            }
            string oldMount = cfg.ProjectMount == null ? "" : cfg.ProjectMount;
            string[] next = BuildInjectListWithMount(cfg.InjectList, project, oldMount);
            cfg.InjectList = next;
            cfg.ProjectMount = project;
            SaveCatCfgData("majordomo", cfg);
            // 静态面同步——主干 session.new 由 RouteSessionNew 的 main/major 分支消费这两个值
            _chatBridge.DefaultInjectList = next;
            _chatBridge.DefaultProjectMount = project;
            int msgCount = _chatBridge.DefaultSession == null ? 0 : _chatBridge.DefaultSession.Context.GetMessages().Length;
            bool fresh = msgCount <= 1;
            if (fresh)
            {
                DispatchInput(MainInputTarget(), "session.new");
            }
            string msg;
            if (project.Length == 0)
            {
                msg = fresh ? "已取消 Project 挂载——会话重建中" : "已取消 Project 挂载——新会话生效";
            }
            else
            {
                msg = fresh ? ("已挂载 " + project + "——会话重建中") : ("已挂载 " + project + "——新会话生效");
            }
            LogStore.Add("CatHome4", 1, "项目挂载：cat=majordomo project=" + (project.Length == 0 ? "(空)" : project)
                + " injectList=" + next.Length.ToString(), "INJECT");
            return JsonUtil.Object(
                ("ok", true),
                ("msg", msg),
                ("injectList", JsonUtil.Array(next)));
        }
        /// <summary>
        /// 项目目录存在性判定——ccbp:Project/&lt;项目名&gt; 可解析且目录存在。
        /// </summary>
        /// <param name="project">项目名</param>
        /// <returns>true=存在</returns>
        private static bool ProjectDirExists(string project)
        {
            if (project == null || project.Length == 0)
            {
                return false;
            }
            string dir = ResolveCcbpPath(ProjectSpecPrefix + project);
            return dir.Length > 0 && Directory.Exists(dir);
        }

        /// <summary>
        /// 注入清单重算——移除上一次挂载留下的项目条目，按需追加新项目的 Job 三件 + 档案四件。
        /// </summary>
        /// <param name="current">当前 injectList</param>
        /// <param name="project">目标项目名（空=只移除）</param>
        /// <param name="mountedProject">当前挂载的项目名（cat.cfg.projectMount——跟猫走，用于精确移除旧条目）</param>
        /// <returns>新清单</returns>
        private static string[] BuildInjectListWithMount(string[] current, string project, string mountedProject)
        {
            // 旧挂载项目名——以 cat.cfg.projectMount 为准（不再从清单反推）
            string oldMount = mountedProject == null ? "" : mountedProject.Trim();

            List<string> list = new List<string>();
            if (current != null)
            {
                for (int i = 0; i < current.Length; i = i + 1)
                {
                    string s = current[i] ?? "";
                    if (s.Length == 0) { continue; }
                    if (s.StartsWith(ProjectSpecPrefix, StringComparison.Ordinal)) { continue; }
                    if (oldMount.Length > 0 && s.StartsWith(JobSpecPrefix + oldMount + "/", StringComparison.Ordinal)) { continue; }
                    list.Add(s);
                }
            }

            if (project.Length > 0)
            {
                string jobDir = ResolveCcbpPath(JobSpecPrefix + project);
                if (jobDir.Length > 0 && Directory.Exists(jobDir))
                {
                    string[] jobNames = new string[] { "exp.md", "skill.md", "tools.md" };
                    for (int i = 0; i < jobNames.Length; i = i + 1)
                    {
                        if (File.Exists(Path.Combine(jobDir, jobNames[i])))
                        {
                            list.Add(JobSpecPrefix + project + "/" + jobNames[i]);
                        }
                    }
                }
                string projDir = ResolveCcbpPath(ProjectSpecPrefix + project);
                if (projDir.Length > 0 && Directory.Exists(projDir))
                {
                    string[] docNames = new string[] { "project.md", "todo.md", "design-status.md", "glossary.md" };
                    for (int i = 0; i < docNames.Length; i = i + 1)
                    {
                        if (File.Exists(Path.Combine(projDir, docNames[i])))
                        {
                            list.Add(ProjectSpecPrefix + project + "/" + docNames[i]);
                        }
                    }
                }
            }
            return list.ToArray();
        }

        /// <summary>
        /// 受控根寻址解析——ccbp:相对路径 → 绝对路径（解析失败返回空串）。
        /// </summary>
        /// <param name="spec">注入条目形态（id:相对路径）</param>
        /// <returns>绝对路径；不可解析=空串</returns>
        private static string ResolveCcbpPath(string spec)
        {
            WorkspaceConfig ws = null;
            if (!DataBox.TryResolve<WorkspaceConfig>(out ws) || ws == null)
            {
                return "";
            }
            try
            {
                WorkspaceConfig.InjectEntry entry = new WorkspaceConfig.InjectEntry();
                entry.File = spec;
                entry.Optional = true;
                entry.Label = spec;
                string path = ws.ResolveInjectFile(entry);
                return path == null ? "" : path;
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "路径解析失败：" + spec + "（" + ex.Message + "）", "INJECT");
                return "";
            }
        }
    }
}
