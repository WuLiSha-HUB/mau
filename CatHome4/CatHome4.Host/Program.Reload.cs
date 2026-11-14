using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using System.Text.Json;
using System.Threading;
using Mau.Runtime;
using Mau.Providers;

namespace CH4
{
    /// <summary>
    /// Program 热重载面分部——reload 事务（忙时拒绝/加载/换注册/试跑回滚/成功换柄）。
    /// P7b partial 拆分——自 Program.cs 原样搬移，逻辑零改动。
    /// </summary>
    public static partial class Program
    {
        /// <summary>
        /// reload 热重载——Flow 注册名（QuickCat/TextCat/...；host-flows 查当前清单）+ 可选 dll 路径（缺省 = 当前 handle 同路径重读）
        /// 部署面：产物区（仓库根 public/app/Flows/FL_&lt;名&gt;.dll）有更新版时执行「备份运行区 → 卸载旧（释放 ALC 文件锁）→ 覆盖运行区」；
        /// 随后统一走加载事务：Load 新 + Tick 试跑验证（失败 → 还原备份 + 重载旧版）→ UnregisterFlow 旧 → RegisterFlow 新 → 旧 TryUnload → 预热 → 报告
        /// P8.5b：返回结果文本（host-reload 工具 LLM 可见；Console 同步输出行为不变）
        /// </summary>
        /// <param name="args">cat + 空格 + dll 路径（dll 可选）</param>
        /// <returns>reload 结果文本</returns>
        private static string ExecuteReload(string args)
        {
            System.Text.StringBuilder sb = new System.Text.StringBuilder();
            string[] parts = args.Split(' ');
            string cat = parts[0].Trim();
            string dllPath;
            if (parts.Length > 1)
            {
                dllPath = parts[1].Trim();
            }
            else
            {
                dllPath = "";
            }
            // [段0] 忙时拒绝——任一会话工具批次执行中 reload 会导致旧批次完成信号永不置位（WaitForTools 空转帧上限）——host-* 直执路径豁免（ChatSession 工具批临时解除）
            if (_chatBridge.IsAnyToolBatchActive())
            {
                string busyMsg = "reload 拒绝: 工具批次执行中（Chat 处理中）——等待完成后再试";
                Console.WriteLine("[CMD] " + busyMsg);
                sb.AppendLine(busyMsg);
                return sb.ToString();
            }
            FlowHandle oldHandle;
            long oldId;
            string name;
            {
                // 工具组按 Flow 名寻址——Registry 动态许可已由 ExecHostReload 校验；此处查宿主句柄表
                name = cat;
                if (!_toolFlowHandles.TryGetValue(name, out oldHandle))
                {
                    string badMsg = "reload 目标无效——宿主句柄表中无该 Flow: " + name + "（host-flows 可查现状）";
                    Console.WriteLine("[CMD] " + badMsg);
                    sb.AppendLine(badMsg);
                    return sb.ToString();
                }
                if (!_toolFlowIds.TryGetValue(name, out oldId))
                {
                    oldId = -1;
                }
            }
            if (dllPath.Length == 0)
            {
                dllPath = oldHandle.SourceDll;
            }
            // [段0b] 部署全流程——产物区（仓库根 public/app/Flows）有更新版时：备份运行区 → 卸载旧（释放 ALC 文件锁）→ 覆盖运行区 → 段1 重载
            string srcDll = ResolveFlowSourceDll(name);
            string backupPath = "";
            bool oldReleased = false;
            if (srcDll.Length > 0 && !string.Equals(srcDll, dllPath, StringComparison.OrdinalIgnoreCase))
            {
                backupPath = dllPath + ".bak";
                try
                {
                    File.Copy(dllPath, backupPath, true);
                }
                catch (Exception ex)
                {
                    string backupFail = "reload " + name + " 失败: 运行区备份失败——" + ex.Message;
                    Console.WriteLine("[CMD] " + backupFail);
                    sb.AppendLine(backupFail);
                    return sb.ToString();
                }
                _runner.UnregisterFlow(oldId);
                oldReleased = true;
                oldHandle.TryUnload(3);
                try
                {
                    File.Copy(srcDll, dllPath, true);
                    sb.AppendLine("部署: " + srcDll + " → " + dllPath);
                }
                catch (Exception ex)
                {
                    RestoreBackup(dllPath, backupPath);
                    sb.AppendLine(ReloadRecover(name, cat, dllPath, "产物覆盖失败——" + ex.Message, sb));
                    return sb.ToString();
                }
            }
            // [段1] 加载新版本（Load 异常 = dll 损坏——失败保留旧；有备份则先还原文件再重载旧版）
            FlowHandle newHandle;
            try
            {
                newHandle = FlowHandle.Load(dllPath);
            }
            catch (Exception ex)
            {
                if (backupPath.Length > 0)
                {
                    RestoreBackup(dllPath, backupPath);
                    sb.AppendLine(ReloadRecover(name, cat, dllPath, "新版本加载未通过——" + ex.Message, sb));
                    return sb.ToString();
                }
                string failMsg = "reload " + name + " 失败: 新版本加载未通过——" + ex.Message;
                Console.WriteLine("[CMD] " + failMsg);
                sb.AppendLine(failMsg);
                return sb.ToString();
            }
            // [段2] 换注册——卸旧（D1 修复：CommandBus key 同步清理 + DataBox FlowId scope 清理）→ 注册新 → 试跑帧（runner 帧序注入 FlowContext=newId——CmdPump 真实注册，无幽灵 owner）
            if (!oldReleased)
            {
                _runner.UnregisterFlow(oldId);
            }
            long newId = _runner.RegisterFlow(newHandle.Flow, name);
            try
            {
                _runner.Tick();
            }
            catch (Exception ex)
            {
                // [段2b] 试跑异常回滚——有备份先还原文件再重载旧版；无备份重载旧 dll 全新实例（CmdPump 全新注册；旧 handle 弃用）
                _runner.UnregisterFlow(newId);
                newHandle.TryUnload(3);
                if (backupPath.Length > 0)
                {
                    RestoreBackup(dllPath, backupPath);
                    sb.AppendLine(ReloadRecover(name, cat, dllPath, "试跑帧异常——" + ex.Message, sb));
                    return sb.ToString();
                }
                FlowHandle rollback;
                long rollbackId;
                try
                {
                    rollback = FlowHandle.Load(oldHandle.SourceDll);
                    rollbackId = _runner.RegisterFlow(rollback.Flow, name);
                    for (int i = 0; i < WarmupFrames; i++)
                    {
                        _runner.Tick();
                    }
                }
                catch (Exception ex2)
                {
                    string rollbackFailMsg = "reload " + name + " 失败: 试跑异常且回滚失败——" + ex.Message + " / " + ex2.Message;
                    Console.WriteLine("[CMD] " + rollbackFailMsg);
                    sb.AppendLine(rollbackFailMsg);
                    return sb.ToString();
                }
                oldHandle.TryUnload(3);
                SetCatHandle(cat, rollback, rollbackId);
                string rollbackMsg = "reload " + name + " 失败: 试跑帧异常已回滚（旧版本全新实例 #" + rollbackId + "）——" + ex.Message;
                Console.WriteLine("[CMD] " + rollbackMsg);
                sb.AppendLine(rollbackMsg);
                return sb.ToString();
            }
            // [段3] 成功路径——换句柄 + 卸载旧 ALC + 预热帧
            oldHandle.TryUnload(3);
            SetCatHandle(cat, newHandle, newId);
            // 工具定义热同步——新版本 GetToolsJson 可能已变（改 tools.<组> 积木描述 → reload 生效；design-ch4-tools-pool §七）
            {
                List<ToolGroupSource> poolGroups = new List<ToolGroupSource>();
                foreach (KeyValuePair<string, FlowHandle> kv in _toolFlowHandles)
                {
                    // 池化组兄弟实例（base~N）——工具定义归基础名，兄弟跳过（避免重复/缺陷误报）
                    if (kv.Key.IndexOf('~') >= 0)
                    {
                        continue;
                    }
                    ToolGroupSource groupSource = new ToolGroupSource();
                    groupSource.Name = kv.Key;
                    groupSource.Flow = kv.Value.Flow;
                    poolGroups.Add(groupSource);
                }
                ToolPool.RebuildAll(poolGroups, BuildBuiltinToolsJson());
                // A107——定义缺陷出声：日志落 L3（改积木改坏 JSON 当场可见）
                LogToolPoolDefects();
                ToolRegistry.Init(ToolPool.BuildSpecs(), ToolPool.BuildOwnerFlowMap(), ToolPool.BuildPrivilegedMap());
            }
            for (int i = 0; i < WarmupFrames; i++)
            {
                _runner.Tick();
            }
            string[] keyDic = _bus.GetKeyDic();
            string okMsg = "reload " + name + ": #" + oldId + " → #" + newId + " | pid=" + Environment.ProcessId + " | " + keyDic[0];
            Console.WriteLine("[CMD] " + okMsg);
            sb.AppendLine(okMsg);
            for (int k = 0; k < keyDic.Length; k++)
            {
                Console.WriteLine("[CMD]     " + keyDic[k]);
                sb.AppendLine("    " + keyDic[k]);
            }
            if (backupPath.Length > 0)
            {
                sb.AppendLine("旧版备份: " + backupPath);
            }
            // A107——定义缺陷随 reload 报告回执（改坏定义 JSON 当场看见，不等下次 info）
            ToolDefect[] reloadDefects = ToolPool.Defects();
            for (int d = 0; d < reloadDefects.Length; d = d + 1)
            {
                sb.AppendLine("定义缺陷: " + reloadDefects[d].Group + " | " + reloadDefects[d].Stage + " | " + reloadDefects[d].Reason);
            }
            // [段4] 池化组——连带重载兄弟实例（基础名 base 的 ~2..~K；同 dll 文件、各自 ALC）
            string[] poolSiblings = PoolSiblingNames(name);
            for (int s = 0; s < poolSiblings.Length; s = s + 1)
            {
                ExecuteReload(poolSiblings[s]);
                sb.AppendLine("池化兄弟实例已重载: " + poolSiblings[s]);
            }
            // 结构化返回体（design-ch4-tools 附录）——首行 JSON 元数据头 + 正文定界
            System.Collections.Generic.Dictionary<string, object> reloadFields = new System.Collections.Generic.Dictionary<string, object>();
            reloadFields["oldId"] = oldId;
            reloadFields["newId"] = newId;
            reloadFields["pid"] = Environment.ProcessId;
            return ToolMetaHead.With("host-reload", true, name, -1, reloadFields, name + " | 已重载" + "\n" + sb.ToString());
        }

