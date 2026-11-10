using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using System.Text.Json;
using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// 宿主会话实体——内置工具分部（R0.2/R1：会话内直执——无需 OA 的内置工具统一入口）。
    /// 分界铁律：内置（无需 OA——会话内/宿主直执）vs OA 工具（按工具组独立 Flow 认领）——双轨并存不走同一执行面。
    /// 内置工具：Note（M4a）+ time/random（R1.1）+ info（R1.2——本会话环境自省，agent 的眼睛）+ sleep（design-ch4-delay §5.3——登记定时唤醒，本轮正常继续）。
    /// </summary>
    internal sealed partial class ChatSession
    {
        /// <summary>
        /// 加载包数据面委托——pack 内置工具（catKey + packKey → 结果 JSON）；宿主启动期由组合根注入。
        /// 池定义与猫级挂载解析归 Admin 域——Core 不依赖配置面。
        /// </summary>
        internal static Func<string, string, string> PackPayloadProvider;

        /// <summary>
        /// 内置工具判定——读 ToolRegistry 注册面（内置/OA 双轨的单一真相源）；未注册 = 非内置。
        /// 分派处原为硬编码名单（Note/time/random/info）——新增内置工具会静默走 OA 工单（无人认领 → 超时）。
        /// </summary>
        /// <param name="name">工具名</param>
        /// <returns>true=内置（会话内直执，不进 OA）</returns>
        private static bool IsBuiltinTool(string name)
        {
            ToolRegistryEntry entry = ToolRegistry.Find(name);
            if (entry == null)
            {
                return false;
            }
            return entry.IsBuiltin;
        }

        /// <summary>
        /// 内置工具统一执行入口——EnterToolBatch 分派（Note/time/random/info/pack/sleep 会话内直执，不进 OA）。
        /// </summary>
        /// <param name="name">内置工具名</param>
        /// <param name="argsJson">参数 JSON</param>
        /// <returns>执行结果文本</returns>
        private string ExecuteBuiltin(string name, string argsJson)
        {
            // timeback 作用域锁定——Note（轮末拉起）/ sleep（等待语义 + 未来注入）/ timer（排程注入）
            // 与「作用域内区间删除」语义冲突，作用域存活期间一律拒绝（莎 2026-09-28 定）
            if (_timebackScope != null && (name == "Note" || name == "sleep" || name == "timer"))
            {
                return "ERR|TIMEBACK_LOCKED|timeback 作用域内 " + name + " 不可用——先 back 回收再调用";
            }
            if (name == "Note")
            {
                return ExecuteNote(argsJson);
            }
            if (name == "time")
            {
                string nowText = DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss");
                // 结构化返回体（design-ch4-tools 附录）——首行 JSON 元数据头 + 正文定界
                Dictionary<string, object> timeFields = new Dictionary<string, object>();
                timeFields["ts"] = nowText;
                return ToolMetaHead.With("time", true, "", -1, timeFields, "时间 | " + nowText);
            }
            if (name == "random")
            {
                return ExecuteRandom(argsJson);
            }
            if (name == "info")
            {
                return ExecuteInfo();
            }
            if (name == "pack")
            {
                return ExecutePack(argsJson);
            }
            if (name == "sleep")
            {
                return ExecuteSleep(argsJson);
            }
            if (name == "timer")
            {
                return ExecuteTimer(argsJson);
            }
            if (name == "timeback-start")
            {
                return ExecuteTimebackStart(argsJson);
            }
            if (name == "timeback-back")
            {
                return ExecuteTimebackBack(argsJson);
            }
            return "ERR|UNKNOWN_BUILTIN|未知内置工具: " + name;
        }

        /// <summary>
        /// random 执行体——生成 [min, max) 范围内随机整数（min 含下限，max 不含上限；min &lt; max 校验）。
        /// </summary>
        /// <param name="argsJson">参数 JSON（min/max 整数）</param>
        /// <returns>随机整数文本</returns>
        private string ExecuteRandom(string argsJson)
        {
            int min = 0;
            int max = 0;
            bool hasMin = false;
            bool hasMax = false;
            if (argsJson != null && argsJson.Length > 0 && argsJson.StartsWith("{"))
            {
                try
                {
                    using (JsonDocument doc = JsonUtil.ParseStrict(argsJson))
                    {
                        JsonElement root = doc.RootElement;
                        JsonElement mn;
                        if (root.TryGetProperty("min", out mn) && mn.ValueKind == JsonValueKind.Number)
                        {
                            min = mn.GetInt32();
                            hasMin = true;
                        }
                        JsonElement mx;
                        if (root.TryGetProperty("max", out mx) && mx.ValueKind == JsonValueKind.Number)
                        {
                            max = mx.GetInt32();
                            hasMax = true;
                        }
                    }
                }
                catch (Exception)
                {
                    return "ERR|BAD_ARGS|random 参数损坏";
                }
            }
            if (!hasMin || !hasMax || min >= max)
            {
                return "ERR|BAD_ARGS|random 需要 min < max 的整数参数";
            }
            Random rng = new Random();
            int picked = rng.Next(min, max);
            // 结构化返回体（design-ch4-tools 附录）——首行 JSON 元数据头 + 正文定界
            Dictionary<string, object> randomFields = new Dictionary<string, object>();
            randomFields["min"] = min;
            randomFields["max"] = max;
            randomFields["value"] = picked;
            return ToolMetaHead.With("random", true, "", -1, randomFields, "随机 | " + min.ToString() + ".." + max.ToString() + " = " + picked.ToString());
        }

        /// <summary>sleep 执行体——同轮帧等待（design-ch4-delay §5.3）：工具单保持未闭合，PumpSleepWait 每帧推进，到点补结果，本轮继续、不结算、不产生新轮；允许对话注入（cat.cfg userInjectIdleOnly=false）时，等待期间到达的人工消息即刻打断等待（Group 回执走 user 通道，管理员睡中可收）。
        /// 参数面：hours/minutes/seconds（非负整数，合计 1..99 秒；超长走 timer；未知参数拒绝——零容忍）。</summary>
        /// <param name="argsJson">参数 JSON（hours/minutes/seconds 可缺省=0）</param>
        /// <returns>结构化结果（元数据头 + 正文；失败 ERR| 前缀）</returns>
        private string ExecuteSleep(string argsJson)
        {
            long hours = 0;
            long minutes = 0;
            long seconds = 0;
            // [段0] 参数面——非负整数 + 未知参数拒绝
            if (argsJson != null && argsJson.Length > 0 && argsJson.StartsWith("{"))
            {
                try
                {
                    using (JsonDocument doc = JsonUtil.ParseStrict(argsJson))
                    {
                        JsonElement root = doc.RootElement;
                        foreach (JsonProperty property in root.EnumerateObject())
                        {
                            if (property.Name == "catId")
                            {
                                continue;
                            }
                            if (property.Name != "hours" && property.Name != "minutes" && property.Name != "seconds")
                            {
                                return "ERR|BAD_ARGS|未知参数: " + property.Name + "（支持 hours / minutes / seconds）";
                            }
                            if (property.Value.ValueKind != JsonValueKind.Number)
                            {
                                return "ERR|BAD_ARGS|" + property.Name + " 需为整数";
                            }
                            long value = property.Value.GetInt64();
                            if (value < 0)
                            {
                                return "ERR|BAD_ARGS|" + property.Name + " 不能为负";
                            }
                            if (property.Name == "hours")
                            {
                                hours = value;
                            }
                            else if (property.Name == "minutes")
                            {
                                minutes = value;
                            }
                            else
                            {
                                seconds = value;
                            }
                        }
                    }
                }
                catch (Exception ex)
                {
                    return "ERR|BAD_ARGS|sleep 参数解析失败: " + ex.Message;
                }
            }
            // [段1] 时长校验——合计 1..99 秒（同轮等待口径：100ms/帧 → 秒数 × 10 = 帧数）
            // 超过 99 秒属跨轮排程语义，走 timer（不置等待、本轮照常收尾）
            long total = hours * 3600 + minutes * 60 + seconds;
            if (total < 1 || total > MaxSleepWaitSeconds)
            {
                return "ERR|BAD_ARGS|sleep 时长需在 1.." + MaxSleepWaitSeconds.ToString() + " 秒之间（当前 " + total.ToString() + " 秒；更长等待请用 timer）";
            }
            if (_sleepDueFrame >= 0)
            {
                return "ERR|BAD_ARGS|本批已有 sleep 在等待——同批只允许一个（剩余 " + _sleepDueFrame.ToString() + " 帧）";
            }
            // [段2] 帧登记——工具单保持未闭合，PumpToolBatch 每帧推进，到点补结果；本轮不结束（同轮续跑）
            // 回执与元数据一律以秒示人（内部计时才用帧——1 帧 = 0.1 秒；帧号只留在日志与排错面）
            long waitFrames = total * FramesPerSecond;
            _sleepDueFrame = waitFrames;
            Dictionary<string, object> sleepFields = new Dictionary<string, object>();
            sleepFields["hours"] = hours;
            sleepFields["minutes"] = minutes;
            sleepFields["seconds"] = seconds;
            sleepFields["waitSeconds"] = total;
            string body = "等待 | " + total.ToString() + " 秒——到点即回本批，本轮继续，不结束轮次。";
            _sleepReply = ToolMetaHead.With("sleep", true, "", -1, sleepFields, body);
            return SleepSuspendMark;
        }

        /// <summary>timer 时长上限（秒）——24 小时（排程语义允许长周期；与 sleep 的 1 小时挂起防呆不同）</summary>
        private const long MaxTimerSeconds = 86400;
        /// <summary>sleep 同轮等待上限（秒）——100ms/帧口径下 99 秒 = 990 帧；超过即属跨轮排程，走 timer</summary>
        private const long MaxSleepWaitSeconds = 99;
        /// <summary>每秒帧数——主循环节拍 100ms/帧（Program.FrameTargetMs）</summary>
        private const long FramesPerSecond = 10;
        /// <summary>sleep 挂起标记——ExecuteSleep 返回它表示「工具单保持未闭合，由帧等待补齐」</summary>
        private const string SleepSuspendMark = "SUSPEND|SLEEP";

        /// <summary>
        /// timer 执行体——登记延迟指令注入（design-ch4-delay §5.4）：本轮正常继续（不挂起、不阻塞），
        /// 到点由调度器注入登记内容。与 sleep 的差别 = 不因主干启动而销毁（闹钟语义）+ 不置工具主动 done（本轮照常自然收尾）。
        /// 参数面：content（必填，非空）+ hours/minutes/seconds（合计 1..86400 秒）+ loop（可缺省=false，触发后按投递时刻 + 时长重排）。
        /// </summary>
        /// <param name="argsJson">参数 JSON（content 必填；hours/minutes/seconds/loop 可缺省）</param>
        /// <returns>结构化结果（元数据头 + 正文；失败 ERR| 前缀）</returns>
        private string ExecuteTimer(string argsJson)
        {
            string content = "";
            long hours = 0;
            long minutes = 0;
            long seconds = 0;
            bool loop = false;
            // [段0] 参数面——content 必填 + 时长非负整数 + loop 布尔 + 未知参数拒绝
            if (argsJson != null && argsJson.Length > 0 && argsJson.StartsWith("{"))
            {
                try
                {
                    using (JsonDocument doc = JsonUtil.ParseStrict(argsJson))
                    {
                        JsonElement root = doc.RootElement;
                        foreach (JsonProperty property in root.EnumerateObject())
                        {
                            if (property.Name == "catId")
                            {
                                continue;
                            }
                            if (property.Name == "content")
                            {
                                if (property.Value.ValueKind != JsonValueKind.String)
                                {
                                    return "ERR|BAD_ARGS|content 需为字符串";
                                }
                                content = property.Value.GetString() ?? "";
                                continue;
                            }
                            if (property.Name == "loop")
                            {
                                if (property.Value.ValueKind != JsonValueKind.True && property.Value.ValueKind != JsonValueKind.False)
                                {
                                    return "ERR|BAD_ARGS|loop 需为布尔";
                                }
                                loop = property.Value.ValueKind == JsonValueKind.True;
                                continue;
                            }
                            if (property.Name != "hours" && property.Name != "minutes" && property.Name != "seconds")
                            {
                                return "ERR|BAD_ARGS|未知参数: " + property.Name + "（支持 content / hours / minutes / seconds / loop）";
                            }
                            if (property.Value.ValueKind != JsonValueKind.Number)
                            {
                                return "ERR|BAD_ARGS|" + property.Name + " 需为整数";
                            }
                            long value = property.Value.GetInt64();
                            if (value < 0)
                            {
                                return "ERR|BAD_ARGS|" + property.Name + " 不能为负";
                            }
                            if (property.Name == "hours")
                            {
                                hours = value;
                            }
                            else if (property.Name == "minutes")
                            {
                                minutes = value;
                            }
                            else
                            {
                                seconds = value;
                            }
                        }
                    }
                }
                catch (Exception ex)
                {
                    return "ERR|BAD_ARGS|timer 参数解析失败: " + ex.Message;
                }
            }
            // [段1] 内容与时长校验——内容必填；合计 1..86400 秒
            if (content.Trim().Length == 0)
            {
                return "ERR|BAD_ARGS|timer 内容为空（content 必填——到点注入的指令文本）";
            }
            long total = hours * 3600 + minutes * 60 + seconds;
            if (total < DelayQueue.MinSleepSeconds || total > MaxTimerSeconds)
            {
                return "ERR|BAD_ARGS|timer 时长需在 " + DelayQueue.MinSleepSeconds.ToString() + ".." + MaxTimerSeconds.ToString() + " 秒之间（当前 " + total.ToString() + " 秒）";
            }
            // [段2] 登记——到点由调度器注入登记内容（本轮正常继续）
            long dueAt = DelayQueue.Now() + total * 1000;
            string added = DelayQueue.Add(_catKey, content, "timer", dueAt, total * 1000, loop);
            if (added.StartsWith("ERR|", StringComparison.Ordinal))
            {
                return added;
            }
            Dictionary<string, object> timerFields = new Dictionary<string, object>();
            timerFields["hours"] = hours;
            timerFields["minutes"] = minutes;
            timerFields["seconds"] = seconds;
            timerFields["loop"] = loop;
            timerFields["dueAt"] = dueAt;
            string loopText = "单次";
            if (loop)
            {
                loopText = "循环（每 " + total.ToString() + " 秒）";
            }
            string body = "排程 | " + DelayQueue.FormatTime(dueAt) + "（" + total.ToString() + " 秒后 · " + loopText + "）——到点自动注入本指令，本轮正常继续。";
            return ToolMetaHead.With("timer", true, "", -1, timerFields, body);
        }

        /// <summary>
        /// pack 执行体——加载包注入：池定义与猫级挂载经 PackPayloadProvider 取，路径解析与前文注入同源。
        /// 语义：任一件失败整包不注入（半个包比没有包更危险——部分成功与完全成功不可区分）。
        /// </summary>
        /// <param name="argsJson">参数 JSON（key 必填）</param>
        /// <returns>注入内容与加载实锤（失败侧 ERR| 前缀 + 逐件原因）</returns>
        private string ExecutePack(string argsJson)
        {
            string packKey = "";
            // [段0] 参数面——key 必填；未知参数拒绝（catId 宿主保留键放行）
            if (argsJson != null && argsJson.Length > 0)
            {
                try
                {
                    using (JsonDocument doc = JsonUtil.ParseStrict(argsJson))
                    {
                        JsonElement root = doc.RootElement;
                        if (root.ValueKind != JsonValueKind.Object)
                        {
                            return "ERR|BAD_ARGS|参数必须是 JSON 对象";
                        }
                        foreach (JsonProperty property in root.EnumerateObject())
                        {
                            if (property.Name == "catId")
                            {
                                continue;
                            }
                            if (property.Name != "key")
                            {
                                return "ERR|BAD_ARGS|未知参数: " + property.Name + "（支持 key）";
                            }
                        }
                        JsonElement keyEl;
                        if (root.TryGetProperty("key", out keyEl) && keyEl.ValueKind == JsonValueKind.String)
                        {
                            string got = keyEl.GetString();
                            if (got != null)
                            {
                                packKey = got;
                            }
                        }
                    }
                }
                catch (Exception ex)
                {
                    return "ERR|BAD_ARGS|参数 JSON 解析失败: " + ex.Message;
                }
            }
            if (packKey.Length == 0)
            {
                return "ERR|BAD_ARGS|缺少参数 key（pack 需要包 key）";
            }
            if (PackPayloadProvider == null)
            {
                return "ERR|PACK_NO_PROVIDER|加载包数据面未注入（宿主未接线）";
            }
            // [段1] 数据面——池定义 + 猫级挂载（Admin 域解析；池外/未挂载在此拦下）
            string payload = PackPayloadProvider(_catKey, packKey);
            bool ok = false;
            string desc = "";
            string error = "";
            string[] paths = new string[0];
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(payload))
                {
                    JsonElement root = doc.RootElement;
                    JsonElement okEl;
                    if (root.TryGetProperty("ok", out okEl) && okEl.ValueKind == JsonValueKind.True)
                    {
                        ok = true;
                    }
                    JsonElement descEl;
                    if (root.TryGetProperty("desc", out descEl) && descEl.ValueKind == JsonValueKind.String)
                    {
                        string gotDesc = descEl.GetString();
                        if (gotDesc != null)
                        {
                            desc = gotDesc;
                        }
                    }
                    JsonElement errEl;
                    if (root.TryGetProperty("error", out errEl) && errEl.ValueKind == JsonValueKind.String)
                    {
                        string gotErr = errEl.GetString();
                        if (gotErr != null)
                        {
                            error = gotErr;
                        }
                    }
                    JsonElement pathsEl;
                    if (root.TryGetProperty("paths", out pathsEl) && pathsEl.ValueKind == JsonValueKind.Array)
                    {
                        List<string> collected = new List<string>();
                        for (int i = 0; i < pathsEl.GetArrayLength(); i = i + 1)
                        {
                            JsonElement item = pathsEl[i];
                            if (item.ValueKind == JsonValueKind.String)
                            {
                                string gotPath = item.GetString();
                                if (gotPath != null && gotPath.Length > 0)
                                {
                                    collected.Add(gotPath);
                                }
                            }
                        }
                        paths = collected.ToArray();
                    }
                }
            }
            catch (Exception ex)
            {
                return "ERR|PACK_BAD_PAYLOAD|包数据面返回解析失败: " + ex.Message;
            }
            if (!ok)
            {
                if (error.Length == 0)
                {
                    error = "ERR|PACK_RESOLVE|包解析失败: " + packKey;
                }
                return error;
            }
            // [段2] 路径展开——目录项一级 *.md 升序；文件项单条（与前文注入同语义）；未知根/缺失即记失败
            WorkspaceConfig ws = null;
            DataBox.TryResolve<WorkspaceConfig>(out ws);
            if (ws == null)
            {
                return "ERR|PACK_NO_WORKSPACE|工作区配置不可用（受控根解析面缺失）";
            }
            List<string> labels = new List<string>();
            List<string> targets = new List<string>();
            List<string> failures = new List<string>();
            for (int i = 0; i < paths.Length; i = i + 1)
            {
                string entryPath = paths[i];
                string resolved = "";
                try
                {
                    WorkspaceConfig.InjectEntry entry = new WorkspaceConfig.InjectEntry();
                    entry.File = entryPath;
                    entry.Optional = false;
                    entry.Label = entryPath;
                    resolved = ws.ResolveInjectFile(entry);
                }
                catch (Exception ex)
                {
                    failures.Add(entryPath + " | 根解析失败: " + ex.Message);
                    continue;
                }
                if (Directory.Exists(resolved))
                {
                    string[] found = Directory.GetFiles(resolved, "*.md", SearchOption.TopDirectoryOnly);
                    Array.Sort(found, StringComparer.OrdinalIgnoreCase);
                    string prefix = entryPath.TrimEnd('/', '\\');
                    for (int k = 0; k < found.Length; k = k + 1)
                    {
                        labels.Add(prefix + "/" + Path.GetFileName(found[k]));
                        targets.Add(found[k]);
                    }
                    if (found.Length == 0)
                    {
                        failures.Add(entryPath + " | 目录内无 .md 文件");
                    }
                }
                else
                {
                    if (!File.Exists(resolved))
                    {
                        failures.Add(entryPath + " | 文件不存在");
                        continue;
                    }
                    labels.Add(entryPath);
                    targets.Add(resolved);
                }
            }
            // [段3] 逐件读取——任一件失败整包不注入（原子拒绝）
            List<string> contents = new List<string>();
            int totalChars = 0;
            for (int i = 0; i < targets.Count; i = i + 1)
            {
                try
                {
                    string text = File.ReadAllText(targets[i]);
                    contents.Add(text);
                    totalChars = totalChars + text.Length;
                }
                catch (Exception ex)
                {
                    failures.Add(labels[i] + " | 读取失败: " + ex.Message);
                }
            }
            if (failures.Count > 0)
            {
                StringBuilder failMsg = new StringBuilder();
                failMsg.Append("ERR|PACK_LOAD_FAILED|");
                failMsg.Append(packKey);
                failMsg.Append(" 加载失败（整包未注入）:");
                for (int i = 0; i < failures.Count; i = i + 1)
                {
                    failMsg.Append("\n  - ");
                    failMsg.Append(failures[i]);
                }
                return failMsg.ToString();
            }
            // [段4] 组装——头部加载实锤（件数/字符数）+ 逐件标注（与前文注入同形）
            StringBuilder sb = new StringBuilder();
            sb.Append("✅ PACK ");
            sb.Append(packKey);
            if (desc.Length > 0)
            {
                sb.Append(" | ");
                sb.Append(desc);
            }
            sb.Append(" | ");
            sb.Append(targets.Count.ToString());
            sb.Append(" 件 / ");
            sb.Append(totalChars.ToString());
            sb.Append(" 字符\n");
            for (int i = 0; i < targets.Count; i = i + 1)
            {
                sb.Append("===== 注入文件: ");
                sb.Append(labels[i]);
                sb.Append(" =====");
                sb.Append("\n");
                sb.Append(contents[i]);
                sb.Append("\n");
            }
            LogStore.Add("CatHome4", 1, "pack 注入: cat=" + _catKey + " | key=" + packKey + " | " + targets.Count.ToString() + " 件 / " + totalChars.ToString() + " 字符", "INJECT");
            // 结构化返回体（design-ch4-tools 附录）——首行 JSON 元数据头 + 正文定界
            Dictionary<string, object> packFields = new Dictionary<string, object>();
            packFields["chars"] = totalChars;
            return ToolMetaHead.With("pack", true, packKey, targets.Count, packFields, packKey + " | " + targets.Count.ToString() + " 件 · " + totalChars.ToString() + " 字符" + "\n" + sb.ToString());
        }

        /// <summary>info 执行体——本会话环境自省，返回分类 JSON 块（version / time / llm / endpoint / roots / tokens / packs；M4e：info 是猫自省目录范围的通道）。</summary>
        /// <returns>环境信息文本</returns>
        private string ExecuteInfo()
        {
            if (_envInfoProvider != null)
            {
                // M4e 猫级白名单——info 按当前会话猫输出（provider 读 ToolCatContext；内置工具直执不经 RunTool）
                string prev = ToolCatContext.CurrentCatKey;
                ToolCatContext.SetCat(_catKey);
                try
                {
                    // 返回体 = 分类 JSON 块（design-ch4-tools 附录 §info——provider 直接产出整块 JSON，不再包「头 + 正文」）
                    return _envInfoProvider();
                }
                finally
                {
                    ToolCatContext.SetCat(prev);
                }
            }
            return "ERR|INFO_NO_PROVIDER|环境信息不可用（未注入 provider）";
        }

        /// <summary>
        /// 环境信息文本公开面——QQ /info 指令直调工具函数（ExecuteInfo 同源；不触会话状态——跨线程安全）。
        /// </summary>
        /// <returns>环境信息文本</returns>
        public string GetInfoText()
        {
            return ExecuteInfo();
        }
    }
}