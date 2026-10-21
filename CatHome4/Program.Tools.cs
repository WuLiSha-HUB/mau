using System;
using System.Collections.Generic;
using System.Text;
using System.Text.Json;
using System.Threading;
using Mau.Runtime;
using Mau.Development;

namespace CH4
{
    /// <summary>
    /// Program 的工具执行面分部——工具声明表 + 名单管理 + 内置工具定义源（P8 工具组）。
    /// 归属：agent 循环基建（宿主侧）。工具执行双轨——OA 认领线（工具组 Flow）承载 OA 工具；内置工具（Note/time/random/info/host-*/pack/sleep/timer/timeback-*）宿主直执。
    /// </summary>
    public static partial class Program
    {
        /// <summary>
        /// 内置工具定义源——宿主内建小表（本质也是 BRIK，只是内置：Note/time/random/info/host-*/pack/sleep/timer/timeback-start/timeback-back）。
        /// 格式与 Flow.GetToolsJson 同构（{"group":"","tools":[...]}）——同一解析器，统一工具池。
        /// </summary>
        /// <returns>内置工具定义 JSON</returns>
        private static string BuildBuiltinToolsJson()
        {
            // 结构化构建——匿名对象树 → JsonSerializer（R6-P3-08：原手写字符串拼接易漏转义、难维护）
            object[] tools = new object[11];
            tools[0] = new
            {
                name = "Note",
                description = "轻量任务追踪器（内存存储，会话关闭即消失）。只记可自主执行的步骤——等确认 / 等回应的闸门项不写入（引擎对未完成项强制拉起）。无参数=推进到下一条；action='set'+content=[\"任务1\",\"任务2\"]=写入新计划（已有未完成需force=true强制覆盖）。返回当前第X/Y条 已完成Z 待完成W 任务目标：... 最后一条时追加提示（已是最后一条需求，完成后可结束本轮）。全部完成后自动清空。剩余1条时引擎不自动拉起。",
                parameters = new
                {
                    type = "object",
                    properties = new Dictionary<string, object>
                            {
                                { "action", new { type = "string", description = "set=写入新计划，不传=推进" } },
                                { "content", new { type = "array", items = new { type = "string" }, description = "action=set时必填——任务条目数组（每元素一条）" } },
                                { "force", new { type = "boolean", description = "覆盖已有未完成计划时传true" } }
                            },
                    required = new string[0]
                }
            };
            tools[1] = new
            {
                name = "time",
                description = "当前系统日期时间（yyyy-MM-dd HH:mm:ss）——会话内直执，无需 OA",
                parameters = new
                {
                    type = "object",
                    properties = new Dictionary<string, object>()
                }
            };
            tools[2] = new
            {
                name = "random",
                description = "生成 [min, max) 范围内的随机整数（min 含下限，max 不含上限，要求 min < max）——会话内直执，无需 OA",
                parameters = new
                {
                    type = "object",
                    properties = new Dictionary<string, object>
                            {
                                { "min", new { type = "integer", description = "随机范围下限（含）" } },
                                { "max", new { type = "integer", description = "随机范围上限（不含）" } }
                            },
                    required = new string[] { "min", "max" }
                }
            };
            tools[3] = new
            {
                name = "info",
                description = "本会话环境自省——返回分类 JSON 块：version（版本 + 编译时刻）/ time（当前时间）/ llm（本猫生效端点）/ endpoint（本地端点：对话页 + 管理面板）/ roots（可见受控根 id/writable/note/path——绝对路径）/ tokens（前文长度 + 最近前文变动时刻）/ packs（挂载包）/ qqbot（渠道说明）/ tools_drift（漂移——added 附用法摘要 name/desc/args（*=必填）· removed 给名字；仅不一致时输出）/ tools_defect（工具组定义缺陷——group/stage/reason；仅非空时输出）/ timeback（上下文作用域——active 活跃作用域 · recent 归档尾部 5 条 · archive 写面可用性；无数据时不出该键）（agent 的眼睛；R1.2）",
                parameters = new
                {
                    type = "object",
                    properties = new Dictionary<string, object>()
                }
            };
            tools[4] = new
            {
                name = "host-reload",
                description = "热重载语料 dll（宿主级）——在 mau-proj 编译成功后单独调用（建议下一轮）；事务三段式：加载失败保留旧版本；cat=已加载 Flow 注册名（host-flows 查当前清单）",
                parameters = new
                {
                    type = "object",
                    properties = new Dictionary<string, object>
                            {
                                { "cat", new { type = "string", description = "Flow 注册名（host-flows 查当前全部）" } }
                            },
                    required = new string[] { "cat" }
                }
            };
            tools[5] = new
            {
                name = "host-flows",
                description = "查看当前运行 Flow 现状（宿主级）——Registry 动态面：每个已加载 Flow 的 Id/Name/Kind/句柄状态/dll 路径；新增/重载后查询目标清单用",
                parameters = new
                {
                    type = "object",
                    properties = new Dictionary<string, object>()
                }
            };
            tools[6] = new
            {
                name = "pack",
                description = "加载包注入——按 key 注入预设文件包（池 packs.json 定义 + 本猫挂载授权），一次调用完成加载；返回逐件明细（件数/字符数/失败原因）。收工/蒸馏/审查等跨身份动作的前置加载入口。",
                parameters = new
                {
                    type = "object",
                    properties = new Dictionary<string, object>
                            {
                                { "key", new { type = "string", description = "包 key（info 可见本猫已挂载包）" } }
                            },
                    required = new string[] { "key" }
                }
            };
            tools[7] = new
            {
                name = "sleep",
                description = "登记定时唤醒（时/分/秒 · 合计 1..3600 秒）——等待语义：本轮正常继续（不挂起），到点由宿主自动注入一条唤醒消息；主干被任何其他输入提前启动时本等待即作废（自动销毁并告知）。适合「等你回话 / 等外部条件」类需求。",
                parameters = new
                {
                    type = "object",
                    properties = new Dictionary<string, object>
                            {
                                { "hours", new { type = "integer", description = "小时（可缺省=0）" } },
                                { "minutes", new { type = "integer", description = "分钟（可缺省=0）" } },
                                { "seconds", new { type = "integer", description = "秒（可缺省=0）" } }
                            },
                    required = new string[0]
                }
            };
            tools[8] = new
            {
                name = "timer",
                description = "登记延迟指令注入（content + 时/分/秒 · 合计 1..86400 秒）——排程语义：到点把 content 原样注入本会话（闹钟必响，不因主干启动而销毁）；loop=true 时触发后按「投递时刻 + 时长」重排（周期巡检）。本轮正常继续，且不影响本轮收尾语义。适合「长任务中途自查 / 周期巡检」。",
                parameters = new
                {
                    type = "object",
                    properties = new Dictionary<string, object>
                            {
                                { "content", new { type = "string", description = "到点注入的指令文本（必填）" } },
                                { "hours", new { type = "integer", description = "小时（可缺省=0）" } },
                                { "minutes", new { type = "integer", description = "分钟（可缺省=0）" } },
                                { "seconds", new { type = "integer", description = "秒（可缺省=0）" } },
                                { "loop", new { type = "boolean", description = "true=周期重排（可缺省=false）" } }
                            },
                    required = new string[] { "content" }
                }
            };
            tools[9] = new
            {
                name = "timeback-start",
                description = "上下文作用域·开锚——type 选域类型（决定域内可用工具白名单 / 回执骨架 / 资源预热），purpose 记用途；过程在作用域内膨胀，回收时用 timeback-back 回卷（膨胀过程从上下文销毁，只把 findings 带回主干——下一轮首条可见）。域类型枚举：browser_vision（浏览网页 + 识图：browser-* / image-* + 基础读面）· text_search（文本与文档检索：文本读面 + 文件结构面）· code_review（代码审查取证：基础读面 + cs 只读七件，不含跑测面）· code_write（代码实现：基础读面 + cs 全套十三件 + 文本写面 + mau-verify——读 / 改 / 编译 / 跑测一域内完成）。🔴 域内只放行本类型白名单——名单外调用被拒（ERR|TIMEBACK_PROFILE，需用则先 back 回主干，不在域内换类型）；系统信息类工具（info / host-flows）不进任何域；域内不做计划、不调 Note / sleep / timer。热重载 / 重启 / 部署不在域内（进程中断即失域）。🔴 开域判据：本环节预期串接 ≥3 次工具调用才收口——单次 / 两次即得答案留主干。v1 未闭合前禁止再次 start。",
                parameters = new
                {
                    type = "object",
                    properties = new Dictionary<string, object>
                            {
                                { "type", new { type = "string", description = "域类型（必填）——browser_vision | text_search | code_review | code_write" } },
                                { "purpose", new { type = "string", description = "用途标签（短）" } }
                            },
                    required = new string[] { "type", "purpose" }
                }
            };
            tools[10] = new
            {
                name = "timeback-back",
                description = "上下文作用域·回卷回收——findings 即本次调用的返回值，回卷后作用域关闭（区间内容从上下文销毁）。findings 按 start 回执给出的骨架写（审查类给「成果位置」· 实现类给「变更 + 可复跑跑测」）——只写「在哪 / 改了什么」不写「结论是什么」；没读到的、凭印象复述的一律不写。回执另附宿主**工具台账**（域内用过的每个工具：工具名 · 目标 · 成败——含只读与被拒）。回收时本调用的思考内容会被替换为占位句（原文留视图层）——防「回域断言」被后续轮次误当事实。",
                parameters = new
                {
                    type = "object",
                    properties = new Dictionary<string, object>
                            {
                                { "findings", new { type = "string", description = "带回载荷（骨架：成果 / 未竟 / 卡点 / 失败——只写位置与简短描述，不写结论）" } }
                            },
                    required = new string[] { "findings" }
                }
            };
            // 统一序列化入口——中文与 < > 直显（JsonUtil=UnsafeRelaxedJsonEscaping 单一真相源）
            return JsonUtil.Serialize(new { group = "", tools = tools });
        }