        /// <summary>
        /// 部署源解析——产物区 FL 更新版路径（仓库根 public/app/Flows/FL_&lt;名&gt;.dll；不存在返回空串 = 不做部署，按原路径重载）
        /// </summary>
        /// <param name="name">Flow 注册名（= 组名 = dll 名去 FL_ 前缀）</param>
        /// <returns>源 dll 绝对路径或空串</returns>
        private static string ResolveFlowSourceDll(string name)
        {
            // 池化组兄弟实例（base~N）——产物源归基础名（FL_base.dll）
            string baseName = name;
            int tilde = name.IndexOf('~');
            if (tilde >= 0)
            {
                baseName = name.Substring(0, tilde);
            }
            string root = ResolveRepoRootForDeploy();
            if (root.Length == 0)
            {
                return "";
            }
            string path = System.IO.Path.Combine(root, "public", "app", "Flows", "FL_" + baseName + ".dll");
            if (!System.IO.File.Exists(path))
            {
                return "";
            }
            return System.IO.Path.GetFullPath(path);
        }

        /// <summary>
        /// 仓库根解析（部署面）——MAU_ROOT 环境变量 → workspace 根（含 Mau.sln 者）→ 程序集位置向上探测
        /// </summary>
        /// <returns>仓库根或空串</returns>
        private static string ResolveRepoRootForDeploy()
        {
            string env = Environment.GetEnvironmentVariable("MAU_ROOT");
            if (env != null && env.Length > 0 && System.IO.File.Exists(System.IO.Path.Combine(env, "Mau.sln")))
            {
                return env;
            }
            WorkspaceConfig ws;
            if (DataBox.TryResolve<WorkspaceConfig>(out ws) && ws != null)
            {
                for (int i = 0; i < ws.Roots.Length; i = i + 1)
                {
                    string path = ws.Roots[i].Path;
                    if (path != null && path.Length > 0 && System.IO.File.Exists(System.IO.Path.Combine(path, "Mau.sln")))
                    {
                        return path;
                    }
                }
            }
            return FindRepoRoot(AppContext.BaseDirectory);
        }

