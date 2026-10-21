using System;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.IO;
using System.Net;
using System.Net.Sockets;
using System.Text.Json;
using Mau.Runtime;
using CatHome4.Http;
using CatHome4.QQ;
using CatHome4.Contracts;
using Mau.Providers;
using CH4;
using static CatHome4.Admin.AdminService;

namespace CatHome4.Admin
{
    /// <summary>
    /// 猫宿主管理域（A285：自 AdminService 抽出）——CatEntry 注册表 + PortAllocator + cat.* 指令族 + cat.cfg 持久化（P9.3b）。
    /// 两态：静默（持久化未运行）/ 启动（运行 + 端口监听）；删除 = 本地销毁 + 目录删除。
    /// 线程模型：处理函数仅主线程调用（HTTP 线程经 P9.3c PumpCatQueues 入队泵消费）。
    /// </summary>
    internal static partial class CatHostService
    {
        /// <summary>端口段——经 AdminService 注入面转发（同名单字段遮蔽 using static 导入，消除与类型名的二义）。</summary>
        private static PortBand PortBand
        {
            get { return AdminService.PortBand; }
        }

        /// <summary>
        /// info 系返回体序列化选项——缩进 + 中文直显（LLM 可读优先）。
        /// 消费面：info 内置工具（Program.BuildEnvInfo）与 cat.info（本类 HandleCatInfo）——单一定义两处共用。
        /// </summary>
        internal static readonly JsonSerializerOptions InfoJsonOptions = new JsonSerializerOptions
        {
            WriteIndented = true,
            Encoder = System.Text.Encodings.Web.JavaScriptEncoder.UnsafeRelaxedJsonEscaping
        };

        /// <summary>多猫注册表——cat.* 指令族管理面（默认 majordomo 会话不在此列——主端口对话区专属）</summary>
        private static readonly List<CatEntry> _cats = new List<CatEntry>();


        /// <summary>majordomo 独立对话端口 HttpHost——serveChatPage=true（F2.2 方案 A：与多猫同构；主端口保留管理面板）</summary>
        private static HttpHost _majorHost;

        /// <summary>
        /// 全部 HttpHost 主线程泵——主循环帧驱动（majordomo + 每猫；快照构建 ThreadGuard 须宿主主线程）。
        /// Q6 修复（2026-09-08）：每猫/独立端口 HttpHost 的快照缓存从未被泵构建（恒 {}）→ 快照 title 注入不生效 → 前端标题/页头占位符。
        /// </summary>
        internal static void PumpAllHosts()
        {
            if (_majorHost != null)
            {
                _majorHost.PumpMainThread();
            }
            for (int i = 0; i < _cats.Count; i++)
            {
                HttpHost host = _cats[i].Host;
                if (host != null)
                {
                    host.PumpMainThread();
                }
            }
        }

        /// <summary>
        /// 全部会话的视图推送面是否仍有待推内容——重启闸门第二判据（A206）。
        /// 多猫下任一会话在途都要等：判据取「或」——只有全部会话的推送面都清空才放行重启。
        /// </summary>
        /// <returns>true=仍有待推内容</returns>
        internal static bool AnyViewPending()
        {
            if (_chatBridge != null && _chatBridge.DefaultSession.ViewHasPendingContent)
            {
                return true;
            }
            for (int i = 0; i < _cats.Count; i = i + 1)
            {
                CatEntry cat = _cats[i];
                if (cat.Session != null && cat.Session.ViewHasPendingContent)
                {
                    return true;
                }
            }
            return false;
        }

        /// <summary>
        /// 轮末总结 LLM 配置解析——catKey → [endpoint, apikey, model]（apiConfigId → 配置池；缺则回落默认端点）。
        /// 供 ChatSession.RoundSummaryLlmProvider 接线（Core 零配置面依赖）；不可用返回 null（调用方静默跳过）。
        /// </summary>
        /// <param name="catKey">猫 key（majordomo=默认猫）</param>
        /// <returns>[endpoint, apikey, model]；不可用 null</returns>
        internal static string[] ResolveRoundSummaryLlm(string catKey)
        {
            Guid id = Guid.Empty;
            if (IsDefaultCatKey(catKey))
            {
                id = _defaultApiConfigId;
            }
            else
            {
                CatEntry cat = FindCat(catKey);
                if (cat != null)
                {
                    id = cat.ApiConfigId;
                }
            }
            CH_LlmApiConfig cfg = null;
            if (_apiStore != null)
            {
                if (id != Guid.Empty)
                {
                    _apiStore.TryGet(id, out cfg);
                }
                else
                {
                    cfg = _apiStore.ResolveDefault();
                }
            }
            if (cfg == null || cfg.Endpoint == null || cfg.Endpoint.Length == 0)
            {
                return null;
            }
            string secret = _apiStore != null ? _apiStore.GetSecret(cfg.ApiConfigId) : "";
            return new string[] { cfg.Endpoint, secret, cfg.DefaultModel == null ? "" : cfg.DefaultModel };
        }

        /// <summary>
        /// 全部 HttpHost 退出前收尾——逐 host 推最后一帧 + 等连接队列排空（A206；重启退出路径调用）。
        /// 动机：视图帧「入队 ≠ 写出」，Environment.Exit 会丢弃在途帧（判例 2026-10-06）。
        /// </summary>
        /// <param name="timeoutMs">单 host 排空等待上限毫秒</param>
        internal static void FlushAllHosts(int timeoutMs)
        {
            if (_majorHost != null)
            {
                _majorHost.FlushViewBeforeExit(timeoutMs);
            }
            for (int i = 0; i < _cats.Count; i = i + 1)
            {
                HttpHost host = _cats[i].Host;
                if (host != null)
                {
                    host.FlushViewBeforeExit(timeoutMs);
                }
            }
        }

        /// <summary>majordomo 独立对话端口——AllocatePort 分配（F2.2）</summary>
        private static int _majorPort = -1;

        /// <summary>majordomo 独立对话端口（F2.2 对外只读——Program 启动日志用）</summary>
        internal static int MajorPort
        {
            get { return _majorPort; }
        }


        /// <summary>
        /// 解析猫启用根条目——cat.cfg enabledRoots → 全局池子集（空/缺失=空白名单——仅常驻 workspace 可用；失败默认收紧，不回落全量）。
        /// M4e 猫级白名单：猫文件工具面只触及启用根。
        /// </summary>
        /// <param name="catKey">猫 key（majordomo=默认猫）</param>
        /// <returns>启用根条目数组</returns>
        internal static WorkspaceConfig.RootEntry[] ResolveCatRootEntries(string catKey)
        {
            WorkspaceConfig ws = null;
            DataBox.TryResolve<WorkspaceConfig>(out ws);
            if (ws == null || ws.Roots == null || ws.Roots.Length == 0)
            {
                return new WorkspaceConfig.RootEntry[0];
            }
            CatCfgData cfg = LoadCatCfg(Path.Combine(_dataRoot, "Data", "sessions", catKey, "cat.cfg"));
            string[] enabled = null;
            if (cfg != null && cfg.EnabledRoots != null && cfg.EnabledRoots.Length > 0)
            {
                enabled = cfg.EnabledRoots;
            }
            List<WorkspaceConfig.RootEntry> entries = new List<WorkspaceConfig.RootEntry>();
            for (int i = 0; i < ws.Roots.Length; i++)
            {
                WorkspaceConfig.RootEntry entry = ws.Roots[i];
                // workspace 强制常驻——不因启用列表为空或未勾选而移除（id 大小写不敏感：workspace.json 记作 WorkSpace）
                if (string.Equals(entry.Id, "workspace", StringComparison.OrdinalIgnoreCase) || string.Equals(entry.Id, "runtime", StringComparison.OrdinalIgnoreCase))
                {
                    entries.Add(entry);
                    continue;
                }
                // 未配置 / 解析失败 = 空白名单——失败默认收紧（只剩常驻根），不回落全量
                if (enabled == null)
                {
                    continue;
                }
                bool found = false;
                for (int j = 0; j < enabled.Length; j++)
                {
                    if (string.Equals(enabled[j], entry.Id, StringComparison.OrdinalIgnoreCase))
                    {
                        found = true;
                        break;
                    }
                }
                if (found)
                {
                    entries.Add(entry);
                }
            }
            return entries.ToArray();
        }

        /// <summary>
        /// 猫级文件系统应用——按猫启用根重建 ToolCatContext 缓存（catcfg.apply / 会话构造时调用；主线程）。
        /// </summary>
        /// <param name="catKey">猫 key（majordomo=默认猫）</param>
        internal static void ApplyCatRoots(string catKey)
        {
            WorkspaceConfig.RootEntry[] entries = ResolveCatRootEntries(catKey);
            // §4.3 空值语义可观测——enabledRoots 空=空白名单（仅常驻根；失败默认收紧）：解析结果落审计行
            CatCfgData catCfg = LoadCatCfg(Path.Combine(_dataRoot, "Data", "sessions", catKey, "cat.cfg"));
            string rootSource = "空白名单";
            int rootDeclared = 0;
            if (catCfg != null && catCfg.EnabledRoots != null && catCfg.EnabledRoots.Length > 0)
            {
                rootSource = "子集";
                rootDeclared = catCfg.EnabledRoots.Length;
            }
            List<string> rootIds = new List<string>();
            for (int i = 0; i < entries.Length; i = i + 1)
            {
                rootIds.Add(entries[i].Id);
            }
            LogStore.Add("CatHome4", 1, "cat.roots | cat=" + catKey + " | 来源=" + rootSource + " | 声明=" + rootDeclared.ToString() + " | 实际=" + entries.Length.ToString() + " | [" + string.Join(",", rootIds) + "]", "CHAT");
            // [段2] 库级授权登记（A277）——默认猫全放；其余按 cat.cfg dbLibs 白名单（空集合 = 无库权限）
            bool allDbLibs = string.Equals(catKey, "majordomo", StringComparison.OrdinalIgnoreCase);
            string[] dbLibs = new string[0];
            if (catCfg != null && catCfg.DbLibs != null)
            {
                dbLibs = catCfg.DbLibs;
            }
            DbAccessPolicy.Register(catKey, allDbLibs, dbLibs);
            string allFlag = "0";
            if (allDbLibs)
            {
                allFlag = "1";
            }
            LogStore.Add("CatHome4", 1, "cat.dblibs | cat=" + catKey + " | allLibs=" + allFlag + " | 声明=" + dbLibs.Length.ToString() + " | [" + string.Join(",", dbLibs) + "]", "CHAT");
            if (entries.Length == 0)
            {
                ToolCatContext.UpdateCatFileSystem(catKey, null, "");
                return;
            }
            string recycleRoot = Path.Combine(WorkspaceRecycleRootFor(entries, _dataRoot), "CatTemp", "fs_recycle");
            ToolCatContext.UpdateCatFileSystem(catKey, entries, recycleRoot);
        }