        /// <summary>
        /// 宿主内置工具参数面校验——薄封装：仅承接「空参数 = 通过」的宿主语义，校验本体归
        /// <see cref="JsonArgs.Validate"/>（单点真相源——与积木面同一实现，A228 档 1）。
        /// </summary>
        /// <param name="argsJson">参数 JSON（空=无参工具）</param>
        /// <param name="allowed">允许键（空格分隔；空=无参数）</param>
        /// <returns>错误文本（空=通过）</returns>
        private static string CheckHostArgs(string argsJson, string allowed)
        {
            if (argsJson == null || argsJson.Trim().Length == 0)
            {
                return "";
            }
            return JsonArgs.Validate(argsJson, allowed, "", "", "");
        }

        /// <summary>
        /// 工具组定义缺陷出声——池缺陷逐条落 L3 日志（A107：定义自曝失败不再静默丢组；design-ch4-tools §三·十三）。
        /// </summary>
        private static void LogToolPoolDefects()
        {
            ToolDefect[] defects = ToolPool.Defects();
            for (int i = 0; i < defects.Length; i = i + 1)
            {
                LogStore.Add("CatHome4", 3, "工具组定义缺陷 | " + defects[i].Group + " | " + defects[i].Stage + " | " + defects[i].Reason, "TOOL");
            }
        }

