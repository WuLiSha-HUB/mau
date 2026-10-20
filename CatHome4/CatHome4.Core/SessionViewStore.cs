using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;
using Mau.Runtime;
using CatHome4.Contracts;

namespace CH4
{
    /// <summary>
    /// 会话视图存储——视图层持久区（A156：与真实前文并列，载入即权威；A165 阶段 1：条目模型 v2）。
    /// 条目在事件发生点一次性定稿落盘（A248：JSONL 每块 append 一行——写入序即权威序）；载入逐行读回，不从前文重建。
    /// v2 契约：持久即持久——无键面 / 无换手 / 无移出（块键、生命周期状态、废弃块、截断通道全部退役）。
    /// 残留面只余顶尾补差（AppendTailMissing）与对账哨兵（AuditEntries），且不得改写已有条目。
    /// </summary>
    internal sealed class SessionViewStore
    {
        /// <summary>视图文件路径——sessions/&lt;id&gt;/&lt;id&gt;.view.jsonl</summary>
        private readonly string _path;

        /// <summary>块行标记——JSONL 唯一行型（{"t":"b",…ViewBlock 全字段}；注入报告亦为普通块）</summary>
        private const string BlockTag = "b";

        /// <summary>注入报告条目型——普通块（每次 session.new 一条；读面取最后一条；ts=0 恒排最前）</summary>
        private const string InjectReportType = "inject_report";

        /// <summary>落盘编码——UTF-8 无 BOM（JSONL 契约；与前文 jsonl 同源）</summary>
        private static readonly UTF8Encoding Utf8NoBom = new UTF8Encoding(false);

        /// <summary>内存视图条目——按生成序（真实前文 append 序）</summary>
        private readonly List<ViewBlock> _blocks = new List<ViewBlock>();

        /// <summary>待配对工具——assistant 工具调用登记，tool 结果到达时生成工具卡条目</summary>
        private readonly List<PendingTool> _pendingTools = new List<PendingTool>();

        /// <summary>
        /// 轮末统计条目——roundsum（每轮 CloseRound 生成：Token 消耗 + 用时；非真实前文派生；写入即落盘，GetBlocks 按帧合并）
        /// </summary>
        private readonly List<ViewBlock> _roundSums = new List<ViewBlock>();

        /// <summary>间隙文本条目——工具轮 seal 文本（非真实前文派生；写入即落盘；QQBot 转发与前端数据源）</summary>
        private readonly List<ViewBlock> _gapTexts = new List<ViewBlock>();

        /// <summary>错误条目——LLM 错误气泡（非真实前文派生；写入即落盘——刷新可回看）</summary>
        private readonly List<ViewBlock> _errors = new List<ViewBlock>();

        /// <summary>重试过程条目——retry 气泡（非真实前文派生；只增不改，每态一条；写入即落盘）</summary>
        private readonly List<ViewBlock> _retries = new List<ViewBlock>();

        /// <summary>最近条目时间戳——单调补差基准（载入后按既有条目最大值初始化；A156 I5）</summary>
        private long _lastTs;

        /// <summary>当前轮号——条目业务定位字段（会话侧每轮同步；QQ 转发按轮次定位的依据）</summary>
        private int _round;

        /// <summary>
        /// 建条通知——条目定稿入容器后调用（持久区「建条即推」的单一出口）。
        /// 消费方 = 会话侧接 ViewBus.PushPersist；前端语义「持久区多一条就渲一条」（只增不改、零配对）。
        /// 空 = 无消费方（无动作；视图层照常落盘）。
        /// </summary>
        public Action<ViewBlock> OnBlockAppended;

        /// <summary>
        /// 建立会话视图存储
        /// </summary>
        /// <param name="path">视图文件路径</param>
        public SessionViewStore(string path)
        {
            _path = path;
        }

        /// <summary>设当前轮号——会话侧轮开始 / 结束时同步（此后建条目的 round 字段取值）</summary>
        /// <param name="round">轮号（0 = 无轮）</param>
        public void SetRound(int round) { _round = round; }

        /// <summary>
        /// 注入报告条目——普通块（A248 尾：与其余条目同一模型 / 同一落盘 / 同一排序；ts=0 恒排最前）。
        /// 覆写语义——同型旧块建新条前移除，读面取最后一条。
        /// </summary>
        /// <param name="json">注入报告 JSON（file/status/…；空=清除报告）</param>
        public void SetInjectReport(string json)
        {
            RemoveInjectReportBlock();
            if (json == null || json.Length == 0)
            {
                // 清除语义——容器已无报告块，落盘反映（低频：会话初始化路径）
                Rewrite();
                return;
            }
            ViewBlock block = NewBlock(InjectReportType, json, 0, -1, false);
            // 置首——保持容器内 ts 升序（归并依赖各源有序；报告 ts=0 恒最小）
            _blocks.Insert(0, block);
            AppendLine(BuildBlockLine(block));
        }
        /// <summary>
        /// 注入报告 JSON——读（前端渲染数据源 / 留档；空串=无注入报告）
        /// </summary>
        /// <returns>注入报告 JSON</returns>
        public string GetInjectReport()
        {
            for (int i = _blocks.Count - 1; i >= 0; i = i - 1)
            {
                if (_blocks[i].RenderType == InjectReportType)
                {
                    return _blocks[i].Payload ?? "";
                }
            }
            return "";
        }

        /// <summary>移除注入报告块——覆写语义的单点（建新条前 / 载入去重时调用）。</summary>
        private void RemoveInjectReportBlock()
        {
            for (int i = _blocks.Count - 1; i >= 0; i = i - 1)
            {
                if (_blocks[i].RenderType == InjectReportType)
                {
                    _blocks.RemoveAt(i);
                }
            }
        }

        /// <summary>待配对工具条目</summary>
        private sealed class PendingTool
        {
            /// <summary>工具调用 ID——与 tool 消息配对锚点</summary>
            public string ToolCallId;

            /// <summary>工具名</summary>
            public string Name;

            /// <summary>参数摘要（≤200）</summary>
            public string Arguments;

            /// <summary>并发序号（1-based——tool_calls 数组顺序）</summary>
            public int Index;

            /// <summary>并发总数（同批 tool_calls 数组长度）</summary>
            public int Total;

            /// <summary>声明时刻（Unix 毫秒）——工具卡条目时间戳基准（A157：取工具调用声明消息 CreatedAt，与实时先行卡同基点）</summary>
            public long Ts;
        }



        /// <summary>内存视图条目——按生成序（全量帧数据源；注入报告为普通块，随归并自然排最前——ts=0）</summary>
        public ViewBlock[] GetBlocks()
        {
            return MergeBlocks();
        }