        /// <summary>
        /// 猫级回收站根——启用根内第一个可写根（对齐全局 WorkspaceRecycleRoot 语义；无可写根回退数据根）。
        /// </summary>
        /// <param name="entries">启用根条目</param>
        /// <param name="dataRoot">数据根</param>
        /// <returns>回收站基底目录</returns>
        private static string WorkspaceRecycleRootFor(WorkspaceConfig.RootEntry[] entries, string dataRoot)
        {
            for (int i = 0; i < entries.Length; i++)
            {
                if (entries[i].Writable)
                {
                    return entries[i].Path;
                }
            }
            return dataRoot;
        }
        /// <summary>
        /// cat.* 指令族处理——主线程调用（DispatchCommand 分支 P9.3c 接线）。
        /// </summary>
        /// <param name="line">指令行</param>
        /// <returns>结果文本（CLI 输出 / HTTP 回执）</returns>
        internal static string HandleCatCommand(string line)
        {
            if (line.StartsWith("cat.new ", StringComparison.Ordinal))
            {
                string name = line.Substring(8).Trim();
                if (name.Length == 0)
                {
                    return "cat.new | 显示名不能为空";
                }
                return HandleCatNew(name);
            }
            if (line == "cat.list")
            {
                return HandleCatList();
            }
            if (line == "cat.info")
            {
                return HandleCatInfo();
            }
            if (line.StartsWith("cat.start ", StringComparison.Ordinal))
            {
                return HandleCatStart(line.Substring(10).Trim());
            }
            if (line.StartsWith("cat.stop ", StringComparison.Ordinal))
            {
                return HandleCatStop(line.Substring(9).Trim());
            }
            if (line.StartsWith("cat.delete ", StringComparison.Ordinal))
            {
                return HandleCatDelete(line.Substring(11).Trim());
            }
            if (line.StartsWith("cat.pause", StringComparison.Ordinal))
            {
                // P6 中止——管理面指定猫（每猫端口无 key 版本走 DispatchCommandForCat/Major）
                string rest = line.Substring(9).Trim();
                if (rest.Length == 0)
                {
                    return "cat.pause | 用法: cat.pause <key>";
                }
                if (string.Equals(rest, "majordomo", StringComparison.Ordinal))
                {
                    return "cat.pause | majordomo 为特殊会话——不可自查（主干会话不能对自己下中止指令）";
                }
                CatEntry cat = FindCat(rest);
                if (cat == null)
                {
                    return "cat.pause | 未找到猫: " + rest;
                }
                cat.Session.Pause();
                return "cat.pause | 已投递中止: " + cat.DisplayName;
            }
            if (line.StartsWith("cat.continue", StringComparison.Ordinal))
            {
                // 继续——管理面指定猫（每猫端口无 key 版本走 DispatchCommandForCat）
                string rest = line.Substring(12).Trim();
                if (rest.Length == 0)
                {
                    return "cat.continue | 用法: cat.continue <key>";
                }
                if (string.Equals(rest, "majordomo", StringComparison.Ordinal))
                {
                    return "cat.continue | majordomo 为特殊会话——不可自查（主干会话不能对自己下继续指令）";
                }
                CatEntry cat = FindCat(rest);
                if (cat == null)
                {
                    return "cat.continue | 未找到猫: " + rest;
                }
                cat.Session.Continue();
                return "cat.continue | 已投递继续: " + cat.DisplayName;
            }
            if (line.StartsWith("cat.chat ", StringComparison.Ordinal))
            {
                // 格式：cat.chat <key> <内容>——内核每猫对话通道（M1d 验收 + 回归复用；DriveUntilIdle 等回复完成）
                string rest = line.Substring(9).Trim();
                int space = rest.IndexOf(' ');
                if (space <= 0)
                {
                    return "cat.chat | 用法: cat.chat <key> <内容>";
                }
                string key = rest.Substring(0, space).Trim();
                string content = rest.Substring(space + 1).Trim();
                if (content.Length == 0)
                {
                    return "cat.chat | 内容不能为空";
                }
                if (string.Equals(key, "majordomo", StringComparison.Ordinal))
                {
                    return "cat.chat | majordomo 为特殊会话——不可自查（主干会话不能对自己投递对话）";
                }
                CatEntry cat = FindCat(key);
                if (cat == null)
                {
                    return "cat.chat | 未找到猫: " + key;
                }
                cat.Session.PostUserMessage(content);
                return "cat.chat | 已投递: " + cat.DisplayName;
            }
            if (line.StartsWith("session.rollback ", StringComparison.Ordinal) || line.StartsWith("session.fork ", StringComparison.Ordinal))
            {
                // P6b 回滚/分支——管理端口通道（CLI/HTTP 主线程直执；HTTP 线程经 _catQueue 泵）
                return ExecuteSessionCmd(_chatBridge.DefaultSession, line);
            }
            if (line.StartsWith("catcfg.apply ", StringComparison.Ordinal))
            {
                // M3 每猫配置运行时生效——主线程泵消费（HTTP 端点落盘后入队）
                return HandleCatCfgApply(line.Substring(13).Trim());
            }
            // A62 每猫配置读写指令——工具面 config-cat-get / config-cat-set 的调度底座（合并写语义）
            if (line.StartsWith("cat.cfg.get ", StringComparison.Ordinal))
            {
                return HandleCatCfgGet(line.Substring(12).Trim());
            }
            if (line.StartsWith("cat.cfg.set ", StringComparison.Ordinal))
            {
                return HandleCatCfgSet(line.Substring(12).Trim());
            }
            return "cat.* 指令未识别: " + line + "\n" + CatCommandUsage();
        }
        /// <summary>
        /// cat.* 指令族用法清单——错 key 自解释（入口面 = 用法声明面；design-ch4-cat-admin §五）。
        /// </summary>
        /// <returns>指令清单文本</returns>
        internal static string CatCommandUsage()
        {
            System.Text.StringBuilder sb = new System.Text.StringBuilder();
            sb.AppendLine("cat.* / catcfg.* 可用指令:");
            sb.AppendLine("  cat.list | 全部猫（id/显示名/运行态:端口）");
            sb.AppendLine("  cat.info | 全猫状态统计（运行态/端口/流式态/前文长度/最近活跃——整块 JSON）");
            sb.AppendLine("  cat.new <显示名> | 新建猫（静默态，待 cat.start）");
            sb.AppendLine("  cat.start <key> | 分配端口 + 拉起 HttpHost");
            sb.AppendLine("  cat.stop <key> | 停止（majordomo 禁停）");
            sb.AppendLine("  cat.delete <key> | 销毁（majordomo 禁删）");
            sb.AppendLine("  cat.pause <key> | 中止当前回合");
            sb.AppendLine("  cat.continue <key> | 继续（当前前文再发一次请求）");
            sb.AppendLine("  cat.chat <key> <内容> | 内核对话投递");
            sb.AppendLine("  cat.cfg.get <key> | 该猫 cat.cfg 全量字段");
            sb.AppendLine("  cat.cfg.set <key> <字段> <值> | 字段级合并写（未提交字段保留）");
            sb.Append("  catcfg.apply <key> | 每猫配置运行时生效");
            return sb.ToString();
        }
        /// <summary>
        /// 猫寻址 → cat.cfg 路径解析（majordomo=默认猫固定路径；多猫经 FindCat 寻址——id 精确 / 显示名 / id 前缀唯一）。
        /// </summary>
        /// <param name="key">猫寻址键</param>
        /// <param name="id">输出：猫 id（cat.cfg 目录名）</param>
        /// <param name="path">输出：cat.cfg 绝对路径</param>
        /// <returns>错误文本（空=通过）</returns>
        private static string ResolveCatCfgTarget(string key, out string id, out string path)
        {
            id = "";
            path = "";
            if (key == null || key.Length == 0)
            {
                return "缺少猫寻址键";
            }
            if (string.Equals(key, "majordomo", StringComparison.Ordinal))
            {
                id = "majordomo";
            }
            else
            {
                CatEntry cat = FindCat(key);
                if (cat == null)
                {
                    return "未找到猫: " + key + "（cat.list 查看全部）";
                }
                id = cat.Id;
            }
            path = Path.Combine(_dataRoot, "Data", "sessions", id, "cat.cfg");
            return "";
        }
        /// <summary>
        /// 字符串数组 → 逗号清单（空数组空串）。
        /// </summary>
        /// <param name="values">数组</param>
        /// <returns>逗号清单</returns>
        private static string JoinArray(string[] values)
        {
            if (values == null || values.Length == 0)
            {
                return "";
            }
            return string.Join(",", values);
        }
        /// <summary>
        /// cat.cfg 字段清单格式化——get / set 共用（写后读回对照用同一口径）。
        /// </summary>
        /// <param name="id">猫 id</param>
        /// <param name="cfg">配置数据</param>
        /// <returns>清单文本</returns>
        private static string FormatCatCfg(string id, CatCfgData cfg)
        {
            System.Text.StringBuilder sb = new System.Text.StringBuilder();
            sb.AppendLine("cat.cfg | " + id);
            sb.AppendLine("  id=" + cfg.Id);
            sb.AppendLine("  displayName=" + cfg.DisplayName);
            sb.AppendLine("  apiConfigId=" + cfg.ApiConfigId);
            sb.AppendLine("  persona=" + cfg.Persona);
            sb.AppendLine("  toolNames=" + cfg.ToolNames);
            sb.AppendLine("  injectList=" + JoinArray(cfg.InjectList));
            sb.AppendLine("  packs=" + JoinArray(cfg.Packs));
            sb.AppendLine("  qqbotId=" + cfg.QqBotId);
            sb.AppendLine("  qqbotEnable=" + (cfg.QqBotEnable ? "true" : "false"));
            sb.AppendLine("  enabledRoots=" + JoinArray(cfg.EnabledRoots));
            sb.Append("  dbLibs=" + JoinArray(cfg.DbLibs));
            return sb.ToString();
        }
        /// <summary>
        /// cat.cfg.get——该猫 cat.cfg 全量字段（不设掩码：cat.cfg 不含密钥字段——密钥在 Data/secrets；design-ch4-cat-admin §三）。
        /// </summary>
        /// <param name="key">猫寻址键（majordomo=默认猫；多猫=会话 id / 显示名 / id 前缀唯一）</param>
        /// <returns>字段清单文本</returns>
        internal static string HandleCatCfgGet(string key)
        {
            string id;
            string path;
            string error = ResolveCatCfgTarget(key, out id, out path);
            if (error.Length > 0)
            {
                return "cat.cfg.get | " + error;
            }
            CatCfgData cfg = LoadCatCfg(path);
            if (cfg == null)
            {
                return "cat.cfg.get | cat.cfg 不存在: " + id;
            }
            return FormatCatCfg(id, cfg);
        }
        /// <summary>
        /// 逗号 / 空白分隔清单解析——去空项（packs / enabledRoots 字段值用）。
        /// </summary>
        /// <param name="value">原始串（如 "overwork,ccbp-core"）</param>
        /// <returns>去空项数组</returns>
        private static string[] SplitList(string value)
        {
            if (value == null || value.Trim().Length == 0)
            {
                return new string[0];
            }
            string[] parts = value.Split(new char[] { ',', ' ', '\t' }, StringSplitOptions.RemoveEmptyEntries);
            List<string> list = new List<string>();
            for (int i = 0; i < parts.Length; i = i + 1)
            {
                string item = parts[i].Trim();
                if (item.Length > 0)
                {
                    list.Add(item);
                }
            }
            return list.ToArray();
        }
        /// <summary>
        /// API 配置身份归一——空 / 全零 = 默认端点语义（Guid.Empty 串，读侧实时解析全局默认）；否则须合法 Guid。
        /// </summary>
        /// <param name="value">原始值</param>
        /// <param name="error">输出：错误文本（空=通过）</param>
        /// <returns>归一后的身份串</returns>
        private static string NormalizeApiConfigId(string value, out string error)
        {
            error = "";
            string trimmed = (value == null) ? "" : value.Trim();
            if (trimmed.Length == 0 || trimmed == Guid.Empty.ToString("D"))
            {
                return Guid.Empty.ToString("D");
            }
            Guid parsed;
            if (!Guid.TryParse(trimmed, out parsed) || parsed == Guid.Empty)
            {
                error = "apiConfigId 非法: " + trimmed + "（空=默认端点）";
                return "";
            }
            return trimmed;
        }
        /// <summary>
        /// 注入清单解析——逗号分隔 + 路径合法性过滤（只接受完整路径：绝对路径 / id: 命名空间；非法项剔除并记 L2）。
        /// </summary>
        /// <param name="value">原始串（如 "CCBP:L1,CCBP:SOUL.md"）</param>
        /// <returns>合法路径数组</returns>
        private static string[] ParseInjectList(string value)
        {
            string[] parts = SplitList(value);
            List<string> list = new List<string>();
            for (int i = 0; i < parts.Length; i = i + 1)
            {
                if (IsValidInjectPath(parts[i]))
                {
                    list.Add(parts[i]);
                }
                else
                {
                    LogStore.Add("CatHome4", 2, "猫配置：注入路径被拒绝（非法格式）" + parts[i], "CONFIG");
                }
            }
            return list.ToArray();
        }
        /// <summary>
        /// cat.cfg.set——字段级合并写（读现值 → 改单字段 → 全量写回；未提交字段逐字保留）。
        /// 🔴 两条写入通道同语义（2026-10-02 判例蒸馏轮收口：POST /api/v1/cat-config 已改为字段级合并写；2026-09-16 的「整对象替换」语义退役）。
        /// 返回落盘实况（写后读回全量对照）；生效走 catcfg.apply 链（apiConfigId 立即，其余前文项新会话生效）。
        /// </summary>
        /// <param name="rest">参数串：&lt;key&gt; &lt;字段&gt; &lt;值…&gt;（值取行内剩余全部原文——persona 多行原样直达）</param>
        /// <returns>落盘实况文本</returns>
        internal static string HandleCatCfgSet(string rest)
        {
            if (rest == null || rest.Length == 0)
            {
                return "cat.cfg.set | 用法: cat.cfg.set <key> <字段> <值>";
            }
            int sp1 = rest.IndexOf(' ');
            if (sp1 <= 0)
            {
                return "cat.cfg.set | 用法: cat.cfg.set <key> <字段> <值>";
            }
            string key = rest.Substring(0, sp1).Trim();
            string tail = rest.Substring(sp1 + 1);
            int sp2 = tail.IndexOf(' ');
            if (sp2 <= 0)
            {
                return "cat.cfg.set | 用法: cat.cfg.set <key> <字段> <值>";
            }
            string field = tail.Substring(0, sp2).Trim();
            string value = tail.Substring(sp2 + 1).Trim();
            string id;
            string path;
            string error = ResolveCatCfgTarget(key, out id, out path);
            if (error.Length > 0)
            {
                return "cat.cfg.set | " + error;
            }
            CatCfgData cfg = LoadCatCfg(path);
            if (cfg == null)
            {
                return "cat.cfg.set | cat.cfg 不存在: " + id;
            }
            // [段1] 字段级写入——白名单 + 类型/值域校验（复用端点写时校验函数；非法一律报错退出，不静默回落）
            string applied;
            bool immediate = false;
            if (field == "displayName")
            {
                if (value.Length == 0)
                {
                    return "cat.cfg.set | displayName 不能为空";
                }
                cfg.DisplayName = value;
                applied = "displayName=" + value;
            }
            else if (field == "apiConfigId")
            {
                string normalized = NormalizeApiConfigId(value, out error);
                if (error.Length > 0)
                {
                    return "cat.cfg.set | " + error;
                }
                cfg.ApiConfigId = normalized;
                applied = "apiConfigId=" + normalized;
                immediate = true;
            }
            else if (field == "type")
            {
                cfg.Type = value.Trim();
                applied = "type=" + (cfg.Type.Length > 0 ? cfg.Type : "（已清空）");
            }
            else if (field == "persona")
            {
                cfg.Persona = value;
                applied = "persona（" + value.Length.ToString() + " 字符）";
            }
            else if (field == "toolNames")
            {
                string[] removedNames;
                string valid = ValidateToolNames(value, out removedNames);
                cfg.ToolNames = valid;
                applied = "toolNames=" + valid;
                // A132 剔除出声——被剔名字随回执带回（调用方可见；明细已记日志，不静默）
                if (removedNames.Length > 0)
                {
                    applied = applied + "｜⚠️ 已剔除池外工具名 " + removedNames.Length.ToString() + " 个：" + JoinArray(removedNames);
                }
            }
            else if (field == "injectList")
            {
                string[] paths = ParseInjectList(value);
                cfg.InjectList = paths;
                applied = "injectList=" + JoinArray(paths);
            }
            else if (field == "packs")
            {
                string[] packKeys = SplitList(value);
                cfg.Packs = packKeys;
                applied = "packs=" + JoinArray(packKeys);
            }
            else if (field == "dbLibs")
            {
                // A266——库白名单（库 ID 清单；空=清空授权；库存在性校验后置到工具面）
                string[] libIds = SplitList(value);
                cfg.DbLibs = libIds;
                applied = "dbLibs=" + JoinArray(libIds);
            }
            else if (field == "qqbotId")
            {
                if (value.Length == 0)
                {
                    cfg.QqBotId = "";
                    applied = "qqbotId=（已清空）";
                }
                else
                {
                    Guid parsed;
                    if (!Guid.TryParse(value, out parsed) || parsed == Guid.Empty)
                    {
                        return "cat.cfg.set | qqbotId 非法: " + value;
                    }
                    string boundBy = FindQqBotBindingOwner(id, value);
                    if (boundBy.Length > 0)
                    {
                        return "cat.cfg.set | 该 QQ Bot 已绑定猫「" + boundBy + "」——一只 Bot 只能绑一只猫";
                    }
                    cfg.QqBotId = value;
                    applied = "qqbotId=" + value;
                }
            }
            else if (field == "qqbotEnable")
            {
                if (string.Equals(value, "true", StringComparison.OrdinalIgnoreCase))
                {
                    cfg.QqBotEnable = true;
                    applied = "qqbotEnable=true";
                }
                else if (string.Equals(value, "false", StringComparison.OrdinalIgnoreCase))
                {
                    cfg.QqBotEnable = false;
                    applied = "qqbotEnable=false";
                }
                else
                {
                    return "cat.cfg.set | qqbotEnable 非法值: " + value + "（true/false）";
                }
            }
            else if (field == "userInjectIdleOnly")
            {
                if (string.Equals(value, "true", StringComparison.OrdinalIgnoreCase))
                {
                    cfg.UserInjectIdleOnly = true;
                    applied = "userInjectIdleOnly=true";
                }
                else if (string.Equals(value, "false", StringComparison.OrdinalIgnoreCase))
                {
                    cfg.UserInjectIdleOnly = false;
                    applied = "userInjectIdleOnly=false";
                }
                else
                {
                    return "cat.cfg.set | userInjectIdleOnly 非法值: " + value + "（true/false）";
                }
            }
            else if (field == "reasoningEffort")
            {
                // A234——思考强度覆盖（空=跟随全局）；白名单 low/high/max
                string norm = value.Trim().ToLowerInvariant();
                if (norm.Length == 0)
                {
                    cfg.ReasoningEffort = "";
                    applied = "reasoningEffort=（跟随全局）";
                }
                else if (norm == "low" || norm == "high" || norm == "max")
                {
                    cfg.ReasoningEffort = norm;
                    applied = "reasoningEffort=" + norm;
                }
                else
                {
                    return "cat.cfg.set | reasoningEffort 非法值: " + value + "（low/high/max/空）";
                }
            }
            else if (field == "appearanceId")
            {
                // 外观配置身份（A244）；空/全零=跟随默认外观；非零须 GUID
                string trimmedAppearance = value == null ? "" : value.Trim();
                if (trimmedAppearance.Length == 0 || trimmedAppearance == Guid.Empty.ToString("D"))
                {
                    cfg.AppearanceId = Guid.Empty.ToString("D");
                    applied = "appearanceId=（跟随默认外观）";
                }
                else
                {
                    Guid parsedAppearance;
                    if (!Guid.TryParse(trimmedAppearance, out parsedAppearance) || parsedAppearance == Guid.Empty)
                    {
                        return "cat.cfg.set | appearanceId 非法值: " + value + "（GUID 或空）";
                    }
                    cfg.AppearanceId = parsedAppearance.ToString("D");
                    applied = "appearanceId=" + parsedAppearance.ToString("D");
                }
            }
            else if (field == "enabledRoots")
            {
                string[] removedRoots;
                bool poolUnavailable;
                string[] roots = ValidateEnabledRoots(SplitList(value), out removedRoots, out poolUnavailable);
                cfg.EnabledRoots = roots;
                applied = "enabledRoots=" + JoinArray(roots);
                // A261 剔除出声——被剔根 id 随回执带回（调用方可见；明细已记日志，不静默）
                if (removedRoots.Length > 0)
                {
                    applied = applied + "｜⚠️ 已剔除池外根 id " + removedRoots.Length.ToString() + " 个：" + JoinArray(removedRoots);
                }
                if (poolUnavailable)
                {
                    applied = applied + "｜⚠️ 全局根池不可用——白名单按空处理（仅常驻根可用）";
                }
            }
            else
            {
                return "cat.cfg.set | 非法字段: " + field + "（可用: displayName/apiConfigId/persona/type/toolNames/injectList/packs/dbLibs/qqbotId/qqbotEnable/enabledRoots/userInjectIdleOnly/reasoningEffort/appearanceId）";
            }
            // [段2] 全量写回 + 生效入队（与端点同源：写后 catcfg.apply 主线程泵消费）
            SaveCatCfgData(id, cfg);
            PostInput(MainInputTarget(), "catcfg.apply " + id);
            // [段3] 落盘实况——写后读回对照（非"已提交"）
            CatCfgData after = LoadCatCfg(path);
            if (after == null)
            {
                return "cat.cfg.set | " + id + " | " + applied + " 已写入，但读回失败（落盘异常——请核查 cat.cfg）";
            }
            System.Text.StringBuilder sb = new System.Text.StringBuilder();
            sb.AppendLine("cat.cfg.set | " + id + " | " + applied + " 已更新");
            sb.AppendLine("  [落盘] cat.cfg 已写回（未提交字段逐字保留）");
            if (immediate)
            {
                sb.AppendLine("  [生效] 立即（LLM 端点已切换）");
            }
            else
            {
                sb.AppendLine("  [生效] 前文项新会话生效（catcfg.apply 已入队）");
            }
            sb.Append(FormatCatCfg(id, after));
            return sb.ToString();
        }