        /// <summary>
        /// 还原运行区备份——覆盖失败/加载失败的兜底（best-effort，不抛）
        /// </summary>
        /// <param name="dllPath">运行区 dll 路径</param>
        /// <param name="backupPath">备份路径</param>
        private static void RestoreBackup(string dllPath, string backupPath)
        {
            try
            {
                if (System.IO.File.Exists(backupPath))
                {
                    System.IO.File.Copy(backupPath, dllPath, true);
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "运行区备份还原失败（best-effort）: " + ex.Message, "RELOAD");
            }
        }

        /// <summary>
        /// reload 失败恢复——从磁盘（已还原的备份）重载旧版并重新注册；恢复失败则报告严重错误
        /// </summary>
        /// <param name="name">Flow 注册名</param>
        /// <param name="cat">句柄表键（QuickCat 独立字段）</param>
        /// <param name="dllPath">运行区 dll 路径</param>
        /// <param name="why">失败原因</param>
        /// <param name="sb">输出累积</param>
        /// <returns>结果文本</returns>
        private static string ReloadRecover(string name, string cat, string dllPath, string why, System.Text.StringBuilder sb)
        {
            FlowHandle rollback;
            long rollbackId;
            try
            {
                rollback = FlowHandle.Load(dllPath);
                rollbackId = _runner.RegisterFlow(rollback.Flow, name);
                for (int i = 0; i < WarmupFrames; i++)
                {
                    _runner.Tick();
                }
            }
            catch (Exception ex)
            {
                string fatal = "reload " + name + " 失败: " + why + "；旧版本重载亦失败——" + ex.Message;
                Console.WriteLine("[CMD] " + fatal);
                sb.AppendLine(fatal);
                return fatal;
            }
            SetCatHandle(cat, rollback, rollbackId);
            string msg = "reload " + name + " 失败: " + why + "——已回滚旧版本（全新实例 #" + rollbackId + "）";
            Console.WriteLine("[CMD] " + msg);
            sb.AppendLine(msg);
            return msg;
        }