        /// <summary>
        /// 执行序对账出声——池内工具逐一比对 ToolOrderTable（A127）：未登记者静默落默认档，
        /// 此处出声 L2（新增工具忘记登记 = 隐性缺口，不静默）。
        /// </summary>
        private static void LogToolOrderAccounting()
        {
            string[] missing = ToolOrderTable.FindUnregistered(ToolPool.AllNames());
            if (missing.Length == 0)
            {
                return;
            }
            StringBuilder sb = new StringBuilder();
            for (int i = 0; i < missing.Length; i = i + 1)
            {
                if (i > 0)
                {
                    sb.Append(",");
                }
                sb.Append(missing[i]);
            }
            LogStore.Add("CatHome4", 2, "工具执行序未登记（落默认档 0）| " + sb.ToString(), "TOOL");
        }

        /// <summary>
        /// 域类型白名单对账出声——白名单里的名字必须在工具池内（拼写漂移 / 工具退役漏改的哨兵 ·
        /// design-ch4-timeback-type §十）：缺失即出声 L2，不静默（域内静默缺件 = 静默族）。
        /// </summary>
        private static void LogTimebackProfileAccounting()
        {
            string[] missing = TimebackProfile.Unknown(ToolPool.AllNames());
            if (missing.Length == 0)
            {
                return;
            }
            StringBuilder sb = new StringBuilder();
            for (int i = 0; i < missing.Length; i = i + 1)
            {
                if (i > 0)
                {
                    sb.Append(",");
                }
                sb.Append(missing[i]);
            }
            LogStore.Add("CatHome4", 2, "域类型白名单含池外工具名（拼写漂移 / 工具退役漏改）| " + sb.ToString(), "TIMEBACK");
        }