        /// <summary>
        /// 追加一行到视图文件——开-写-关（无长驻句柄；读侧共享无冲突）。
        /// 失败不阻断会话——记 L3 告警（失败必须可见）。
        /// </summary>
        /// <param name="line">行内容（单行 JSON）</param>
        private void AppendLine(string line)
        {
            if (line == null || line.Length == 0)
            {
                return;
            }
            try
            {
                EnsureDirectory();
                using (FileStream fs = new FileStream(_path, FileMode.Append, FileAccess.Write, FileShare.ReadWrite))
                {
                    using (StreamWriter sw = new StreamWriter(fs, Utf8NoBom))
                    {
                        sw.Write(line);
                        sw.Write('\n');
                    }
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("SessionViewStore", 3, "视图增量落盘失败（本条未落盘）: " + ex.Message, "SYS");
            }
        }

        /// <summary>确保视图文件目录存在。</summary>
        private void EnsureDirectory()
        {
            string dir = Path.GetDirectoryName(_path);
            if (dir != null && dir.Length > 0 && !Directory.Exists(dir))
            {
                Directory.CreateDirectory(dir);
            }
        }

        /// <summary>构建块行——ViewBlock 全字段 + 行型标记（t=b）。</summary>
        /// <param name="block">视图条目</param>
        /// <returns>单行 JSON</returns>
        private static string BuildBlockLine(ViewBlock block)
        {
            JsonSerializerOptions options = LineOptions();
            JsonNode node = JsonSerializer.SerializeToNode(block, options);
            if (node == null)
            {
                return "";
            }
            JsonObject obj = node.AsObject();
            obj["t"] = BlockTag;
            return obj.ToJsonString(options);
        }

        /// <summary>JSONL 序列化选项——中文直出（默认 \uXXXX 转义人读不便——2026-09-08 全局统一）。</summary>
        /// <returns>选项</returns>
        private static JsonSerializerOptions LineOptions()
        {
            JsonSerializerOptions options = new JsonSerializerOptions();
            options.Encoder = System.Text.Encodings.Web.JavaScriptEncoder.UnsafeRelaxedJsonEscaping;
            return options;
        }

        /// <summary>
        /// 解析一行——块行（t=b → block；唯一行型，注入报告亦为普通块）。
        /// 解析失败或行型未知 → false（坏行——调用方按位置出警处置）。
        /// </summary>
        /// <param name="line">行内容</param>
        /// <param name="block">解析出的条目（无=null）</param>
        /// <returns>true=行型已识别</returns>
        private static bool TryParseLine(string line, out ViewBlock block)
        {
            block = null;
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(line))
                {
                    JsonElement root = doc.RootElement;
                    if (root.ValueKind != JsonValueKind.Object)
                    {
                        return false;
                    }
                    string tag = GetStringProp(root, "t");
                    if (tag == BlockTag)
                    {
                        ViewBlock b = new ViewBlock();
                        b.RenderType = GetStringProp(root, "RenderType");
                        b.Payload = GetStringProp(root, "Payload");
                        b.Timestamp = ReadLongProp(root, "Timestamp");
                        b.MsgIndex = (int)ReadLongProp(root, "MsgIndex");
                        b.Round = (int)ReadLongProp(root, "Round");
                        block = b;
                        return true;
                    }
                    return false;
                }
            }
            catch (Exception)
            {
                // 解析失败 = 坏行（载入侧按位置出警——此处不重复出声）
                return false;
            }
        }

        /// <summary>载入分流——按条目型归入对应容器（缺省前文派生面）。</summary>
        /// <param name="block">载入条目</param>
        private void AddLoadedBlock(ViewBlock block)
        {
            string type = block.RenderType == null ? "" : block.RenderType;
            if (type == "gap_text")
            {
                _gapTexts.Add(block);
                return;
            }
            if (type == "roundsum" || type == "roundsummary")
            {
                _roundSums.Add(block);
                return;
            }
            if (type == "error")
            {
                _errors.Add(block);
                return;
            }
            if (type == "retry")
            {
                _retries.Add(block);
                return;
            }
            if (type == InjectReportType)
            {
                // 覆写语义——多行同型只认最后一条（正常路径下文件内至多一条：Clear/Rewrite 会压实）
                // 置首——保持容器内 ts 升序（归并依赖各源有序；报告 ts=0 恒最小）
                RemoveInjectReportBlock();
                _blocks.Insert(0, block);
                return;
            }
            _blocks.Add(block);
        }

        /// <summary>合并面——五类块源按时间戳升序归并（同一坐标系；注入报告为普通块，随归并排最前——ts=0）。</summary>
        /// <returns>归并后的条目数组</returns>
        private ViewBlock[] MergeBlocks()
        {
            List<ViewBlock>[] sources = new List<ViewBlock>[] { _blocks, _gapTexts, _roundSums, _errors, _retries };
            int total = 0;
            for (int si = 0; si < sources.Length; si = si + 1)
            {
                total = total + sources[si].Count;
            }
            ViewBlock[] merged = new ViewBlock[total];
            int[] cursors = new int[sources.Length];
            for (int mi = 0; mi < total; mi = mi + 1)
            {
                int best = -1;
                long bestTs = long.MaxValue;
                for (int si = 0; si < sources.Length; si = si + 1)
                {
                    if (cursors[si] >= sources[si].Count)
                    {
                        continue;
                    }
                    long ts = sources[si][cursors[si]].Timestamp;
                    if (best < 0 || ts < bestTs)
                    {
                        best = si;
                        bestTs = ts;
                    }
                }
                merged[mi] = sources[best][cursors[best]];
                cursors[best] = cursors[best] + 1;
            }
            return merged;
        }

        /// <summary>
        /// 真实前文 append 钩子——用户消息 → user 条目（人工路径专属）。
        /// </summary>
        /// <param name="m">真实前文消息</param>
        /// <param name="msgIndex">真实前文消息索引（条目业务定位字段）</param>
        /// <param name="origin">原注入来源标记（空串=非 QQ 注入——不落字段；非空=QQ 注入的轮起点锚，落载荷 origin）</param>
        public void OnUserMessage(LlmMessage m, int msgIndex, string origin = "")
        {
            Dictionary<string, object> payload = new Dictionary<string, object>();
            payload["text"] = m.Content ?? "";
            // 原注入来源标记——QQ 注入的轮起点锚（缺省不落字段——旧视图文件零迁移）
            if (origin != null && origin.Length > 0)
            {
                payload["origin"] = origin;
            }
            Append(m, "user", payload, msgIndex);
        }
        /// <summary>
        /// 真实前文 append 钩子——系统注入消息 → systemautomessage 条目（载荷携带原始来源族名）。
        /// </summary>
        /// <param name="m">真实前文消息</param>
        /// <param name="msgIndex">真实前文消息索引（条目业务定位字段）</param>
        /// <param name="source">来源族名（systemauto / sleep / timer / delay / restart——原值入载荷，不塌缩）</param>
        /// <param name="origin">原注入来源标记（空串=非 QQ 注入——不落字段；非空=QQ 注入的轮起点锚，落载荷 origin）</param>
        public void OnSystemMessage(LlmMessage m, int msgIndex, string source, string origin = "")
        {
            Dictionary<string, object> payload = new Dictionary<string, object>();
            payload["text"] = m.Content ?? "";
            payload["source"] = source ?? "";
            if (origin != null && origin.Length > 0)
            {
                payload["origin"] = origin;
            }
            Append(m, "systemautomessage", payload, msgIndex);
        }