        /// <summary>
        /// cat.new——创建会话实体 + cat.cfg 持久化（静默态；cat.start 启动）。
        /// </summary>
        /// <param name="name">显示名</param>
        /// <returns>结果文本</returns>
        private static string HandleCatNew(string name)
        {
            string id = SessionStore.NewSessionId();
            CatEntry cat = CreateCatEntry(id, name, null);
            if (cat == null)
            {
                return "cat.new | 会话构造失败";
            }
            _cats.Add(cat);
            SaveCatCfg(cat);
            LogStore.Add("CatHome4", 1, "已创建猫「" + name + "」（id " + id + "），静默待启动", "CHAT");
            return "cat.new | id=" + id + " | name=" + name + " | 静默态（cat.start 启动）";
        }

        /// <summary>
        /// 会话指令执行——session.rollback/session.fork 统一入口（P6b；仅主线程调用）。
        /// </summary>
        /// <param name="session">目标会话（fork 时 = 源会话）</param>
        /// <param name="line">指令行（session.rollback &lt;msgIndex&gt; / session.fork &lt;name&gt; &lt;msgIndex&gt;）</param>
        /// <returns>结果文本</returns>
        internal static string ExecuteSessionCmd(ChatSession session, string line)
        {
            if (line.StartsWith("session.rollback ", StringComparison.Ordinal))
            {
                string arg = line.Substring(17).Trim();
                int idx;
                if (!int.TryParse(arg, out idx))
                {
                    return "session.rollback | 参数非法: " + arg + "（需要消息索引）";
                }
                return session.Rollback(idx);
            }
            if (line.StartsWith("session.fork ", StringComparison.Ordinal))
            {
                string arg = line.Substring(13).Trim();
                int space = arg.IndexOf(' ');
                if (space <= 0)
                {
                    return "session.fork | 用法: session.fork <显示名> <消息索引>";
                }
                string name = arg.Substring(0, space).Trim();
                int idx;
                if (!int.TryParse(arg.Substring(space + 1).Trim(), out idx))
                {
                    return "session.fork | 消息索引非法: " + arg.Substring(space + 1).Trim();
                }
                return HandleCatFork(session, name, idx);
            }
            return "session.* 指令未识别: " + line;
        }