        /// <summary>
        /// 工具执行器路由——按工具名调度（宿主直执面：仅 host-* 宿主级工具；其余工具一律 OA 认领）
        /// </summary>
        /// <param name="name">工具名</param>
        /// <param name="argsJson">参数 JSON（展平）</param>
        /// <returns>执行结果（失败 ERR| 前缀——错误可见性）</returns>
        private static string ExecuteTool(string name, string argsJson)
        {
            if (name == "host-reload")
            {
                return ExecHostReload(argsJson);
            }
            if (name == "host-flows")
            {
                return ExecHostFlows(argsJson);
            }
            return "ERR|UNKNOWN_TOOL|未知工具: " + name;
        }
        /// <summary>
        /// 工具用法摘要——漂移新增工具（info tools_drift.added 条目；design-ch4-tools §三·十一）。
        /// 数据源 = 注册表声明（ToolRegistry.Find(name).Spec）：描述首句 + 参数键（必填标 *）。
        /// 注册表缺失 / 参数 JSON 不可析 → 退化为仅名字（未知工具仍可见，不静默丢条目）。
        /// </summary>
        /// <param name="name">工具名</param>
        /// <returns>摘要字典（name / desc / args）</returns>
        private static Dictionary<string, object> BuildToolBrief(string name)
        {
            Dictionary<string, object> brief = new Dictionary<string, object>();
            brief["name"] = name;
            brief["desc"] = "";
            brief["args"] = "";
            ToolRegistryEntry entry = ToolRegistry.Find(name);
            if (entry == null || entry.Spec == null)
            {
                return brief;
            }
            brief["desc"] = FirstSentence(entry.Spec.Description, 60);
            brief["args"] = BuildArgKeys(entry.Spec.ParametersJson);
            return brief;
        }
        /// <summary>
        /// 描述首句——截到首个句读（。；换行）；超出上限按字符截断加省略号。
        /// </summary>
        /// <param name="text">完整描述</param>
        /// <param name="max">字符上限</param>
        /// <returns>首句文本（无描述 = 空串）</returns>
        private static string FirstSentence(string text, int max)
        {
            if (text == null)
            {
                return "";
            }
            string line = text.Trim();
            // [段1] 取最早句读——多个标点命中时取位置最小者
            string[] stops = new string[] { "。", "；", "\n", ". " };
            int cut = -1;
            for (int i = 0; i < stops.Length; i = i + 1)
            {
                int idx = line.IndexOf(stops[i], StringComparison.Ordinal);
                if (idx >= 0 && (cut < 0 || idx < cut))
                {
                    cut = idx;
                }
            }
            if (cut >= 0)
            {
                line = line.Substring(0, cut);
            }
            line = line.Trim();
            // [段2] 超长截断——字符上限 + 省略号
            if (line.Length > max)
            {
                line = line.Substring(0, max) + "…";
            }
            return line;
        }
        /// <summary>
        /// 参数键摘要——从工具声明 parameters JSON 提取属性名（required 成员标 *）。
        /// </summary>
        /// <param name="parametersJson">参数 JSON Schema（空 = 无参数工具）</param>
        /// <returns>键摘要（如 "path*、start"；无参 / 不可析 = 空串）</returns>
        private static string BuildArgKeys(string parametersJson)
        {
            if (parametersJson == null || parametersJson.Length == 0)
            {
                return "";
            }
            try
            {
                // [段1] 必填清单——required 数组成员
                using (JsonDocument doc = JsonUtil.ParseStrict(parametersJson))
                {
                    JsonElement root = doc.RootElement;
                    List<string> required = new List<string>();
                    JsonElement requiredEl;
                    if (root.TryGetProperty("required", out requiredEl) && requiredEl.ValueKind == JsonValueKind.Array)
                    {
                        for (int i = 0; i < requiredEl.GetArrayLength(); i = i + 1)
                        {
                            JsonElement item = requiredEl[i];
                            if (item.ValueKind == JsonValueKind.String)
                            {
                                required.Add(item.GetString() ?? "");
                            }
                        }
                    }
                    // [段2] 属性名清单——按声明序拼接，必填标 *
                    JsonElement propertiesEl;
                    if (!root.TryGetProperty("properties", out propertiesEl) || propertiesEl.ValueKind != JsonValueKind.Object)
                    {
                        return "";
                    }
                    StringBuilder keys = new StringBuilder();
                    foreach (JsonProperty property in propertiesEl.EnumerateObject())
                    {
                        if (keys.Length > 0)
                        {
                            keys.Append("、");
                        }
                        keys.Append(property.Name);
                        if (required.Contains(property.Name))
                        {
                            keys.Append("*");
                        }
                    }
                    return keys.ToString();
                }
            }
            catch (Exception ex)
            {
                // 声明面 JSON 不可析——摘要退化（工具名仍可见；不阻断 info）
                LogStore.Add("CatHome4", 2, "工具参数摘要解析失败: " + ex.Message, "TOOLBRIEF");
                return "";
            }
        }

    }
}