        /// <summary>
        /// host-reload 工具执行——解析 cat 参数（Flow 注册名）→ 动态校验（Registry.Entries）→ ExecuteReload（复用事务三段式）；宿主级工具——ExecuteToolBatch 白名单直执不走 OA
        /// </summary>
        /// <param name="argsJson">参数 JSON</param>
        /// <returns>reload 结果文本</returns>
        private static string ExecHostReload(string argsJson)
        {
            // [参数面] 声明面口径零容忍——未知参数一律 ERR|BAD_ARGS（catId 保留键放行）
            string badArgs = CheckHostArgs(argsJson, "cat");
            if (badArgs.Length > 0)
            {
                return badArgs;
            }
            string cat = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(argsJson))
                {
                    JsonElement root = doc.RootElement;
                    JsonElement c;
                    if (root.TryGetProperty("cat", out c) && c.ValueKind == JsonValueKind.String)
                    {
                        string got = c.GetString();
                        if (got != null)
                        {
                            cat = got;
                        }
                    }
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "host-reload cat 参数解析失败: " + ex.Message, "RELOAD");
                cat = "";
            }
            if (cat.Length == 0)
            {
                return "ERR|BAD_ARGS|host-reload cat 参数缺失——须为已加载 Flow 注册名（host-flows 可查当前清单）";
            }
            // 动态许可——Registry.Entries 为真相源（新增工具组 Flow 零宿主改动；dev 等死目标天然拒绝）
            bool found = false;
            FlowEntry[] entries = _runner.Registry.Entries;
            for (int i = 0; i < entries.Length; i = i + 1)
            {
                if (string.Equals(entries[i].Name, cat, StringComparison.Ordinal))
                {
                    found = true;
                    break;
                }
            }
            if (!found)
            {
                StringBuilder avail = new StringBuilder();
                for (int i = 0; i < entries.Length; i = i + 1)
                {
                    if (i > 0)
                    {
                        avail.Append(" / ");
                    }
                    avail.Append(entries[i].Name);
                }
                return "ERR|BAD_ARGS|host-reload cat 须为已加载 Flow 注册名（当前: " + avail.ToString() + "）——host-flows 可查";
            }
            return ExecuteReload(cat);
        }

        /// <summary>
        /// host-flows 工具执行——查看当前运行 Flow 现状（Registry 动态面：Id/Name/Kind + 宿主句柄状态 + dll）；宿主级直执
        /// </summary>
        /// <param name="argsJson">参数 JSON（无参）</param>
        /// <returns>Flow 现状文本</returns>
        private static string ExecHostFlows(string argsJson)
        {
            // [参数面] 声明面口径零容忍——本工具无参数（catId 保留键放行）
            string badArgs = CheckHostArgs(argsJson, "");
            if (badArgs.Length > 0)
            {
                return badArgs;
            }
            System.Text.StringBuilder sb = new System.Text.StringBuilder();
            FlowEntry[] entries = _runner.Registry.Entries;
            sb.Append("Flow 运行现状（" + entries.Length.ToString() + " 个）:");
            for (int i = 0; i < entries.Length; i = i + 1)
            {
                FlowEntry entry = entries[i];
                string state;
                string dll = "";
                FlowHandle curHandle = null;
                {
                    FlowHandle handle;
                    if (_toolFlowHandles.TryGetValue(entry.Name, out handle))
                    {
                        state = "alive";
                        dll = handle.SourceDll;
                        curHandle = handle;
                    }
                    else
                    {
                        state = "no-handle";
                    }
                }
                string buildTime = "";
                if (curHandle != null)
                {
                    try
                    {
                        buildTime = VersionInfo.GetAssemblyBuildTime(curHandle.Flow.GetType().Assembly);
                    }
                    catch (Exception ex)
                    {
                        LogStore.Add("CatHome4", 2, "Flow 构建时刻读取失败（回落空）: " + ex.Message, "RELOAD");
                        buildTime = "";
                    }
                }
                string dllInfo = "";
                if (dll.Length > 0)
                {
                    dllInfo = " | " + dll;
                }
                string timeInfo = "";
                if (buildTime.Length > 0)
                {
                    timeInfo = " | 编译: " + buildTime;
                }
                sb.Append(System.Environment.NewLine);
                sb.Append("  #" + entry.Id.ToString() + " " + entry.Name + " kind=" + (entry.Kind != null ? entry.Kind : "") + " " + state + dllInfo + timeInfo);
            }
            // 结构化返回体（design-ch4-tools 附录）——首行 JSON 元数据头 + 正文定界
            return ToolMetaHead.With("host-flows", true, "", entries.Length, null, entries.Length.ToString() + " 个 Flow" + "\n" + sb.ToString());
        }

        /// <summary>
        /// 更新指定 Flow 的句柄 + 注册 ID 字段——工具组按 Flow 名写字典（R0.2）
        /// </summary>
        /// <param name="cat">Flow 注册名（TextCat/MauCat/CsCat/ConfigCat/...）</param>
        /// <param name="handle">新句柄</param>
        /// <param name="id">新注册 ID</param>
        private static void SetCatHandle(string cat, FlowHandle handle, long id)
        {
            _toolFlowHandles[cat] = handle;
            _toolFlowIds[cat] = id;
        }
    }
}