        /// <summary>
        /// session.fork——从源猫节点分支新建独立 Cat（P6b：继承配置 + 播种节点前前文；静默态待启动）。
        /// 继承：persona/toolNames/injectList/apiConfigId/enabledRoots/qqbotId + 节点前前文；qqbot 转发默认关闭（enable=false——防双猫抢 Bot）；不继承：运行态。
        /// 配置源：多猫 = CatEntry 运行时实体（catcfg.apply 后真相）；majordomo = sessions/majordomo/cat.cfg（source.Id 是 Ticks——判例 fork 配置全空）。
        /// </summary>
        /// <param name="source">源会话（发起 fork 的猫）</param>
        /// <param name="name">新猫显示名</param>
        /// <param name="msgIndex">切点消息索引（assistant 正式回复）</param>
        /// <returns>结果文本</returns>
        private static string HandleCatFork(ChatSession source, string name, int msgIndex)
        {
            if (source == null)
            {
                return "session.fork | 源会话不存在";
            }
            if (!source.IsIdle)
            {
                return "session.fork | 源会话忙——分支仅空闲时执行";
            }
            LlmMessage[] all = source.Context.GetMessages();
            if (msgIndex < 0 || msgIndex >= all.Length)
            {
                return "session.fork | 节点索引越界: " + msgIndex.ToString();
            }
            LlmMessage target = all[msgIndex];
            if (target.Role != LlmRole.Assistant || target.Content == null || target.Content.Length == 0)
            {
                return "session.fork | 节点不是正式回复（仅 assistant 回复可作切点）";
            }
            // [段1] 播种前文——保留 [0..msgIndex]（含源 system——注入内容已在其中）
            LlmMessage[] seed = new LlmMessage[msgIndex + 1];
            for (int i = 0; i <= msgIndex; i = i + 1)
            {
                seed[i] = all[i];
            }
            // [段2] 继承源配置——运行时实体优先（多猫 CatEntry = catcfg.apply 后运行时真相）；majordomo 特判路径（source.Id 是 Ticks 非 "majordomo"——判例：fork 配置全空）
            string id = SessionStore.NewSessionId();
            CatCfgData cfgData = null;
            CatEntry srcCat = FindCat(source.Id);
            if (srcCat != null)
            {
                // 多猫——运行时实体字段（ApiConfigId/Persona/ToolNames/InjectList/QqBotId 继承）
                cfgData = new CatCfgData();
                cfgData.ApiConfigId = srcCat.ApiConfigId == Guid.Empty ? "" : srcCat.ApiConfigId.ToString("D");
                cfgData.Persona = srcCat.Persona;
                cfgData.ToolNames = srcCat.ToolNames;
                cfgData.InjectList = srcCat.InjectList;
                cfgData.QqBotId = srcCat.QqBotId == Guid.Empty ? "" : srcCat.QqBotId.ToString("D");
                cfgData.QqBotEnable = srcCat.QqBotEnable;
                cfgData.Type = srcCat.Type;
                // EnabledRoots CatEntry 不持有——从源磁盘 cfg 补读
                CatCfgData srcDisk = LoadCatCfg(Path.Combine(_dataRoot, "Data", "sessions", srcCat.Id, "cat.cfg"));
                if (srcDisk != null)
                {
                    cfgData.EnabledRoots = srcDisk.EnabledRoots;
                }
            }
            else
            {
                // majordomo 默认猫——cfg 路径固定 sessions/majordomo/cat.cfg（source.Id = Ticks 不可用于寻址）
                cfgData = LoadCatCfg(Path.Combine(_dataRoot, "Data", "sessions", "majordomo", "cat.cfg"));
            }
            if (cfgData == null)
            {
                cfgData = new CatCfgData();
            }
            cfgData.Id = id;
            cfgData.DisplayName = name;
            cfgData.Running = false;
            cfgData.Port = 0;
            // qqbotId 继承保留（防双猫抢 Bot 靠 enable=false——莎拍板：复制配置、默认不开启转发）
            cfgData.QqBotEnable = false;
            SaveCatCfgData(id, cfgData);
            // [段3] 创建 + 播种 + 注册（CreateCatEntry 读新 cfg——克隆配置生效；视图面与会话元数据面随前文播种）
            CatEntry cat = CreateCatEntry(id, name, seed, source, msgIndex);
            if (cat == null)
            {
                return "session.fork | 会话构造失败";
            }
            // [段3b] 播种前文立即落盘——会话文件不存在时重启扫描会走注入分支（播种丢失）；Save 保证重启恢复播种
            cat.Session.Store.Rewrite(cat.Session.Context.GetMessages());
            _cats.Add(cat);
            LogStore.Add("CatHome4", 1, "已从「" + source.DisplayName + "」节点 " + msgIndex.ToString() + " 分支新猫「" + name + "」（id " + id + "），静默待启动", "CHAT");
            return "session.fork | id=" + id + " | name=" + name + " | 已从节点 " + msgIndex.ToString() + " 分支（静默态，cat.start 启动）";
        }

        /// <summary>
        /// cat.list——全猫列出（主干会话前置 + 多猫注册表；id/显示名/状态/端口）。
        /// 主干会话口径与前端列表（BuildCatsJson）一致——_cats 注册表不含 majordomo。
        /// </summary>
        /// <returns>结果文本</returns>
        private static string HandleCatList()
        {
            // 主干会话前置——与前端列表（BuildCatsJson）同口径：多猫注册表 _cats 不含 majordomo
            System.Text.StringBuilder sb = new System.Text.StringBuilder();
            sb.Append("cat.list | " + (_cats.Count + 1).ToString() + " 只猫");
            sb.Append(Environment.NewLine);
            string majorState;
            if (_majorHost != null)
            {
                majorState = "运行中 :" + _majorPort.ToString();
            }
            else
            {
                majorState = "静默";
            }
            sb.Append("  majordomo | majordomo | " + majorState + " | 主干会话（禁停禁删）");
            for (int i = 0; i < _cats.Count; i++)
            {
                CatEntry cat = _cats[i];
                string state;
                if (cat.Running)
                {
                    state = "运行中 :" + cat.Port.ToString();
                }
                else
                {
                    state = "静默";
                }
                sb.Append(Environment.NewLine);
                sb.Append("  " + cat.Id + " | " + cat.DisplayName + " | " + state);
            }
            return sb.ToString();
        }
        /// <summary>
        /// cat.info——全猫状态统计（主干 + 多猫；字段与快照 sessions 段 / info tokens 段同源——不另起数据面）。
        /// 返回体 = 整块分类 JSON（缩进 + 中文直显——同 info 规格，无「头 + 正文」两段）。
        /// </summary>
        /// <returns>JSON 文本</returns>
        private static string HandleCatInfo()
        {
            List<object> catList = new List<object>();
            ChatSession majorSession = null;
            if (_chatBridge != null)
            {
                majorSession = _chatBridge.DefaultSession;
            }
            catList.Add(BuildCatInfoEntry("majordomo", "majordomo", _majorHost != null, _majorPort, true, majorSession, ""));
            for (int i = 0; i < _cats.Count; i = i + 1)
            {
                CatEntry cat = _cats[i];
                catList.Add(BuildCatInfoEntry(cat.Id, cat.DisplayName, cat.Running, cat.Port, false, cat.Session, cat.Type));
            }
            Dictionary<string, object> info = new Dictionary<string, object>();
            info["ok"] = true;
            info["tool"] = "cat.info";
            info["time"] = new { now = DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss") };
            info["count"] = catList.Count;
            info["cats"] = catList;
            return JsonSerializer.Serialize(info, InfoJsonOptions);
        }
        /// <summary>
        /// 单猫状态条目——运行面（运行态 / 端口 / 主干标记）+ 会话观测字段（与快照 sessions 段 / info tokens 段同源）。
        /// context 取 ContextTokensKnown（请求级实时优先，未请求回落轮末落盘值——看别猫场景 idle 猫亦有值）。
        /// 会话缺失（未附加 / 已销毁）时只输出运行面字段——不补空值、不写 NaN（同 info「无则省略键」口径）。
        /// </summary>
        /// <param name="id">猫 id</param>
        /// <param name="name">显示名</param>
        /// <param name="running">是否运行</param>
        /// <param name="port">监听端口（0=未监听）</param>
        /// <param name="special">是否主干特殊会话（禁停禁删）</param>
        /// <param name="session">会话实体（可空）</param>
        /// <returns>状态条目字典</returns>
        private static Dictionary<string, object> BuildCatInfoEntry(string id, string name, bool running, int port, bool special, ChatSession session, string type)
        {
            Dictionary<string, object> entry = new Dictionary<string, object>();
            entry["id"] = id;
            entry["name"] = name;
            entry["type"] = type == null ? "" : type;
            entry["special"] = special;
            entry["running"] = running;
            entry["port"] = port;
            if (session == null)
            {
                return entry;
            }
            // A225 会话态统一投影——猫列表 / 快照会话段 / cat.info 同源（ChatSession.BuildStateEntry）
            Dictionary<string, object> state = session.BuildStateEntry();
            foreach (KeyValuePair<string, object> kv in state)
            {
                entry[kv.Key] = kv.Value;
            }
            return entry;
        }

        /// <summary>
        /// cat.start——分配端口 + 拉起 HttpHost + 会话附加（竞态换端口重试一次）。
        /// </summary>
        /// <param name="key">猫寻址键（id 精确/前缀唯一/显示名）</param>
        /// <returns>结果文本</returns>
        private static string HandleCatStart(string key)
        {
            CatEntry cat = FindCat(key);
            if (cat == null)
            {
                return "cat.start | 未找到猫: " + key;
            }
            if (cat.Running)
            {
                return "cat.start | 已在运行: " + cat.DisplayName + " :" + cat.Port.ToString();
            }
            int port = AllocatePort(PortBand.FamilyFrom);
            if (port < 0)
            {
                return "cat.start | 端口分配失败（端口段 " + PortBand.FamilyFrom.ToString() + "-" + PortBand.FamilyTo.ToString() + " 全占用）";
            }
            HttpHost host;
            try
            {
                host = StartCatHost(cat, port);
            }
            catch (Exception ex)
            {
                // 试绑后释放的竞态窗口——换端口重试一次
                int retryPort = AllocatePort(port + PortBand.FamilyStep());
                if (retryPort < 0)
                {
                    return "cat.start | 端口绑定失败: " + ex.Message;
                }
                host = StartCatHost(cat, retryPort);
                port = retryPort;
            }
            cat.Running = true;
            cat.Port = port;
            cat.Host = host;
            AttachSessionHost(cat.Session, host);
            SetCatRuntime(cat.Id, true, port);
            LogStore.Add("CatHome4", 1, "已启动猫「" + cat.DisplayName + "」，端口 " + port.ToString(), "CHAT");
            return "cat.start | " + cat.DisplayName + " | http://127.0.0.1:" + port.ToString();
        }

        /// <summary>
        /// cat.stop——停 HttpHost + 回静默态 + cfg 更新。
        /// </summary>
        /// <param name="key">猫寻址键</param>
        /// <returns>结果文本</returns>
        private static string HandleCatStop(string key)
        {
            // F2.2 majordomo 特殊会话——强制自启无关闭（拒绝）
            if (string.Equals(key, "majordomo", StringComparison.Ordinal))
            {
                return "cat.stop | majordomo 为特殊会话——强制自启，不可停止";
            }
            CatEntry cat = FindCat(key);
            if (cat == null)
            {
                return "cat.stop | 未找到猫: " + key;
            }
            if (!cat.Running)
            {
                return "cat.stop | 已是静默态: " + cat.DisplayName;
            }
            try
            {
                cat.Host.Stop();
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "停止猫「" + cat.DisplayName + "」异常：" + ex.Message, "CHAT");
            }
            cat.Host = null;
            cat.Running = false;
            cat.Port = 0;
            SetCatRuntime(cat.Id, false, 0);
            LogStore.Add("CatHome4", 1, "已停止猫「" + cat.DisplayName + "」", "CHAT");
            return "cat.stop | " + cat.DisplayName + " | 已停止";
        }