        /// <summary>
        /// 真实前文 append 钩子——assistant 纯文本回复 → reason 条目（有思考时，先落）+ text 条目
        /// </summary>
        /// <param name="m">真实前文消息</param>
        /// <param name="msgIndex">真实前文消息索引（条目业务定位字段）</param>
        /// <param name="reasonDurMs">思考用时（毫秒；-1 = 未记录——载入顶尾补差等无实时数据路径）</param>
        public void OnAssistantText(LlmMessage m, int msgIndex, long reasonDurMs = -1)
        {
            // A158 期三——纯文本轮的思考段落条目（工具轮走 OnAssistantToolCalls；缺此路径思考内容在持久区丢失）
            string reasoning = m.ReasoningContent ?? "";
            if (reasoning.Length > 0)
            {
                Append(m, "reason", ViewCardPayload.BuildReason(reasoning, reasonDurMs), msgIndex);
            }
            Dictionary<string, object> payload = new Dictionary<string, object>();
            payload["text"] = m.Content ?? "";
            Append(m, "text", payload, msgIndex);
        }

        /// <summary>
        /// 真实前文 append 钩子——assistant 工具调用声明 → reason 条目（有思考时）+ 登记待配对工具
        /// </summary>
        /// <param name="m">真实前文消息</param>
        /// <param name="msgIndex">真实前文消息索引（条目业务定位字段）</param>
        /// <param name="reasonDurMs">思考用时（毫秒；-1 = 未记录——载入顶尾补差等无实时数据路径）</param>
        public void OnAssistantToolCalls(LlmMessage m, int msgIndex, long reasonDurMs = -1)
        {
            string reasoning = m.ReasoningContent ?? "";
            if (reasoning.Length > 0)
            {
                Append(m, "reason", ViewCardPayload.BuildReason(reasoning, reasonDurMs), msgIndex);
            }
            RegisterPendingTools(m.ToolCallsJson ?? "", m.CreatedAt);
        }

        /// <summary>
        /// 真实前文 append 钩子——tool 结果 → 配对生成工具卡条目（孤立 tool 丢弃——视图容错）。
        /// A157：条目时间戳取工具调用声明时刻（target.Ts），与实时区先行卡同基点。
        /// </summary>
        /// <param name="m">真实前文消息</param>
        /// <param name="msgIndex">真实前文消息索引（条目业务定位字段）</param>
        /// <param name="durMs">运行时长（毫秒；-1 = 未记录——由会话侧按工具单派发时刻结算；进载荷）</param>
        public void OnToolResult(LlmMessage m, int msgIndex, long durMs)
        {
            string toolCallId = m.ToolCallId ?? "";
            PendingTool target = null;
            for (int i = _pendingTools.Count - 1; i >= 0; i = i - 1)
            {
                if (_pendingTools[i].ToolCallId == toolCallId)
                {
                    target = _pendingTools[i];
                    _pendingTools.RemoveAt(i);
                    break;
                }
            }
            if (target == null)
            {
                return; // 孤立 tool 丢弃
            }
            string name = target.Name.Length > 0 ? target.Name : (m.ToolName ?? "");
            // A69 视图层报错中文注释——真实前文保持原文，仅视图条目追加中文注释
            string viewResult = ErrorNote.Apply(m.Content ?? "");
            Dictionary<string, object> payload = ViewCardPayload.BuildToolCard(name, target.Arguments, viewResult, target.Index, target.Total, durMs);
            Append(m, "toolcard", payload, msgIndex, target.Ts);
        }

