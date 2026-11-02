using System;
using System.Collections.Generic;
using CatHome4.Contracts;
using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// 会话视图出口——chat 快照协议 v2 出口（A165 阶段 1）。
    /// 三段结构：state（前端状态 · 后端权威业务态）/ persist（持久对话区 · 只增）/
    /// live（临时区 · **状态投影**——type + context 两个字符串，A196 起）。
    /// 三种传输模式：全量（连接建立与重置后重发）· 追加（新持久条目）· 变化增量（状态与临时区变化）。
    /// 🔴 临时区模型（A196）——唯一入口 <see cref="SetLive"/>，两个参数都是字符串，每次调用**整段覆盖**：
    /// type ∈ {thinksse, replysse, toolrun, empty}（白名单校验；非法值出声并按 empty 落）；
    /// context = 该 type 的当前整段内容（思考全文 / 回复全文 / 未完成工具卡数组 JSON / 空串）。
    /// 无表、无槽位键、无插入序、无增删、无配对——「销毁」= 下一次覆盖（写 empty 即清空）；
    /// 前端解析 type、把 context 交给对应渲染结构（契约 §12.5）。
    /// 「何时推、推给谁」归 HttpHost，「当前状态是什么」归本类；宿主推送面未附加时全部入口静默跳过。
    /// </summary>
    internal sealed class ViewBus
    {
        /// <summary>临时区 type 白名单——四值（A196：toolrun / thinksse / replysse / empty）</summary>
        private static readonly string[] LiveTypes = new string[] { "toolrun", "thinksse", "replysse", "empty" };

        /// <summary>宿主推送面——Bootstrap 段6 宿主 HTTP 启动后 Attach 赋值（构造时宿主 HTTP 未启动）</summary>
        private IHostPush _host;

        /// <summary>持久区全表——只增序列（**降级路径**：全量数据源未注入时使用；无移除面）</summary>
        private readonly List<ViewBlock> _persist = new List<ViewBlock>();

        /// <summary>
        /// 全量数据源——会话侧注入（视图存储的合成出口 GetBlocks）。
        /// 🔴 单一真相源：全量帧每次**现取**，不依赖任何「启动时同步」——视图存储是唯一权威，
        /// 本类不持副本（副本必然与权威分叉：载入 / 清空 / 回滚三处都是分叉点）。
        /// 未注入时回落 `_persist`（降级，仅供测试与未接线场景）。
        /// </summary>
        private Func<ViewBlock[]> _fullSource;

        /// <summary>待推追加——自上次取帧以来的新增持久条目（取走即清）</summary>
        private readonly List<ViewBlock> _pendingAppend = new List<ViewBlock>();

        /// <summary>全量待发标记——连接建立之外的重置（新会话 / 清空前文）置位，帧轮广播全量帧</summary>
        private bool _fullPending;

        /// <summary>临时区 type（A196——四值白名单之一；初始 empty）</summary>
        private string _liveType = "empty";

        /// <summary>临时区 context（A196——该 type 的当前整段内容；整段覆盖，不做增量与累积）</summary>
        private string _liveContext = "";

        /// <summary>临时区变化标记——自上次取帧以来内容有变化</summary>
        private bool _liveDirty;

        /// <summary>状态段 JSON——会话侧 SetState 供（后端权威业务态整段：轮阶段 / token / 前文长度与条数 / Note / 连接健康）</summary>
        private string _stateJson = "{}";

        /// <summary>状态段变化标记——整段比对，变化才推</summary>
        private bool _stateDirty;

        /// <summary>推送面就绪标志——宿主外观层已附加</summary>
        public bool Ready
        {
            get
            {
                return _host != null;
            }
        }
        /// <summary>
        /// 是否仍有待推的**内容**——只读探测（不消耗）：持久区追加 / 全量待发 / 临时区变更任一置位即 true。
        /// 🔴 不含状态段：state 段含运行时长 / 帧号等每帧变化的值，纳入即永久置位（闸门永不通过）。
        /// 消费面 = 重启闸门（A206：业务 Idle ≠ 推送面清空——在途帧随进程退出被丢弃）。
        /// 未接线（宿主推送面未附加）时恒 false——无推送面即无待推。
        /// </summary>
        public bool HasPendingContent
        {
            get
            {
                if (_host == null)
                {
                    return false;
                }
                return _fullPending || _pendingAppend.Count > 0 || _liveDirty;
            }
        }

        /// <summary>
        /// 附加宿主推送面——Bootstrap 段6 宿主 HTTP 启动后调用（SSE 转发面就位）。
        /// </summary>
        /// <param name="host">HTTP 外观层实例</param>
        public void Attach(IHostPush host)
        {
            _host = host;
        }

        /// <summary>
        /// 临时区重写（🔴 A196 唯一入口）——按主干当前状态决定 live 段的 type 与整段内容。
        /// type 白名单外一律出声并按 `empty` + 空 context 落（主干状态未知时的安全值）；
        /// 与当前值完全相同则零动作（帧轮据此产出零字节）。
        /// </summary>
        /// <param name="type">临时区类型（toolrun / thinksse / replysse / empty）</param>
        /// <param name="context">该 type 的当前整段内容（thinking / reply 传累计全文；toolrun 传未完成工具卡数组 JSON；empty 传空串）</param>
        public void SetLive(string type, string context)
        {
            if (_host == null)
            {
                return;
            }
            string liveType = type == null ? "" : type;
            bool known = false;
            for (int i = 0; i < LiveTypes.Length; i = i + 1)
            {
                if (LiveTypes[i] == liveType)
                {
                    known = true;
                    break;
                }
            }
            if (!known)
            {
                // 失败必须可见——非法 type 出声（不静默落值）；安全值 = 空态
                LogStore.Add("ViewBus", 2, "临时区 type 非法（按 empty 落）: " + (liveType.Length == 0 ? "(空)" : liveType), "SYS");
                liveType = "empty";
                context = "";
            }
            string liveContext = context == null ? "" : context;
            if (liveType == _liveType && liveContext == _liveContext)
            {
                return;
            }
            _liveType = liveType;
            _liveContext = liveContext;
            _liveDirty = true;
        }

        /// <summary>状态段更新——后端权威业务态整段（会话侧组装；与上帧相同则零动作）</summary>
        /// <param name="json">状态段 JSON（空 = 空对象）</param>
        public void SetState(string json)
        {
            string value = json == null || json.Length == 0 ? "{}" : json;
            if (value == _stateJson)
            {
                return;
            }
            _stateJson = value;
            _stateDirty = true;
        }

        /// <summary>全量帧——当前状态整段快照（连接建立首帧取一次；重置后由帧轮广播）</summary>
        /// <returns>载荷 JSON（{"v":2,"state":{…},"persist":{"mode":"full","items":[…]},"live":{"type":…,"context":…}}）</returns>
        public string BuildFull()
        {
            string persistJson = JsonUtil.Object(
                ("mode", "full"),
                ("items", JsonUtil.RawArray(PersistItems())));
            _pendingAppend.Clear();
            _liveDirty = false;
            _stateDirty = false;
            _fullPending = false;
            return JsonUtil.Object(
                ("v", 2),
                ("state", JsonUtil.Raw(StateJson())),
                ("persist", JsonUtil.Raw(persistJson)),
                ("live", JsonUtil.Raw(LiveJson())));
        }
        /// <summary>
        /// 待推标记复位——宿主推送面随猫停机失效时调用（A297）。
        /// 🔴 帧泵只在会话所属 HttpHost 的 PumpMainThread 里跑：宿主 Stop 后标记再无人取走，
        /// `HasPendingContent` 恒真 → 重启闸门 `AnyViewPending` 永久阻塞。
        /// 只清变更标记，不动内容（下次接新宿主由连接建立首帧 BuildFull 现取全量，不依赖本标记）。
        /// </summary>
        public void ResetPending()
        {
            _pendingAppend.Clear();
            _liveDirty = false;
            _stateDirty = false;
            _fullPending = false;
        }

        /// <summary>
        /// 取帧——帧轮唯一出口：无变化返回 false（零字节），有变化按内容组装（追加 / 状态 / 临时区可同帧承载）。
        /// 取走即清变更标记；全量待发优先（重置后整体重绘）。
        /// </summary>
        /// <param name="json">帧 JSON；无变化时为 null</param>
        /// <returns>true = 有帧可推</returns>
        public bool TryTakeFrame(out string json)
        {
            json = null;
            if (_host == null)
            {
                return false;
            }
            if (_fullPending)
            {
                _fullPending = false;
                json = BuildFull();
                return true;
            }
            if (_pendingAppend.Count == 0 && !_stateDirty && !_liveDirty)
            {
                return false;
            }
            List<string> parts = new List<string>();
            if (_pendingAppend.Count > 0)
            {
                ViewBlock[] appended = _pendingAppend.ToArray();
                _pendingAppend.Clear();
                string persistJson = JsonUtil.Object(
                    ("mode", "append"),
                    ("items", JsonUtil.RawArray(PersistItems(appended))));
                parts.Add("\"persist\":" + persistJson);
            }
            if (_stateDirty)
            {
                _stateDirty = false;
                parts.Add("\"state\":" + StateJson());
            }
            if (_liveDirty)
            {
                _liveDirty = false;
                parts.Add("\"live\":" + LiveJson());
            }
            json = "{" + string.Join(",", parts) + "}";
            return true;
        }

        /// <summary>
        /// 持久块记入——持久区建块即记（由 SessionViewStore 建块事件驱动，本类不建持久块）：只增，
        /// 记入追加队列（帧轮按「追加」模式推送）。
        /// 🔴 A196：工具卡两区换手（时间戳交接表 / 身份键直配）整体退役——临时区是状态投影，由主干按「未完成工具」重写。
        /// </summary>
        /// <param name="block">已定稿的持久块</param>
        public void PushPersist(ViewBlock block)
        {
            if (block == null || _host == null)
            {
                return;
            }
            _persist.Add(block);
            _pendingAppend.Add(block);
        }

        /// <summary>
        /// 注入全量数据源——会话侧接线（ChatSession.AttachHost）时调用一次。
        /// 全量帧（连接建立 / 重连 / 刷新）每次经此**现取**——不缓存、不同步、不分叉。
        /// </summary>
        /// <param name="source">全量块取数委托（视图存储合成出口）</param>
        public void AttachSource(Func<ViewBlock[]> source)
        {
            _fullSource = source;
        }

        /// <summary>
        /// 状态清空——新会话 / 清空前文：持久区与临时区全清并置全量待发标记（帧轮广播全量帧，前端整体重绘空态）。
        /// 持久区只增的例外仅此一处：会话重置即新会话，旧条目不再属于本会话。
        /// </summary>
        public void ResetAll()
        {
            _persist.Clear();
            _pendingAppend.Clear();
            _liveType = "empty";
            _liveContext = "";
            _fullPending = true;
        }

        /// <summary>状态段 JSON——空回落空对象（序列化面直嵌，不二次转义）</summary>
        /// <returns>状态段 JSON</returns>
        private string StateJson()
        {
            return _stateJson == null || _stateJson.Length == 0 ? "{}" : _stateJson;
        }

        /// <summary>临时区 JSON——{"type":…,"context":…}（A196 两个字符串，前端按 type 分发）</summary>
        /// <returns>临时区 JSON</returns>
        private string LiveJson()
        {
            return JsonUtil.Object(
                ("type", _liveType),
                ("context", _liveContext == null ? "" : _liveContext));
        }

        /// <summary>
        /// 全量块集——**单一真相源优先**：注入的全量数据源（视图存储合成出口）→ 未注入时回落内部累积（降级）。
        /// 排序在此完成（前端零排序——推送序即渲染序）。
        /// </summary>
        /// <returns>全量持久块（时间戳升序）</returns>
        private ViewBlock[] FullBlocks()
        {
            ViewBlock[] all = null;
            if (_fullSource != null)
            {
                all = _fullSource();
            }
            if (all == null)
            {
                all = _persist.ToArray();
            }
            List<ViewBlock> list = new List<ViewBlock>(all);
            list.Sort(delegate (ViewBlock a, ViewBlock b)
            {
                return a.Timestamp.CompareTo(b.Timestamp);
            });
            return list.ToArray();
        }

        /// <summary>持久条目片段数组——全量数据源（单一真相源取数 + 时间戳升序）</summary>
        /// <returns>条目 JSON 片段数组</returns>
        private string[] PersistItems()
        {
            return PersistItems(FullBlocks());
        }

        /// <summary>持久条目片段数组——指定块集（追加帧数据源，保持入队序）</summary>
        /// <param name="blocks">块数组</param>
        /// <returns>条目 JSON 片段数组</returns>
        private static string[] PersistItems(ViewBlock[] blocks)
        {
            string[] result = new string[blocks.Length];
            for (int i = 0; i < blocks.Length; i = i + 1)
            {
                result[i] = ItemJson(blocks[i]);
            }
            return result;
        }

        /// <summary>持久条目序列化——{type, ts, msgIndex, round, payload}（无同步标识面）</summary>
        /// <param name="block">持久块</param>
        /// <returns>条目 JSON</returns>
        private static string ItemJson(ViewBlock block)
        {
            string payload = block.Payload == null || block.Payload.Length == 0 ? "null" : block.Payload;
            return JsonUtil.Object(
                ("type", block.RenderType == null ? "" : block.RenderType),
                ("ts", block.Timestamp),
                ("msgIndex", block.MsgIndex),
                ("round", block.Round),
                ("payload", JsonUtil.Raw(payload)));
        }
    }
}