        /// <summary>
        /// cat.delete——本地销毁（停 + 移除注册表/轮转表 + 删目录与前文文件）。
        /// </summary>
        /// <param name="key">猫寻址键</param>
        /// <returns>结果文本</returns>
        private static string HandleCatDelete(string key)
        {
            // F2.2 majordomo 特殊会话——不可删除（拒绝）
            if (string.Equals(key, "majordomo", StringComparison.Ordinal))
            {
                return "cat.delete | majordomo 为特殊会话——不可删除";
            }
            CatEntry cat = FindCat(key);
            if (cat == null)
            {
                return "cat.delete | 未找到猫: " + key;
            }
            if (cat.Running)
            {
                try
                {
                    cat.Host.Stop();
                }
                catch (Exception ex)
                {
                    // 停止异常不阻断删除
                    LogStore.Add("AdminService", 2, "停止猫实例异常，不阻断删除: " + ex.Message, "SYS");
                }
            }
            _cats.Remove(cat);
            _chatBridge.RemoveSession(cat.Session);
            // M4e 猫级白名单——缓存随猫销毁
            ToolCatContext.RemoveCatFileSystem(cat.Id);
            // 运行态条目随猫销毁（Data/runtime/cats.json）
            RemoveCatRuntime(cat.Id);
            DeleteCatFiles(cat.Id);
            LogStore.Add("CatHome4", 1, "已销毁猫「" + cat.DisplayName + "」（id " + cat.Id + "）", "CHAT");
            return "cat.delete | " + cat.DisplayName + " | 已销毁";
        }

        /// <summary>
        /// 建立猫实体——上下文 + 前文恢复/注入 + ChatSession 构造 + 轮转注册（cat.new 与启动扫描共用）。
        /// </summary>
        /// <param name="id">会话 ID</param>
        /// <param name="displayName">显示名</param>
        /// <param name="seedMessages">fork 播种前文（null/空=不播种——正常注入/恢复语义）</param>
        /// <returns>猫实体；构造失败 null</returns>
        /// <param name="forkSource">fork 源会话（空 = 常规建造路径——视图 / 元数据走读盘恢复）</param>
        /// <param name="forkMsgIndex">fork 切点前文索引（-1 = 非 fork 路径）</param>
        private static CatEntry CreateCatEntry(string id, string displayName, LlmMessage[] seedMessages, ChatSession forkSource = null, int forkMsgIndex = -1)
        {
            try
            {
                // [段1] 每猫 API 引用解析——cat.cfg apiConfigId → Guid.Empty=默认端点语义（Cat 可选配置；未配置走默认端点）
                CatCfgData cfgData = LoadCatCfg(Path.Combine(_dataRoot, "Data", "sessions", id, "cat.cfg"));
                Guid apiConfigId = Guid.Empty;
                if (cfgData != null && cfgData.ApiConfigId != null && cfgData.ApiConfigId.Length > 0)
                {
                    Guid parsed;
                    if (Guid.TryParse(cfgData.ApiConfigId, out parsed) && parsed != Guid.Empty)
                    {
                        apiConfigId = parsed;
                    }
                }
                CH_LlmApiConfigStore apiStore = null;
                DataBox.TryResolve<CH_LlmApiConfigStore>(out apiStore);
                if (apiStore == null)
                {
                    // 防御——Bootstrap 已绑定，实际不触发；空 Store 保证非空
                    apiStore = new CH_LlmApiConfigStore(Path.Combine(_dataRoot, "Data", "config"), Path.Combine(_dataRoot, "Data", "secrets"));
                }
                CH_LlmApiConfig apiConfig = new CH_LlmApiConfig();
                if (apiConfigId == Guid.Empty)
                {
                    CH_LlmApiConfig defaultConfig = apiStore.ResolveDefault();
                    if (defaultConfig != null)
                    {
                        apiConfig = defaultConfig;
                    }
                }
                else
                {
                    apiStore.TryGet(apiConfigId, out apiConfig);
                }
                ConfigStore globalConfig = null;
                DataBox.TryResolve<ConfigStore>(out globalConfig);
                // R2.3 每猫 qqbot 配置——qqbotId/qqbotEnable（cfg 缺失=未绑定/禁用）
                Guid qqBotId = Guid.Empty;
                bool qqBotEnable = false;
                if (cfgData != null)
                {
                    if (cfgData.QqBotId != null && cfgData.QqBotId.Length > 0)
                    {
                        Guid parsed;
                        if (Guid.TryParse(cfgData.QqBotId, out parsed) && parsed != Guid.Empty)
                        {
                            qqBotId = parsed;
                        }
                    }
                    qqBotEnable = cfgData.QqBotEnable;
                }
                // [段2] 每猫配置四字段——persona/toolNames/injectList/packs（M2/R4；cfg 缺失=新猫走全局默认模板继承）
                string persona = "";
                string type = "";
                string toolNames = "";
                string[] injectList = new string[0];
                string[] packs = new string[0];
                bool userInjectIdleOnly = false;
                string reasoningEffort = "";
                Guid appearanceId = Guid.Empty;
                bool roundSummary = false;
                string petKind = "";
                if (cfgData != null)
                {
                    if (cfgData.Persona != null)
                    {
                        persona = cfgData.Persona;
                    }
                    if (cfgData.Type != null)
                    {
                        type = cfgData.Type;
                    }
                    if (cfgData.ToolNames != null)
                    {
                        toolNames = cfgData.ToolNames;
                    }
                    if (cfgData.InjectList != null)
                    {
                        injectList = cfgData.InjectList;
                    }
                    if (cfgData.Packs != null)
                    {
                        packs = cfgData.Packs;
                    }
                    userInjectIdleOnly = cfgData.UserInjectIdleOnly;
                    if (cfgData.ReasoningEffort != null)
                    {
                        reasoningEffort = cfgData.ReasoningEffort;
                    }
                    appearanceId = ResolveAppearanceId(cfgData);
                    // A244——roundSummary 迁入外观池：经猫 key 解析生效值（cat.cfg 不再持该字段）
                    roundSummary = ResolveEffectiveRoundSummary(id);
                    petKind = ResolveEffectivePetKind(id);
                }
                else
                {
                    // 新猫——cat-default.cfg 模板继承（模板缺失=现状空四字段）
                    CatDefaultCfgData tpl = LoadCatDefaultCfg();
                    if (tpl != null)
                    {
                        persona = tpl.DefaultPersona != null ? tpl.DefaultPersona : "";
                        toolNames = tpl.DefaultToolNames != null ? tpl.DefaultToolNames : "";
                        injectList = tpl.DefaultInjectList != null ? tpl.DefaultInjectList : new string[0];
                        packs = tpl.DefaultPacks != null ? tpl.DefaultPacks : new string[0];
                        userInjectIdleOnly = tpl.DefaultUserInjectIdleOnly;
                    }
                }
                // M2c 声明面裁剪——读时比对（非法名过滤/全空全量保底）
                ToolSpec[] catSpecs = FilterToolSpecs(ResolveToolNames(toolNames, false), false);
                // [段3] 上下文 + 前文恢复/注入
                ChatContext context = new ChatContext();
                SessionStore store = new SessionStore(Path.Combine(_dataRoot, "Data", "sessions", id, id + ".jsonl"));
                store.SessionId = id;
                LlmMessage[] restored;
                if (store.TryLoad(out restored))
                {
                    context.ReplaceMessages(restored);
                }
                else if (seedMessages != null && seedMessages.Length > 0)
                {
                    // fork 播种——以源猫节点前前文为起点（含源 system——注入内容已在其中；覆盖注入分支）
                    context.ReplaceMessages(seedMessages);
                }
                else
                {
                    // 无前文 = 新猫——按 cat.cfg injectList 清单注入（M2d：仅读 List 内文件，List 外一律不加载）
                    WorkspaceConfig workspace = null;
                    DataBox.TryResolve<WorkspaceConfig>(out workspace);
                    string injectPrompt = _chatBridge.BuildPrompt(workspace, catSpecs, persona, injectList);
                    context.SetSystemPrompt(injectPrompt);
                }
                // 会话标识 ≡ 猫 key（唯一标识——不再有独立"会话身份"层）；哨兵：标识被外部改写时校正并回写（常态恒等不触发）
                if (!string.Equals(store.SessionId, id, StringComparison.Ordinal))
                {
                    store.SessionId = id;
                    store.Rewrite(context.GetMessages());
                    LogStore.Add("CatHome4", 1, "会话标识对齐猫 key: " + id, "CHAT");
                }
                // [段4] 会话构造——M1c 每猫独立 Runtime（API 配置池按该猫 apiConfigId 构造）；M2c 声明面按猫裁剪
                ILlmRuntime catRuntime = new DeepSeekLlmRuntime(apiStore, apiConfigId, globalConfig, reasoningEffort);
                SessionViewStore viewStore = new SessionViewStore(Path.Combine(_dataRoot, "Data", "sessions", id, id + ".view.jsonl"));
                // A221 工具面解析——持久化快照优先（会话工具头与 system prompt 同命；重启不改面）
                ToolSpec[] sessionTools = ChatSession.ResolveSessionTools(id, catSpecs);
                ChatSession session = new ChatSession(id, displayName, context, store, catRuntime, _oa, sessionTools, ExecuteTool, viewStore);
                // M4e 猫级白名单——多猫启用根（cat.cfg enabledRoots；缺省全量）+ 工具执行猫上下文
                session.SetCatKey(id);
                ApplyCatRoots(id);
                session.AttachEnvInfo(() => BuildEnvInfoProvider());
                session.AttachRoundNotify(NotifyBalloon);
                session.SetUserInjectIdleOnly(userInjectIdleOnly);
                session.SetRoundSummary(roundSummary);
                session.SetPetKind(petKind);
                // [段3b] 视图面 / 元数据面——fork 播种走克隆（源面保留部分），常规路径读盘恢复
                if (forkSource != null && forkMsgIndex >= 0)
                {
                    session.SeedViewFrom(forkSource, forkMsgIndex);
                    session.SeedMetaFrom(forkSource);
                }
                else
                {
                    session.LoadView();
                    // A201 会话元数据——启动恢复（实例 ID / 创建时刻 / 两级 token 累计 + A202 请求边界态；文件缺失走首建）
                    session.LoadMeta();
                }
                _chatBridge.RegisterSession(session);
                CatEntry cat = new CatEntry();
                cat.Id = id;
                cat.DisplayName = displayName;
                cat.Running = false;
                cat.Port = 0;
                cat.Session = session;
                cat.Host = null;
                cat.ApiConfigId = apiConfigId;
                cat.ApiConfig = apiConfig;
                cat.Persona = persona;
                cat.Type = type;
                cat.ToolNames = toolNames;
                cat.InjectList = injectList;
                cat.Packs = packs;
                cat.UserInjectIdleOnly = userInjectIdleOnly;
                cat.ReasoningEffort = reasoningEffort;
                cat.AppearanceId = appearanceId;
                cat.RoundSummary = roundSummary;
                cat.ToolSpecs = catSpecs;
                // §4.3 空值语义可观测——toolNames 空=全量保底（反直觉默认）：解析结果落审计行
                string toolSource = "子集";
                int toolDeclared = 0;
                if (toolNames == null || toolNames.Length == 0)
                {
                    toolSource = "全量";
                }
                else
                {
                    toolDeclared = toolNames.Split(',').Length;
                }
                int toolActual = 0;
                if (catSpecs != null)
                {
                    toolActual = catSpecs.Length;
                }
                LogStore.Add("CatHome4", 1, "cat.tools | cat=" + id + " | 来源=" + toolSource + " | 声明=" + toolDeclared.ToString() + " | 实际=" + toolActual.ToString(), "CHAT");
                cat.QqBotId = qqBotId;
                cat.QqBotEnable = qqBotEnable;
                LogStore.Add("CatHome4", 1, "猫「" + displayName + "」绑定 LLM 配置：" + (apiConfigId == Guid.Empty ? "默认端点" : apiConfigId.ToString("D")) + "，模型 " + apiConfig.DefaultModel, "CHAT");
                return cat;
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 3, "创建猫实体失败：" + ex.Message, "CHAT");
                return null;
            }
        }
        /// <summary>
        /// 解析猫的有效 LLM API 配置——cat.cfg apiConfigId 零值/缺失=跟随全局默认端点，非零=按身份取（A50 端点解析同源化）。
        /// 消费面：info 环境信息（本猫实际生效端点）；默认猫与多猫同源。
        /// </summary>
        /// <param name="catKey">猫 key（majordomo=默认猫；多猫=会话 ID）</param>
        /// <param name="followDefault">输出：true=跟随全局默认端点；false=猫显式绑定（引用失效时同样为 false）</param>
        /// <returns>生效配置；无默认端点/引用失效 null</returns>
        internal static CH_LlmApiConfig ResolveEffectiveApiConfig(string catKey, out bool followDefault)
        {
            followDefault = true;
            Guid apiConfigId = Guid.Empty;
            if (catKey != null && catKey.Length > 0)
            {
                CatCfgData cfg = LoadCatCfg(Path.Combine(_dataRoot, "Data", "sessions", catKey, "cat.cfg"));
                apiConfigId = ResolveApiConfigId(cfg);
            }
            if (_apiStore == null)
            {
                return null;
            }
            if (apiConfigId == Guid.Empty)
            {
                return _apiStore.ResolveDefault();
            }
            followDefault = false;
            CH_LlmApiConfig config;
            if (_apiStore.TryGet(apiConfigId, out config))
            {
                return config;
            }
            return null;
        }