        /// <summary>
        /// 全量重写视图文件（原子——临时文件 + 替换）——清空 / 截断 / 播种场景唯一写通道。
        /// append-only 的合法例外：清空与尾部截断无法用追加表达（低频操作）。
        /// </summary>
        public void Rewrite()
        {
            // [段1] 组装保留面——条目流（按时间戳升序；注入报告为普通块，随流落位——ts=0 居首）
            StringBuilder sb = new StringBuilder();
            ViewBlock[] all = MergeBlocks();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                sb.Append(BuildBlockLine(all[i]));
                sb.Append('\n');
            }
            // [段2] 原子替换——临时文件写全量 → 更名上架（写中崩不毁既有文件）
            try
            {
                EnsureDirectory();
                string tmp = _path + "." + Guid.NewGuid().ToString("N") + ".tmp";
                File.WriteAllText(tmp, sb.ToString(), Utf8NoBom);
                File.Move(tmp, _path, true);
            }
            catch (Exception ex)
            {
                // A156：视图层为权威——保存失败必须可见（不兜底、不遮蔽：写盘失败是必须暴露的缺陷）
                LogStore.Add("SessionViewStore", 3, "视图重写失败: " + ex.Message, "SYS");
            }
        }

        /// <summary>追加轮末统计条目——roundsum（Token 消耗 + 工具次数 + 总耗时 + 用时）。非真实前文派生；写入即落盘——宿主中断不丢。</summary>
        /// <param name="payloadJson">roundsum 载荷 JSON（{"data":{...}}——载荷不带 type，条目型由块承载）</param>
        /// <param name="timestamp">创建时间戳（Unix 毫秒——与消息条目同坐标系，归并排序键）</param>
        public void AppendRoundSummary(string payloadJson, long timestamp)
        {
            ViewBlock block = NewBlock("roundsum", payloadJson, timestamp, -1);
            _roundSums.Add(block);
            AppendLine(BuildBlockLine(block));
            EmitBlock(block);
        }

        /// <summary>追加轮末总结条目——roundsummary（LLMSimpleRequest 产出一句话总结）。与 roundsum 同属轮末条目族（同容器承载、type 区分——复用车、落盘、合并、补差全链）；写入即落盘——宿主中断不丢。音频字段（A265）为拼接后单文件名——仅供前端点播，无音频时缺省不写。</summary>
        /// <param name="text">总结文本（失败侧为 ERR| 文本——失败可见）</param>
        /// <param name="timestamp">创建时间戳（= roundsum ts + 1——固定插在轮末统计之后）</param>
        /// <param name="audioName">拼接后音频文件名（空=不写该字段）</param>
        public void AppendRoundSummaryNote(string text, long timestamp, string audioName)
        {
            if (text == null || text.Length == 0)
            {
                return;
            }
            Dictionary<string, object> payload = new Dictionary<string, object>();
            payload["text"] = text;
            if (audioName != null && audioName.Length > 0)
            {
                payload["audio"] = audioName;
            }
            ViewBlock block = NewBlock("roundsummary", JsonUtil.Serialize(payload), timestamp, -1);
            _roundSums.Add(block);
            AppendLine(BuildBlockLine(block));
            EmitBlock(block);
        }

        /// <summary>追加间隙文本条目——工具轮 seal 文本（模型调用工具前说的话）。非真实前文派生；写入即落盘——工具轮中途中断不丢。视图层 = 全部外观真源——前端与 QQBot 转发统一消费此条目。
        /// 条目型 = `gap_text`（A188：与正式回复 `text` 分型——消费方据此区分两义；外观形态仍为气泡件）。</summary>
        /// <param name="content">间隙文本</param>
        /// <param name="timestamp">创建时间戳（Unix 毫秒——与消息条目同坐标系）</param>
        public void AppendGapText(string content, long timestamp)
        {
            if (content == null || content.Length == 0)
            {
                return;
            }
            Dictionary<string, object> payload = new Dictionary<string, object>();
            payload["text"] = content;
            ViewBlock block = NewBlock("gap_text", JsonUtil.Serialize(payload), timestamp, -1);
            _gapTexts.Add(block);
            AppendLine(BuildBlockLine(block));
            EmitBlock(block);
        }

        /// <summary>
        /// 追加错误条目——LLM 错误气泡（非真实前文派生；写入即落盘——刷新可回看）。
        /// </summary>
        /// <param name="text">错误文本</param>
        /// <param name="timestamp">创建时间戳（Unix 毫秒——与消息条目同坐标系）</param>
        public void AppendError(string text, long timestamp)
        {
            if (text == null || text.Length == 0)
            {
                return;
            }
            Dictionary<string, object> payload = new Dictionary<string, object>();
            payload["text"] = text;
            ViewBlock block = NewBlock("error", JsonUtil.Serialize(payload), timestamp, -1);
            _errors.Add(block);
            AppendLine(BuildBlockLine(block));
            EmitBlock(block);
        }

        /// <summary>
        /// 追加重试条目——retry 气泡（A158 期三：只增不改——「重试中 / 成功 / 失败」各推一条，不做原位更新）。
        /// </summary>
        /// <param name="payloadJson">retry 载荷 JSON（state/attempt/max/text）</param>
        /// <param name="timestamp">创建时间戳（Unix 毫秒）</param>
        public void AppendRetry(string payloadJson, long timestamp)
        {
            ViewBlock block = NewBlock("retry", payloadJson, timestamp, -1);
            _retries.Add(block);
            AppendLine(BuildBlockLine(block));
            EmitBlock(block);
        }

        /// <summary>文件名清洗——Windows 非法文件名字符替换为下划线并去首尾空白（显示名可含中文与空格）</summary>
        /// <param name="name">原始名（猫显示名 / 猫 key）</param>
        /// <returns>可作文件名前缀的串（空=原名缺失）</returns>
        private static string SanitizeFileName(string name)
        {
            if (name == null)
            {
                return "";
            }
            char[] invalid = Path.GetInvalidFileNameChars();
            StringBuilder sb = new StringBuilder();
            for (int i = 0; i < name.Length; i = i + 1)
            {
                char c = name[i];
                bool bad = false;
                for (int j = 0; j < invalid.Length; j = j + 1)
                {
                    if (invalid[j] == c)
                    {
                        bad = true;
                        break;
                    }
                }
                if (bad)
                {
                    sb.Append('_');
                }
                else
                {
                    sb.Append(c);
                }
            }
            return sb.ToString().Trim();
        }

        /// <summary>
        /// 旧会话留档——session.new 清空前导出（A87：user 消息 / 正式回复 / 注入报告 / 每轮结算 → sessions_old 下 MD 文件）。
        /// 只留四部分（思考条目 / 工具卡 / 间隙文本 / 错误 / 重试一律不进档）；四部分全空也照常出档（档案面留痕优先于体积）。
        /// </summary>
        /// <param name="catKey">猫 key——文件名前缀（与会话目录名同源）</param>
        /// <param name="displayName">会话显示名——写入文件头</param>
        /// <returns>落盘文件绝对路径（空串=未生成；失败已记 ERR 日志）</returns>
        public string ArchiveLegacy(string catKey, string displayName)
        {
            try
            {
                // [段1] 落点——sessions_old 与 sessions 同级（视图路径形态 <data>/sessions/<id>/<id>.view.jsonl）
                string sessionDir = Path.GetDirectoryName(_path);
                string sessionsRoot = null;
                if (sessionDir != null && sessionDir.Length > 0)
                {
                    sessionsRoot = Path.GetDirectoryName(sessionDir);
                }
                string dataDir = null;
                if (sessionsRoot != null && sessionsRoot.Length > 0 && Path.GetFileName(sessionsRoot) == "sessions")
                {
                    dataDir = Path.GetDirectoryName(sessionsRoot);
                }
                if (dataDir == null || dataDir.Length == 0)
                {
                    LogStore.Add("CatHome4", 3, "旧会话留档跳过：视图路径形态不符 " + _path, "CHAT");
                    return "";
                }
                string archiveDir = Path.Combine(dataDir, "sessions_old");
                if (!Directory.Exists(archiveDir))
                {
                    Directory.CreateDirectory(archiveDir);
                }
                // [段2] 文件名——猫显示名 + 归档时间戳（非法字符清洗；显示名缺省回落猫 key；同秒重复触发加序号，不覆盖既有档案）
                string prefix = SanitizeFileName(displayName);
                if (prefix.Length == 0)
                {
                    prefix = SanitizeFileName(catKey);
                }
                string stamp = DateTime.Now.ToString("yyyyMMdd_HHmmss");
                string file = Path.Combine(archiveDir, prefix + "_" + stamp + ".md");
                int suffix = 1;
                while (File.Exists(file))
                {
                    suffix = suffix + 1;
                    file = Path.Combine(archiveDir, prefix + "_" + stamp + "_" + suffix.ToString() + ".md");
                }
                // [段3] 生成 + 落盘（.md 契约：UTF-8 BOM；换行由写侧保真）
                string text = BuildLegacyMarkdown(catKey, displayName, stamp);
                File.WriteAllText(file, text, new UTF8Encoding(true));
                return file;
            }
            catch (Exception ex)
            {
                // 留档失败不阻断新会话（失败可见——ERR 日志 + 空返回）
                LogStore.Add("CatHome4", 3, "旧会话留档失败: " + ex.Message, "CHAT");
                return "";
            }
        }

        /// <summary>
        /// 生成留档 Markdown——文件头 + 新会话加载报告 + 对话时序（user / 正式回复 / 轮结算三类条目，按时间戳归并）。
        /// </summary>
        /// <param name="catKey">猫 key</param>
        /// <param name="displayName">会话显示名</param>
        /// <param name="stamp">归档时间戳（文件名同源）</param>
        /// <returns>Markdown 全文</returns>
        private string BuildLegacyMarkdown(string catKey, string displayName, string stamp)
        {
            // [段1] 对话段——两源各自按时间戳升序，手工归并（_blocks 只取 user/text，roundsum 全取）
            StringBuilder body = new StringBuilder();
            long firstTs = 0;
            long lastTs = 0;
            bool hasTs = false;
            int userCount = 0;
            int replyCount = 0;
            int blockCursor = 0;
            int sumCursor = 0;
            while (blockCursor < _blocks.Count || sumCursor < _roundSums.Count)
            {
                while (blockCursor < _blocks.Count && !IsLegacyBlock(_blocks[blockCursor].RenderType))
                {
                    blockCursor = blockCursor + 1;
                }
                bool takeBlock = false;
                if (blockCursor >= _blocks.Count)
                {
                    takeBlock = false;
                }
                else if (sumCursor >= _roundSums.Count)
                {
                    takeBlock = true;
                }
                else if (_blocks[blockCursor].Timestamp <= _roundSums[sumCursor].Timestamp)
                {
                    takeBlock = true;
                }
                ViewBlock current;
                if (takeBlock)
                {
                    current = _blocks[blockCursor];
                    blockCursor = blockCursor + 1;
                }
                else
                {
                    current = _roundSums[sumCursor];
                    sumCursor = sumCursor + 1;
                }
                if (current.RenderType == "user")
                {
                    userCount = userCount + 1;
                }
                else if (current.RenderType == "text")
                {
                    replyCount = replyCount + 1;
                }
                if (!hasTs || current.Timestamp < firstTs)
                {
                    firstTs = current.Timestamp;
                }
                hasTs = true;
                if (current.Timestamp > lastTs)
                {
                    lastTs = current.Timestamp;
                }
                AppendLegacyBlock(body, current);
            }
            // [段2] 头部——猫 / 归档时刻 / 区间 / 条目数（无时间条目时区间记「—」）
            string range = "—";
            if (hasTs)
            {
                range = FormatLegacyTime(firstTs) + " ~ " + FormatLegacyTime(lastTs);
            }
            StringBuilder sb = new StringBuilder();
            sb.Append("# 会话留档 — " + displayName + "\n\n");
            sb.Append("- 猫：`" + catKey + "`（" + displayName + "）\n");
            sb.Append("- 归档：" + stamp + "\n");
            sb.Append("- 区间：" + range + "\n");
            sb.Append("- 块数：user " + userCount.ToString() + " · 回复 " + replyCount.ToString() + " · 轮结算 " + _roundSums.Count.ToString() + "\n\n");
            sb.Append("---\n\n");
            // [段3] 加载报告段 + 对话段
            AppendInjectReportSection(sb, GetInjectReport());
            sb.Append("---\n\n");
            sb.Append("## 对话\n\n");
            sb.Append(body.ToString());
            return sb.ToString();
        }

        /// <summary>留档入选判定——user 消息与正式回复（text）进档，其余条目型一律丢弃</summary>
        /// <param name="renderType">条目渲染类型</param>
        /// <returns>true=进档</returns>
        private static bool IsLegacyBlock(string renderType)
        {
            if (renderType == null)
            {
                return false;
            }
            return renderType == "user" || renderType == "text";
        }

        /// <summary>留档条目渲染——按条目型分派（user/text 取 payload.text；roundsum 转简洁统计行）</summary>
        /// <param name="sb">目标缓冲</param>
        /// <param name="b">待渲染条目</param>
        private static void AppendLegacyBlock(StringBuilder sb, ViewBlock b)
        {
            string type = b.RenderType;
            if (type == null)
            {
                type = "";
            }
            string title = "用户";
            if (type == "text")
            {
                title = "回复";
            }
            else if (type == "roundsum")
            {
                title = "轮结算";
            }
            sb.Append("### " + FormatLegacyTime(b.Timestamp) + " · " + title + "\n\n");
            if (type == "roundsum")
            {
                sb.Append(BuildRoundSumLine(b.Payload) + "\n\n");
                return;
            }
            string text = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(b.Payload))
                {
                    text = GetStringProp(doc.RootElement, "text");
                }
            }
            catch (Exception ex)
            {
                // 单条解析失败不拖垮整份留档（局部降级可见——不静默丢条目）
                LogStore.Add("CatHome4", 2, "旧会话留档条目解析失败: " + ex.Message, "CHAT");
            }
            sb.Append(text + "\n\n");
        }

        /// <summary>轮结算行渲染——roundsum 载荷转人读一行（Token / 缓存 / 工具次数 / 请求次数 / 用时）</summary>
        /// <param name="payloadJson">roundsum 载荷 JSON</param>
        /// <returns>单行文本</returns>
        private static string BuildRoundSumLine(string payloadJson)
        {
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(payloadJson))
                {
                    JsonElement root = doc.RootElement;
                    JsonElement data;
                    if (!root.TryGetProperty("data", out data))
                    {
                        return "（载荷缺 data 字段）";
                    }
                    long prompt = ReadLongProp(data, "prompt");
                    long completion = ReadLongProp(data, "completion");
                    long cacheHit = ReadLongProp(data, "cacheHit");
                    long toolCount = ReadLongProp(data, "toolCount");
                    long requests = ReadLongProp(data, "requests");
                    long elapsedMs = ReadLongProp(data, "elapsedMs");
                    double seconds = elapsedMs / 1000.0;
                    return "Token 上 " + prompt.ToString() + " / 下 " + completion.ToString()
                        + "（缓存 " + cacheHit.ToString() + "）· 工具 " + toolCount.ToString() + " 次"
                        + " · 请求 " + requests.ToString() + " 次 · 用时 " + seconds.ToString("0.0") + " 秒";
                }
            }
            catch (Exception ex)
            {
                // 解析失败降级为原文（不静默丢统计）
                LogStore.Add("CatHome4", 2, "旧会话留档结算解析失败: " + ex.Message, "CHAT");
                return "（解析失败）" + payloadJson;
            }
        }

        /// <summary>加载报告段渲染——旧会话注入报告（逐文件清单 + 工具组清单）；无报告记一行，解析失败降级为原文</summary>
        /// <param name="sb">目标缓冲</param>
        /// <param name="injectReport">注入报告 JSON（空=无）</param>
        private static void AppendInjectReportSection(StringBuilder sb, string injectReport)
        {
            sb.Append("## 新会话加载报告\n\n");
            if (injectReport == null || injectReport.Length == 0)
            {
                sb.Append("- 无（旧会话未生成注入报告）\n\n");
                return;
            }
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(injectReport))
                {
                    JsonElement root = doc.RootElement;
                    long total = ReadLongProp(root, "total");
                    long ok = ReadLongProp(root, "ok");
                    long missing = ReadLongProp(root, "missing");
                    long failed = ReadLongProp(root, "failed");
                    sb.Append("- 注入清单：" + total.ToString() + " 个文件（ok " + ok.ToString()
                        + " / missing " + missing.ToString() + " / 失败 " + failed.ToString() + "）\n");
                    JsonElement files;
                    if (root.TryGetProperty("files", out files) && files.ValueKind == JsonValueKind.Array)
                    {
                        sb.Append("\n");
                        foreach (JsonElement f in files.EnumerateArray())
                        {
                            sb.Append("- `" + GetStringProp(f, "file") + "` — " + GetStringProp(f, "status")
                                + "（" + ReadLongProp(f, "chars").ToString() + " 字符）\n");
                        }
                    }
                    JsonElement groups;
                    if (root.TryGetProperty("toolGroups", out groups) && groups.ValueKind == JsonValueKind.Array)
                    {
                        sb.Append("\n");
                        foreach (JsonElement g in groups.EnumerateArray())
                        {
                            sb.Append("- 工具组 `" + GetStringProp(g, "group") + "`：");
                            JsonElement tools;
                            bool first = true;
                            if (g.TryGetProperty("tools", out tools) && tools.ValueKind == JsonValueKind.Array)
                            {
                                foreach (JsonElement t in tools.EnumerateArray())
                                {
                                    if (!first)
                                    {
                                        sb.Append("、");
                                    }
                                    first = false;
                                    sb.Append(GetStringProp(t, "name"));
                                }
                            }
                            sb.Append("\n");
                        }
                    }
                }
            }
            catch (Exception ex)
            {
                // 报告解析失败降级为原文（可辨识——不静默丢内容）
                LogStore.Add("CatHome4", 2, "旧会话留档报告解析失败: " + ex.Message, "CHAT");
                sb.Append("- 解析失败，原文：\n\n```json\n" + injectReport + "\n```\n");
            }
            sb.Append("\n");
        }

        /// <summary>留档时间戳格式化——Unix 毫秒 → 本地「MM-dd HH:mm:ss」（非正值记「—」）</summary>
        /// <param name="ts">Unix 毫秒时间戳</param>
        /// <returns>格式化文本</returns>
        private static string FormatLegacyTime(long ts)
        {
            if (ts <= 0)
            {
                return "—";
            }
            return DateTimeOffset.FromUnixTimeMilliseconds(ts).ToLocalTime().ToString("MM-dd HH:mm:ss");
        }

        /// <summary>读取 JSON 数值属性——防御式（缺字段 / 非数值返回 0）</summary>
        /// <param name="data">JSON 对象</param>
        /// <param name="prop">属性名</param>
        /// <returns>数值（缺省 0）</returns>
        private static long ReadLongProp(JsonElement data, string prop)
        {
            JsonElement value;
            if (!data.TryGetProperty(prop, out value))
            {
                return 0;
            }
            if (value.ValueKind != JsonValueKind.Number)
            {
                return 0;
            }
            return value.GetInt64();
        }

        /// <summary>清空视图层——session.new 清前文时同步（真实前文 Clear 后视图随生命周期清理）</summary>
        public void Clear()
        {
            _blocks.Clear();
            _pendingTools.Clear();
            // 注入报告为普通块——随 _blocks 一并清理（session.new 后 HandleSessionNew 重新 Set）
            // 间隙文本随视图层清理——新会话不保留旧 gap 条目
            _gapTexts.Clear();
            // 轮末统计随视图层清理——新会话不保留旧轮统计
            _roundSums.Clear();
            // 错误条目随视图层清理——新会话不保留旧错误气泡
            _errors.Clear();
            // 重试过程条目随视图层清理——新会话不保留旧重试记录
            _retries.Clear();
            // A248：清空同步落盘（原子重写为空）——否则旧行在载入时被读回，清空语义失效
            Rewrite();
        }
        /// <summary>
        /// 视图层截断——回滚（session.rollback）：丢弃前文索引大于切点的条目，与真实前文截断同批物化。
        /// 判定：前文派生条目（MsgIndex ≥ 0）按索引；非前文派生条目（-1）按时间戳界限——时间戳单调化
        /// 保证全局严格递增，故被截区间内产生的一切条目时间戳必不小于「被丢弃前文条目的最小时间戳」。
        /// 保留面 = 切点及之前的全部条目（注入报告为普通块——MsgIndex=-1 不参与索引判定，天然保留）；写盘。
        /// </summary>
        /// <param name="msgIndex">切点前文索引（保留 ≤ 该索引的条目）</param>
        public void TruncateFrom(int msgIndex)
        {
            // [段1] 前文派生条目——按索引判定（保留 ≤ msgIndex）
            long dropMinTs = long.MaxValue;
            for (int i = _blocks.Count - 1; i >= 0; i = i - 1)
            {
                if (_blocks[i].MsgIndex > msgIndex)
                {
                    if (_blocks[i].Timestamp < dropMinTs)
                    {
                        dropMinTs = _blocks[i].Timestamp;
                    }
                    _blocks.RemoveAt(i);
                }
            }
            // [段2] 非前文派生条目——按时间戳界限丢弃（不早于被截区间起点者属于被截区间）
            if (dropMinTs < long.MaxValue)
            {
                DropBlocksFrom(_gapTexts, dropMinTs);
                DropBlocksFrom(_roundSums, dropMinTs);
                DropBlocksFrom(_errors, dropMinTs);
                DropBlocksFrom(_retries, dropMinTs);
            }
            // [段3] 待配对工具——被截区间内的工具声明不再配对（回滚仅空闲时执行，通常本就为空）
            _pendingTools.Clear();
            Rewrite();
        }
        /// <summary>
        /// 视图层播种——fork（session.fork）：把源视图存储的保留面（≤ 切点）复制进本存储，与新猫播种前文同步。
        /// 保留口径与 <see cref="TruncateFrom"/> 同源（前文派生按索引 · 非前文派生按时间戳界限）；
        /// 条目按引用搬入（只读沿用，不复制对象）；注入报告随面搬入；写盘。
        /// </summary>
        /// <param name="source">源视图存储（同类实例）</param>
        /// <param name="msgIndex">切点前文索引（保留 ≤ 该索引）</param>
        public void CopyFrom(SessionViewStore source, int msgIndex)
        {
            if (source == null)
            {
                return;
            }
            long dropMinTs = long.MaxValue;
            _blocks.Clear();
            for (int i = 0; i < source._blocks.Count; i = i + 1)
            {
                ViewBlock b = source._blocks[i];
                if (b.MsgIndex > msgIndex)
                {
                    if (b.Timestamp < dropMinTs)
                    {
                        dropMinTs = b.Timestamp;
                    }
                }
                else
                {
                    _blocks.Add(b);
                }
            }
            _gapTexts.Clear();
            CopyBlocksBefore(source._gapTexts, _gapTexts, dropMinTs);
            _roundSums.Clear();
            CopyBlocksBefore(source._roundSums, _roundSums, dropMinTs);
            _errors.Clear();
            CopyBlocksBefore(source._errors, _errors, dropMinTs);
            _retries.Clear();
            CopyBlocksBefore(source._retries, _retries, dropMinTs);
            _pendingTools.Clear();
            // 注入报告为普通块——随 _blocks 搬迁（MsgIndex=-1 不参与索引判定，天然保留）
            _lastTs = MaxTimestamp();
            Rewrite();
        }
        /// <summary>丢弃时间戳不早于界限的条目——截断用（保留面 = 界限之前）。</summary>
        /// <param name="list">条目容器（原地删）</param>
        /// <param name="cutoff">界限（时间戳 ≥ 该值即丢弃）</param>
        private static void DropBlocksFrom(List<ViewBlock> list, long cutoff)
        {
            for (int i = list.Count - 1; i >= 0; i = i - 1)
            {
                if (list[i].Timestamp >= cutoff)
                {
                    list.RemoveAt(i);
                }
            }
        }
        /// <summary>搬运时间戳早于界限的条目——播种用（复制引用，不改对象）。</summary>
        /// <param name="source">源容器</param>
        /// <param name="dest">目标容器</param>
        /// <param name="cutoff">界限（时间戳 ≥ 该值即跳过）</param>
        private static void CopyBlocksBefore(List<ViewBlock> source, List<ViewBlock> dest, long cutoff)
        {
            for (int i = 0; i < source.Count; i = i + 1)
            {
                if (source[i].Timestamp < cutoff)
                {
                    dest.Add(source[i]);
                }
            }
        }

        /// <summary>
        /// 生成前文派生视图条目——时间戳取消息 CreatedAt（A157：工具卡可指定时间戳覆盖——取工具调用声明时刻，与实时区先行卡同基点）。
        /// </summary>
        /// <param name="m">真实前文消息</param>
        /// <param name="renderType">渲染类型</param>
        /// <param name="payload">渲染载荷（字典）</param>
        /// <param name="msgIndex">真实前文消息索引（条目业务定位字段）</param>
        /// <param name="timestampOverride">时间戳覆盖（>0 生效；0 = 取消息 CreatedAt——工具卡取声明时刻）</param>
        private void Append(LlmMessage m, string renderType, Dictionary<string, object> payload, int msgIndex, long timestampOverride = 0)
        {
            string payloadJson = JsonUtil.Serialize(payload);
            long timestamp = timestampOverride > 0 ? timestampOverride : m.CreatedAt;
            ViewBlock block = NewBlock(renderType, payloadJson, timestamp, msgIndex);
            _blocks.Add(block);
            AppendLine(BuildBlockLine(block));
            EmitBlock(block);
        }

        /// <summary>
        /// 建条单点——时间戳单调化 + 业务定位字段就位（v2：无键面 / 无生命周期 / 无来源类别）。
        /// </summary>
        /// <param name="renderType">渲染类型</param>
        /// <param name="payloadJson">渲染载荷 JSON</param>
        /// <param name="timestamp">创建时间戳（Unix 毫秒——前文派生取消息 CreatedAt）</param>
        /// <param name="msgIndex">前文消息索引（-1 = 非前文派生）</param>
        /// <param name="monotonic">是否参与时间戳单调化（默认 true；归档类条目取历史时间戳时传 false）</param>
        /// <returns>已定稿的视图条目</returns>
        private ViewBlock NewBlock(string renderType, string payloadJson, long timestamp, int msgIndex, bool monotonic = true)
        {
            long ts = monotonic ? MonotonicTs(timestamp) : timestamp;
            ViewBlock block = new ViewBlock();
            block.RenderType = renderType;
            block.Payload = payloadJson;
            block.Timestamp = ts;
            block.MsgIndex = msgIndex;
            block.Round = _round;
            return block;
        }

        /// <summary>
        /// 建条通知——条目定稿入容器后调用，供会话侧接持久区推送出口（ViewBus.PushPersist）。
        /// </summary>
        /// <param name="block">已入容器的定稿条目</param>
        private void EmitBlock(ViewBlock block)
        {
            Action<ViewBlock> handler = OnBlockAppended;
            if (handler != null)
            {
                handler(block);
            }
        }

        /// <summary>
        /// 时间戳单调化——同刻或回退由写入侧 +1 保证全序（A156 I5：前端可无脑按 ts 冒泡插入）。
        /// </summary>
        /// <param name="timestamp">原始时间戳（Unix 毫秒）</param>
        /// <returns>单调递增后的时间戳</returns>
        private long MonotonicTs(long timestamp)
        {
            if (timestamp <= _lastTs)
            {
                timestamp = _lastTs + 1;
            }
            _lastTs = timestamp;
            return timestamp;
        }

        /// <summary>
        /// 登记待配对工具——解析 tool_calls JSON 数组（{id,function:{name,arguments}}；解析失败空登记——容错）
        /// </summary>
        /// <param name="toolCallsJson">tool_calls JSON</param>
        /// <param name="declaredTs">声明时刻（Unix 毫秒——工具卡条目时间戳基准，A157）</param>
        private void RegisterPendingTools(string toolCallsJson, long declaredTs)
        {
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(toolCallsJson))
                {
                    JsonElement root = doc.RootElement;
                    if (root.ValueKind != JsonValueKind.Array)
                    {
                        return;
                    }
                    for (int i = 0; i < root.GetArrayLength(); i++)
                    {
                        JsonElement call = root[i];
                        string id = GetStringProp(call, "id");
                        string name = "";
                        string arguments = "";
                        JsonElement funcEl;
                        if (call.TryGetProperty("function", out funcEl))
                        {
                            name = GetStringProp(funcEl, "name");
                            arguments = GetStringProp(funcEl, "arguments");
                        }
                        PendingTool pt = new PendingTool();
                        pt.ToolCallId = id;
                        pt.Name = name;
                        pt.Arguments = arguments;
                        pt.Index = i + 1;
                        pt.Total = root.GetArrayLength();
                        pt.Ts = declaredTs;
                        _pendingTools.Add(pt);
                    }
                }
            }
            catch (Exception ex)
            {
                // 解析失败——空登记（容错）
                LogStore.Add("SessionViewStore", 2, "工具卡登记解析失败，按空登记: " + ex.Message, "SYS");
            }
        }

        /// <summary>
        /// 读取 JSON 对象字符串属性——防御式（缺字段返回空串）
        /// </summary>
        /// <param name="obj">JSON 对象</param>
        /// <param name="prop">属性名</param>
        /// <returns>属性值</returns>
        private static string GetStringProp(JsonElement obj, string prop)
        {
            JsonElement value;
            if (obj.TryGetProperty(prop, out value) && value.ValueKind == JsonValueKind.String)
            {
                string got = value.GetString();
                if (got != null)
                {
                    return got;
                }
            }
            return "";
        }

        /// <summary>
        /// 载入视图层——逐行读回 JSONL（唯一行型块行；注入报告为普通块）。
        /// 末行残缺丢弃、非末行坏行跳过（各自出声）；失败出声——视图层为权威，静默不可接受。
        /// </summary>
        public void Load()
        {
            if (!File.Exists(_path))
            {
                return;
            }
            string[] lines;
            try
            {
                lines = File.ReadAllLines(_path, Encoding.UTF8);
            }
            catch (Exception ex)
            {
                // A156：视图层为权威——载入失败出声（空视图继续 = 前端历史整体缺口）
                LogStore.Add("SessionViewStore", 3, "视图载入失败（按空视图继续——本会话历史将空白）: " + ex.Message, "SYS");
                return;
            }
            // [段1] 容器清零——载入即权威重建（不复用内存残留；注入报告为 _blocks 内普通块，随之一并清零）
            _blocks.Clear();
            _gapTexts.Clear();
            _roundSums.Clear();
            _errors.Clear();
            _retries.Clear();
            // [段2] 末条非空行定位——仅末行按残缺处置（末尾截断行：丢弃 + 出警）
            int lastIndex = -1;
            for (int i = lines.Length - 1; i >= 0; i = i - 1)
            {
                if (lines[i].Trim().Length > 0)
                {
                    lastIndex = i;
                    break;
                }
            }
            // [段3] 逐行解析——块行 / 坏行（非末行坏行跳过——不整文件作废）
            for (int i = 0; i < lines.Length; i = i + 1)
            {
                string line = lines[i].Trim();
                if (line.Length == 0)
                {
                    continue;
                }
                ViewBlock block = null;
                bool parsed = TryParseLine(line, out block);
                if (parsed && block != null)
                {
                    AddLoadedBlock(block);
                    continue;
                }
                if (i == lastIndex)
                {
                    LogStore.Add("SessionViewStore", 2, "视图末行残缺——该行丢弃（无块信息）", "SYS");
                }
                else
                {
                    LogStore.Add("SessionViewStore", 2, "视图第 " + (i + 1).ToString() + " 行损坏——跳过（其余照常加载）", "SYS");
                }
            }
            _lastTs = MaxTimestamp();
        }

        /// <summary>
        /// 全表条目最大时间戳——单调补差基准初始化（载入后调用）。
        /// </summary>
        /// <returns>最大时间戳（无条目 = 0）</returns>
        private long MaxTimestamp()
        {
            long max = 0;
            ViewBlock[] all = GetBlocks();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                if (all[i].Timestamp > max)
                {
                    max = all[i].Timestamp;
                }
            }
            return max;
        }

        /// <summary>
        /// 对账哨兵——抽样比对条目的前文来源与前文对应消息（A156：替代常驻重建作正确性保证；A165 v2：无哈希面 → 按索引存在性与角色一致性核对）。
        /// 不一致出声（L3）；不修改任何条目。
        /// </summary>
        /// <param name="messages">真实前文消息数组</param>
        /// <param name="sample">抽样条目数（0 或负 = 全量比对）</param>
        /// <returns>不一致条目数（0 = 全对）</returns>
        public int AuditEntries(LlmMessage[] messages, int sample)
        {
            int checkedCount = 0;
            int bad = 0;
            for (int i = 0; i < _blocks.Count; i = i + 1)
            {
                ViewBlock b = _blocks[i];
                if (b.MsgIndex < 0)
                {
                    continue;
                }
                if (sample > 0 && checkedCount >= sample)
                {
                    break;
                }
                checkedCount = checkedCount + 1;
                if (b.MsgIndex >= messages.Length)
                {
                    bad = bad + 1;
                    LogStore.Add("SessionViewStore", 3, "对账哨兵：条目来源索引越界（类型 " + (b.RenderType == null ? "" : b.RenderType) + " / 消息 " + b.MsgIndex.ToString() + " / 前文 " + messages.Length.ToString() + "）", "SYS");
                    continue;
                }
                if (!RoleMatches(b.RenderType, messages[b.MsgIndex]))
                {
                    bad = bad + 1;
                    LogStore.Add("SessionViewStore", 3, "对账哨兵：条目来源角色不符（类型 " + (b.RenderType == null ? "" : b.RenderType) + " / 消息 " + b.MsgIndex.ToString() + "）", "SYS");
                }
            }
            return bad;
        }

        /// <summary>
        /// 对账角色一致性判定——条目类型与前文消息角色是否相符（v2 无哈希面后的对账判据）。
        /// </summary>
        /// <param name="renderType">条目渲染类型</param>
        /// <param name="m">前文消息</param>
        /// <returns>true=相符</returns>
        private static bool RoleMatches(string renderType, LlmMessage m)
        {
            if (renderType == "user" || renderType == "systemautomessage")
            {
                return m.Role == LlmRole.User;
            }
            if (renderType == "text" || renderType == "reason")
            {
                return m.Role == LlmRole.Assistant;
            }
            if (renderType == "toolcard")
            {
                return m.Role == LlmRole.Tool;
            }
            return true;
        }
    }
}