        /// <summary>
        /// 收集所有绑定 qqbot 的猫——默认猫（majordomo）+ 多猫（R2.3.5 输出转发轮询面；A58 的 1:1 约束在 Bot 侧——每猫各自的 Bot）。
        /// </summary>
        /// <returns>全部绑定目标（QqBotId 非空）</returns>
        internal static List<QqTarget> CollectAllQqTargets()
        {
            List<QqTarget> result = new List<QqTarget>();
            if (_chatBridge.DefaultQqBotId != Guid.Empty)
            {
                ChatSession captured = _chatBridge.DefaultSession;
                result.Add(new QqTarget
                {
                    Key = "majordomo",
                    DisplayName = "majordomo",
                    QqBotId = _chatBridge.DefaultQqBotId,
                    Enable = _chatBridge.DefaultQqBotEnable,
                    Inject = delegate (string s, string origin) { return captured.PostUserMessage(s, "user", "", origin); },
                    GetViewItems = delegate () { return ConvertQqViewItems(captured.ViewStore.GetBlocks()); },
                    GetBlockFingerprint = delegate (int index) { return ComputeBlockFingerprint(captured.ViewStore.GetBlocks(), index); },
                    ParseImages = ParseQqImages,
                    IsIdle = delegate () { return captured.IsIdle; },
                    IsTimebackActive = delegate () { return captured.TimebackActive; },
                    NewSession = delegate ()
                    {
                        PostInput(MainInputTarget(), "session.new");
                        return "新会话已请求（注入执行中）\n" + _chatBridge.BuildInjectSummary(_chatBridge.DefaultPersona, _chatBridge.DefaultInjectList, _chatBridge.DefaultToolSpecs);
                    },
                    GetInfo = delegate () { return captured.GetInfoText(); }
                });
            }
            for (int i = 0; i < _cats.Count; i++)
            {
                CatEntry cat = _cats[i];
                if (cat.QqBotId != Guid.Empty)
                {
                    CatEntry captured = cat;
                    result.Add(new QqTarget
                    {
                        Key = cat.Id,
                        DisplayName = cat.DisplayName,
                        QqBotId = cat.QqBotId,
                        Enable = cat.QqBotEnable,
                        Inject = delegate (string s, string origin) { return captured.Session.PostUserMessage(s, "user", "", origin); },
                        GetViewItems = delegate () { return ConvertQqViewItems(captured.Session.ViewStore.GetBlocks()); },
                        GetBlockFingerprint = delegate (int index) { return ComputeBlockFingerprint(captured.Session.ViewStore.GetBlocks(), index); },
                        ParseImages = ParseQqImages,
                        IsIdle = delegate () { return captured.Session.IsIdle; },
                        IsTimebackActive = delegate () { return captured.Session.TimebackActive; },
                        NewSession = delegate ()
                        {
                            PostInput(CatInputTarget(captured), "session.new");
                            return "新会话已请求（注入执行中）\n" + _chatBridge.BuildInjectSummary(captured.Persona, captured.InjectList, captured.ToolSpecs);
                        },
                        GetInfo = delegate () { return captured.Session.GetInfoText(); }
                    });
                }
            }
            return result;
        }

        /// <summary>
        /// 取绑定指定 qqbot 的猫（A58 1:1——一个 Bot 唯一对应一只猫：重复配置取首个 + L2 留痕）。
        /// </summary>
        /// <param name="qqBotId">qqbot 配置身份</param>
        /// <returns>绑定目标列表（≤1 项；空=未绑定任何猫）</returns>
        internal static List<QqTarget> CollectQqTargets(Guid qqBotId)
        {
            List<QqTarget> result = new List<QqTarget>();
            List<QqTarget> all = CollectAllQqTargets();
            for (int i = 0; i < all.Count; i = i + 1)
            {
                if (all[i].QqBotId != qqBotId)
                {
                    continue;
                }
                if (result.Count == 0)
                {
                    result.Add(all[i]);
                    continue;
                }
                // 1:1 约束外（旧数据 / 手工改 cat.cfg）——取首个 + L2 留痕（可诊断）
                LogStore.Add("QQBot", 2, "同一 Bot 被多猫绑定（取首个） | " + qqBotId.ToString("D") + " | 首个 " + result[0].Key + " · 忽略 " + all[i].Key, "QQBOT");
                break;
            }
            return result;
        }

        /// <summary>
        /// qqbot 绑定占用者查询（A58 1:1）——返回除 catKey 自身外绑定了该 Bot 的猫显示名（空=无人占用）。
        /// 两处消费：保存配置时查重（冲突显式拒绝）· 配置读面标注（前端禁用已绑他猫的 Bot）。
        /// </summary>
        /// <param name="catKey">当前猫寻址键（majordomo=默认猫；自身不计入占用）</param>
        /// <param name="qqbotId">Bot 身份（Guid 文本）</param>
        /// <returns>占用者显示名（空串=无占用）</returns>
        internal static string FindQqBotBindingOwner(string catKey, string qqbotId)
        {
            Guid target;
            if (!Guid.TryParse(qqbotId, out target) || target == Guid.Empty)
            {
                return "";
            }
            if (catKey != "majordomo" && _chatBridge.DefaultQqBotId == target)
            {
                return "majordomo";
            }
            for (int i = 0; i < _cats.Count; i = i + 1)
            {
                CatEntry cat = _cats[i];
                if (cat.Id == catKey)
                {
                    continue;
                }
                if (cat.QqBotId == target)
                {
                    return cat.DisplayName;
                }
            }
            return "";
        }

        /// <summary>
        /// QQBot 渠道说明串——info 环境信息消费（A58）：该猫已绑定且启用 qqbot 转发时给出文件发送用法；
        /// 未绑定 / 未启用 → 空串（info 不显示任何 qqbot 相关内容）。
        /// </summary>
        /// <param name="catKey">猫 key（majordomo=默认猫）</param>
        /// <returns>说明串（空=未接入 qqbot）</returns>
        internal static string BuildCatQqBotInfo(string catKey)
        {
            if (catKey == null || catKey.Length == 0)
            {
                return "";
            }
            Guid botId;
            bool enabled;
            if (catKey == "majordomo")
            {
                botId = _chatBridge.DefaultQqBotId;
                enabled = _chatBridge.DefaultQqBotEnable;
            }
            else
            {
                CatEntry cat = FindCat(catKey);
                if (cat == null)
                {
                    return "";
                }
                botId = cat.QqBotId;
                enabled = cat.QqBotEnable;
            }
            if (botId == Guid.Empty || !enabled)
            {
                return "";
            }
            string botName = ResolveQqBotDisplayName(botId);
            if (botName.Length == 0)
            {
                botName = botId.ToString("D");
            }
            return botName + "：发本地文件=单独一行写 [QQBot发送文件:\"<真实绝对路径>\"]（≤10MB · 私聊/群聊）";
        }
        /// <summary>
        /// 本地对话端点端口解析——info 环境信息消费（本猫实际监听端口）。
        /// majordomo = 独立对话端口；多猫 = 注册表运行态端口；未监听 / 未知 key = 0。
        /// </summary>
        /// <param name="catKey">猫 key（majordomo=默认猫）</param>
        /// <returns>监听端口；0=未监听</returns>
        internal static int ResolveCatPort(string catKey)
        {
            if (catKey == null || catKey.Length == 0)
            {
                return 0;
            }
            if (catKey == "majordomo")
            {
                if (_majorPort < 0)
                {
                    return 0;
                }
                return _majorPort;
            }
            CatEntry cat = FindCat(catKey);
            if (cat == null || !cat.Running)
            {
                return 0;
            }
            return cat.Port;
        }

        /// <summary>
        /// Bot 显示名解析——Bot 池按身份查显示名（池内缺失 → 空串，调用方回退身份串）。
        /// </summary>
        /// <param name="botId">Bot 配置身份</param>
        /// <returns>显示名（空=池内缺失）</returns>
        internal static string ResolveQqBotDisplayName(Guid botId)
        {
            CH_QqBotConfigStore store = null;
            DataBox.TryResolve<CH_QqBotConfigStore>(out store);
            if (store == null)
            {
                return "";
            }
            CH_QqBotConfig cfg;
            if (store.TryGet(botId, out cfg) && cfg != null && cfg.DisplayName != null)
            {
                return cfg.DisplayName;
            }
            return "";
        }

        /// <summary>
        /// 视图块 → QQ 转发项转换——窄 DTO（QQ 域消费 RenderType / Content / Done / Origin）。
        /// text / gap_text 块提取 payload.text；user 块提取 payload.origin（QQ 注入的轮起点锚——转发面据此绑定来源与轮）。
        /// </summary>
        /// <param name="blocks">视图块数组</param>
        /// <returns>QQ 转发项数组</returns>
        private static QqViewItem[] ConvertQqViewItems(ViewBlock[] blocks)
        {
            if (blocks == null)
            {
                return new QqViewItem[0];
            }
            QqViewItem[] items = new QqViewItem[blocks.Length];
            for (int i = 0; i < blocks.Length; i = i + 1)
            {
                ViewBlock b = blocks[i];
                QqViewItem item = new QqViewItem();
                item.RenderType = b.RenderType ?? "";
                item.Content = "";
                item.Done = "";
                item.Origin = "";
                if (item.RenderType == "text" || item.RenderType == "gap_text")
                {
                    item.Content = ExtractTextContent(b.Payload);
                }
                else if (item.RenderType == "roundsum")
                {
                    item.Done = ExtractRoundSumDone(b.Payload);
                }
                else if (item.RenderType == "user")
                {
                    item.Origin = ExtractOrigin(b.Payload);
                }
                items[i] = item;
            }
            return items;
        }

        /// <summary>
        /// 提取 user 块的原注入来源标记——payload.origin（空 / 缺失 = 非 QQ 注入；防御式解析失败回落空串）。
        /// </summary>
        /// <param name="payloadJson">块载荷 JSON</param>
        /// <returns>origin 值（空=非 QQ 注入）</returns>
        private static string ExtractOrigin(string payloadJson)
        {
            if (payloadJson == null || payloadJson.Length == 0)
            {
                return "";
            }
            try
            {
                using (JsonDocument d = JsonUtil.ParseStrict(payloadJson))
                {
                    if (d.RootElement.TryGetProperty("origin", out JsonElement c) && c.ValueKind == JsonValueKind.String)
                    {
                        return c.GetString() ?? "";
                    }
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "载荷 origin 提取失败（回落空）: " + ex.Message, "CHAT");
            }
            return "";
        }

        /// <summary>
        /// 块内容指纹——按块下标取（A111 续接锚点）：渲染类型 + 前文索引 + 载荷全文经 SHA256 取前 16 位十六进制。
        /// 判据：与实际产物同源（同一 ViewBlock 载荷）——重建后前缀一致则指纹一致，块序变更后仍可精确定位。
        /// </summary>
        /// <param name="blocks">视图块数组</param>
        /// <param name="index">块下标（越界返回空串）</param>
        /// <returns>指纹（空=不可作锚）</returns>
        private static string ComputeBlockFingerprint(ViewBlock[] blocks, int index)
        {
            if (blocks == null || index < 0 || index >= blocks.Length)
            {
                return "";
            }
            ViewBlock b = blocks[index];
            string raw = (b.RenderType ?? "") + "\u0001" + b.MsgIndex.ToString() + "\u0001" + (b.Payload ?? "");
            byte[] hash = System.Security.Cryptography.SHA256.HashData(System.Text.Encoding.UTF8.GetBytes(raw));
            System.Text.StringBuilder sb = new System.Text.StringBuilder();
            for (int i = 0; i < 8; i = i + 1)
            {
                sb.Append(hash[i].ToString("x2"));
            }
            return sb.ToString();
        }

        /// <summary>
        /// 提取 text 块内容——payload JSON 的 text 字段（A165 v2 键名统一；防御式解析失败返回空串）。
        /// </summary>
        /// <param name="payloadJson">块载荷 JSON</param>
        /// <returns>内容文本</returns>
        private static string ExtractTextContent(string payloadJson)
        {
            if (payloadJson == null || payloadJson.Length == 0)
            {
                return "";
            }
            try
            {
                using (JsonDocument d = JsonUtil.ParseStrict(payloadJson))
                {
                    if (d.RootElement.TryGetProperty("text", out JsonElement c) && c.ValueKind == JsonValueKind.String)
                    {
                        return c.GetString() ?? "";
                    }
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "载荷 text 提取失败（回落空）: " + ex.Message, "CHAT");
            }
            return "";
        }

        /// <summary>
        /// 提取 roundsum 块的 done 字段——本轮结束语义（tool=工具主动 done / stream=流式自然收尾）。
        /// 缺失或解析失败回退 stream（保守按自然收尾：来源照常出队消费，不误留）。
        /// </summary>
        /// <param name="payloadJson">roundsum 载荷 JSON（{"type":"roundsum","data":{...,"done":"..."}}）</param>
        /// <returns>done 值（tool / stream）</returns>
        private static string ExtractRoundSumDone(string payloadJson)
        {
            if (payloadJson == null || payloadJson.Length == 0)
            {
                return "stream";
            }
            try
            {
                using (JsonDocument d = JsonUtil.ParseStrict(payloadJson))
                {
                    JsonElement data;
                    if (d.RootElement.TryGetProperty("data", out data) && data.ValueKind == JsonValueKind.Object)
                    {
                        JsonElement done;
                        if (data.TryGetProperty("done", out done) && done.ValueKind == JsonValueKind.String)
                        {
                            string got = done.GetString();
                            if (got != null && got.Length > 0)
                            {
                                return got;
                            }
                        }
                    }
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "roundsum done 提取失败（按 stream 处理）: " + ex.Message, "CHAT");
            }
            return "stream";
        }

        /// <summary>
        /// 猫寻址——id 精确 / 显示名精确 / id 前缀唯一（歧义返回 null 要求更精确）。
        /// </summary>
        /// <param name="key">寻址键</param>
        /// <returns>猫实体；未找到/歧义 null</returns>
        internal static CatEntry FindCat(string key)
        {
            if (key == null || key.Length == 0)
            {
                return null;
            }
            CatEntry exact = null;
            CatEntry prefix = null;
            for (int i = 0; i < _cats.Count; i++)
            {
                CatEntry cat = _cats[i];
                if (cat.Id == key || cat.DisplayName == key)
                {
                    exact = cat;
                    break;
                }
                if (cat.Id.StartsWith(key, StringComparison.Ordinal))
                {
                    if (prefix != null)
                    {
                        return null;
                    }
                    prefix = cat;
                }
            }
            if (exact != null)
            {
                return exact;
            }
            return prefix;
        }
        /// <summary>
        /// 默认会话判定——特权工具永久激活基准（猫 key = majordomo / 空；design-ch4-tools §三·十）。
        /// </summary>
        /// <param name="key">猫寻址键</param>
        /// <returns>true=默认会话</returns>
        internal static bool IsDefaultCatKey(string key)
        {
            if (key == null || key.Length == 0)
            {
                return true;
            }
            return string.Equals(key, "majordomo", StringComparison.Ordinal);
        }

        /// <summary>
        /// 当前授权工具名——工具判定授权面的实时查询入口（design-ch4-tools §三·十一）。
        /// 实时解析当前 cat.cfg 名单（空 / 全非法 → 全量保底；随注册表变化即时反映）；默认猫读静态面原始串，多猫读 CatEntry。
        /// </summary>
        /// <param name="catKey">猫键（null / 空 / majordomo = 默认猫）</param>
        /// <returns>授权工具名数组（非 null）</returns>
        internal static string[] ResolveCatAuthorizedToolNames(string catKey)
        {
            string raw = "";
            if (catKey == null || catKey.Length == 0 || catKey == "majordomo")
            {
                if (_chatBridge != null)
                {
                    raw = _chatBridge.DefaultToolNames;
                }
            }
            else
            {
                CatEntry cat = FindCat(catKey);
                if (cat != null && cat.ToolNames != null)
                {
                    raw = cat.ToolNames;
                }
            }
            if (raw == null)
            {
                raw = "";
            }
            return ResolveToolNames(raw, IsDefaultCatKey(catKey));
        }

        /// <summary>
        /// 猫会话——按猫键取会话实体（M4e 猫级白名单同源寻址；majordomo/空键走默认会话）。
        /// 消费面：info 自查、快照 sessions 段的实时字段（前文长度 / 最近前文变动时刻）。
        /// </summary>
        /// <param name="key">猫键（id/显示名；null/空=majordomo 默认会话）</param>
        /// <returns>会话；未找到 null</returns>
        internal static ChatSession FindCatSession(string key)
        {
            if (key == null || key.Length == 0 || key == "majordomo")
            {
                if (_chatBridge != null && _chatBridge.DefaultSession != null)
                {
                    return _chatBridge.DefaultSession;
                }
                return null;
            }
            CatEntry cat = FindCat(key);
            if (cat != null && cat.Session != null)
            {
                return cat.Session;
            }
            return null;
        }

        /// <summary>
        /// 动态端口分配——从起始端口起沿端口段方向 TCP 试绑（试绑后释放；竞态由 cat.start 重试兜底）。
        /// 扫描边界 = 端口段终点（开发区 8070 / 部署区 8180）——段内全占即失败，不越界到对方区段。
        /// </summary>
        /// <param name="startPort">起始端口（含；应在端口段内）</param>
        /// <returns>可用端口；段内全占用 -1</returns>
        private static int AllocatePort(int startPort)
        {
            if (PortBand == null)
            {
                LogStore.Add("CatHome4", 2, "端口分配失败：端口段未注入（AdminService.Configure 未接线）", "CHAT");
                return -1;
            }
            int step = PortBand.FamilyStep();
            int limit = PortBand.FamilyTo;
            int port = startPort;
            while (true)
            {
                if (step > 0 && port > limit)
                {
                    break;
                }
                if (step < 0 && port < limit)
                {
                    break;
                }
                if (!IsPortTaken(port))
                {
                    try
                    {
                        TcpListener listener = new TcpListener(IPAddress.Loopback, port);
                        listener.Start();
                        listener.Stop();
                        return port;
                    }
                    catch (Exception ex)
                    {
                        // 试绑失败——继续下一端口
                        LogStore.Add("AdminService", 1, "端口试绑未成，继续下一端口: " + ex.Message, "SYS");
                    }
                }
                port = port + step;
            }
            return -1;
        }

        /// <summary>
        /// 端口占用检查——注册表运行中猫的端口集合（系统占用由试绑探测）。
        /// </summary>
        /// <param name="port">端口</param>
        /// <returns>true=注册表内已占用</returns>
        private static bool IsPortTaken(int port)
        {
            for (int i = 0; i < _cats.Count; i++)
            {
                if (_cats[i].Running && _cats[i].Port == port)
                {
                    return true;
                }
            }
            return false;
        }

        /// <summary>
        /// 每猫 HttpHost 启动——回调闭包按猫（快照全局 / dispatcher 按猫 / history 按猫 / chat.html 页）。
        /// </summary>
        /// <param name="cat">猫实体</param>
        /// <param name="port">监听端口</param>
        /// <returns>HttpHost 实例</returns>
        /// <summary>
        /// 会话 ↔ 宿主绑定——推送面 + 连接面一次注入（单一出口；三条启动路径共用）。
        /// 连接面 = state 段 conn.clients 数据源（A186）；漏注入时 clients 恒 0——该值本身在契约里可见，不静默。
        /// </summary>
        /// <param name="session">目标会话</param>
        /// <param name="host">该会话的 HTTP 外观层实例</param>
        private static void AttachSessionHost(ChatSession session, HttpHost host)
        {
            session.AttachHost(host);
            session.AttachConnInfo(() => host.ClientCount);
        }

        /// <summary>启动猫的 HTTP 外观层实例——按端口起 Host 并返回句柄（三条启动路径共用）。</summary>
        private static HttpHost StartCatHost(CatEntry cat, int port)
        {
            return HttpHost.Start(new HttpHostOptions
            {
                Port = port,
                FrontendTestPort = PortBand.FrontendTestPort,
                SessionId = cat.Session.Id,
                SnapshotBuilder = BuildSnapshotJson,
                Dispatcher = (string line) => DispatchInput(CatInputTarget(cat), line),
                EnvelopeBuilder = MakeChatEnvelopeBuilder(cat.Session),
                FrameBuilder = null,
                CatsBuilder = null,
                PatchBuilder = null,
                ViewFullBuilder = () => cat.Session.BuildViewFullJson(),
                ViewFrameBuilder = () => cat.Session.TakeViewFrameJson(),
                ServeChatPage = true,
                RouteRegistrar = RegisterChatPageRoutes,
                HtmlRootProvider = HtmlRoot,
                DisplayName = cat.DisplayName
            });
        }

        /// <summary>
        /// majordomo 独立对话端口启动——F2.2 方案 A（与多猫同构）：独立端口 serve chat.html，绑定 DefaultSession。
        /// 主端口保留管理面板（index.html）；DefaultSession.AttachHost 改绑本端口（chat 事件推送走 chat.html）。
        /// 强制自启：Bootstrap 调用（段6b 前）；无关闭/删除（cat.stop/cat.delete 拒绝——HandleCatStop/HandleCatDelete）。
        /// </summary>
        /// <returns>是否成功启动（端口分配失败 false）</returns>
        internal static bool StartMajorHost()
        {
            if (_majorHost != null)
            {
                return true;
            }
            int port = AllocatePort(PortBand.FamilyFrom);
            if (port < 0)
            {
                LogStore.Add("CatHome4", 2, "majordomo 独立端口启动失败：端口段 " + PortBand.FamilyFrom.ToString() + "-" + PortBand.FamilyTo.ToString() + " 全占用", "CHAT");
                StartupDegradation.Report("MAJOR_PORT_EXHAUSTED", StartupDegradation.KindEnv, "默认猫对话页",
                    StartupDegradation.StateUnavailable,
                    "端口段 " + PortBand.FamilyFrom.ToString() + "-" + PortBand.FamilyTo.ToString() + " 全占用",
                    "关闭占用端口的进程后重启", "S6");
                return false;
            }
            HttpHost host;
            try
            {
                host = HttpHost.Start(new HttpHostOptions
                {
                    Port = port,
                    FrontendTestPort = PortBand.FrontendTestPort,
                    SessionId = _chatBridge.DefaultSession.Id,
                    SnapshotBuilder = BuildSnapshotJson,
                    Dispatcher = (string line) => DispatchInput(MajorInputTarget(), line),
                    EnvelopeBuilder = MakeChatEnvelopeBuilder(_chatBridge.DefaultSession),
                    FrameBuilder = null,
                    CatsBuilder = null,
                    PatchBuilder = null,
                    ViewFullBuilder = () => _chatBridge.DefaultSession.BuildViewFullJson(),
                    ViewFrameBuilder = () => _chatBridge.DefaultSession.TakeViewFrameJson(),
                    ServeChatPage = true,
                    RouteRegistrar = RegisterChatPageRoutes,
                    HtmlRootProvider = HtmlRoot,
                    DisplayName = _chatBridge.DefaultSession.DisplayName
                });
            }
            catch (System.Exception ex)
            {
                // A260：默认猫对话页不可用不得阻断宿主启动（管理面板仍可用）
                LogStore.Add("CatHome4", 2, "majordomo 独立对话端口启动失败：" + ex.Message, "CHAT");
                StartupDegradation.Report("MAJOR_HOST_FAILED", StartupDegradation.KindEnv, "默认猫对话页",
                    StartupDegradation.StateUnavailable, "独立端口 " + port.ToString() + " 启动失败：" + ex.Message,
                    "关闭占用端口的进程后重启", "S6");
                return false;
            }
            _majorHost = host;
            _majorPort = port;
            // 会话事件推送改绑 majordomo 独立对话端口（主端口 index.html 管理面板不再消费 chat 事件——F2.1）
            AttachSessionHost(_chatBridge.DefaultSession, host);
            LogStore.Add("CatHome4", 1, "majordomo 独立对话端口已启动：" + port.ToString(), "CHAT");
            return true;
        }

        /// <summary>
        /// 多猫列表 JSON——GET /api/v1/cats 回调（主端口管理页签数据源；P9.3c 接线）。
        /// F2.2——前置 majordomo 特殊会话条目（special:true + 强制自启 running + 独立对话端口；前端零差异渲染靠 special 标记）。
        /// </summary>
        /// <returns>列表 JSON</returns>
        internal static string BuildCatsJson()
        {
            List<object> list = new List<object>();
            // F2.2 majordomo 特殊会话——置顶 + special 标记（前端不视为多猫；无停止/删除）
            // A225 会话态统一投影——与 cat.info / 快照会话段同源（BuildCatInfoEntry → ChatSession.BuildStateEntry）
            list.Add(BuildCatInfoEntry("majordomo", "majordomo", _majorHost != null, _majorPort, true, _chatBridge != null ? _chatBridge.DefaultSession : null, ""));
            for (int i = 0; i < _cats.Count; i++)
            {
                CatEntry cat = _cats[i];
                list.Add(BuildCatInfoEntry(cat.Id, cat.DisplayName, cat.Running, cat.Port, false, cat.Session, cat.Type));
            }
            var resp = new
            {
                version = 1,
                cats = list
            };
            return JsonUtil.Serialize(resp);
        }

        /// <summary>
        /// 启动扫描——sessions/*/cat.cfg 全部加载进注册表（静默猫跨进程可见）；running 猫拉起 HttpHost（P9.3c Bootstrap 接线）。
        /// </summary>
        internal static void LoadCatsOnBoot()
        {
            string sessionsDir = Path.Combine(_dataRoot, "Data", "sessions");
            if (!Directory.Exists(sessionsDir))
            {
                return;
            }
            string[] dirs;
            try
            {
                dirs = Directory.GetDirectories(sessionsDir);
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "启动扫描目录枚举失败（跳过加载）: " + ex.Message, "CHAT");
                // A260 二期——多猫拉起失败接启动降级登记面
                StartupDegradation.Report("CATS_SCAN_FAILED", StartupDegradation.KindEnv, "多猫（会话目录扫描）",
                    StartupDegradation.StateEmpty, "sessions 目录枚举失败：" + sessionsDir + " | " + ex.Message,
                    "检查 Data/sessions 权限后重启", "S7");
                return;
            }
            // 运行态分离——running/port 取自 Data/runtime/cats.json（cat.cfg 只承载用户配置；启动链零写入 cat.cfg）
            Dictionary<string, CatRuntimeEntry> runtimeMap = LoadCatRuntime();
            bool runtimeDirty = false;
            for (int i = 0; i < dirs.Length; i++)
            {
                // 默认猫 majordomo 目录跳过——Bootstrap 专属处理（M1c 默认猫 cat.cfg 读取面），不进多猫注册表
                if (string.Equals(Path.GetFileName(dirs[i]), "majordomo", StringComparison.Ordinal))
                {
                    continue;
                }
                string cfgPath = Path.Combine(dirs[i], "cat.cfg");
                if (!File.Exists(cfgPath))
                {
                    continue;
                }
                CatCfgData cfg = LoadCatCfg(cfgPath);
                if (cfg == null || cfg.Id == null || cfg.Id.Length == 0)
                {
                    continue;
                }
                CatEntry cat = CreateCatEntry(cfg.Id, cfg.DisplayName, null);
                if (cat == null)
                {
                    LogStore.Add("CatHome4", 2, "启动扫描：猫 " + cfg.Id + " 会话构造失败", "CHAT");
                    StartupDegradation.Report("CAT_BOOT_FAILED", StartupDegradation.KindData, "多猫（" + cfg.Id + "）",
                        StartupDegradation.StateUnavailable, "会话构造失败：sessions/" + cfg.Id + "/cat.cfg",
                        "管理面板 → 猫管理 → 检查该猫配置后重启", "S7");
                    continue;
                }
                _cats.Add(cat);
                // 运行态取值——文件优先；无记录则从旧 cat.cfg 迁移一次（旧版本把 running/port 混在 cat.cfg）
                CatRuntimeEntry rt;
                if (!runtimeMap.TryGetValue(cat.Id, out rt))
                {
                    if (MigrateCatRuntime(cat.Id, cfg.Running, cfg.Port, runtimeMap))
                    {
                        runtimeDirty = true;
                        rt = runtimeMap[cat.Id];
                        LogStore.Add("CatHome4", 1, "启动扫描：猫「" + cat.DisplayName + "」运行态迁移（running=" + (rt.Running ? "true" : "false") + " port=" + rt.Port.ToString() + "）", "CHAT");
                    }
                }
                if (rt == null || !rt.Running)
                {
                    // 静默态——注册表可见（cat.list/start 可寻址），不拉起
                    LogStore.Add("CatHome4", 1, "启动扫描：猫「" + cat.DisplayName + "」为静默态（id " + cat.Id + "）", "CHAT");
                    continue;
                }
                int port = rt.Port;
                if (port < 1024 || IsPortTaken(port))
                {
                    port = AllocatePort(PortBand.FamilyFrom);
                }
                if (port < 0)
                {
                    LogStore.Add("CatHome4", 2, "启动扫描：猫 " + cfg.Id + " 端口分配失败", "CHAT");
                    StartupDegradation.Report("CAT_BOOT_FAILED", StartupDegradation.KindEnv, "多猫（" + cfg.Id + "）",
                        StartupDegradation.StateUnavailable, "端口分配失败（端口段耗尽）：猫 " + cfg.Id,
                        "关闭占用端口的进程后重启，或减少同时运行的猫", "S7");
                    continue;
                }
                try
                {
                    cat.Host = StartCatHost(cat, port);
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 2, "启动扫描：猫 " + cfg.Id + " 拉起失败：" + ex.Message, "CHAT");
                    StartupDegradation.Report("CAT_BOOT_FAILED", StartupDegradation.KindEnv, "多猫（" + cfg.Id + "）",
                        StartupDegradation.StateUnavailable, "拉起失败：猫 " + cfg.Id + " 端口 " + port.ToString() + " | " + ex.Message,
                        "管理面板 → 猫管理 → 手动启动该猫", "S7");
                    continue;
                }
                cat.Running = true;
                cat.Port = port;
                AttachSessionHost(cat.Session, cat.Host);
                // 端口与登记值不一致（被占回落）→ 只更新运行态文件（启动链不写 cat.cfg）
                if (port != rt.Port)
                {
                    rt.Port = port;
                    runtimeDirty = true;
                }
                LogStore.Add("CatHome4", 1, "启动扫描：已拉起猫「" + cat.DisplayName + "」，端口 " + port.ToString(), "CHAT");
            }
            if (runtimeDirty)
            {
                SaveCatRuntime(runtimeMap);
            }
        }

    }
}
