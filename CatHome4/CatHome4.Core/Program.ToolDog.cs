using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// 工具单 Dog——单步工单载体（CH2 CH_Dog_TextMover 同构移植——P8 二期 OA 走单）。
    /// 不 Post、不处理业务——发起者（宿主 ExecuteToolBatch）负责 Post 并注入 OfficeId；本实体只轮询 OA 等待结果。
    /// 多级 OA 单预留：Relist 后单回 Open 继续流转，Dog 只认 Closed/TimeOut 终态——对链路无感（多级中间结果传递 CH2 亦未做）。
    /// </summary>
    internal sealed class ToolOrderDog
    {
        /// <summary>
        /// tool_call_id——结果配对键（回传 LLM）
        /// </summary>
        public string ToolCallId;

        /// <summary>
        /// 工具名（officeName——执行 Cat 按名认领）
        /// </summary>
        public string Name;

        /// <summary>
        /// 参数整包 JSON——OA 载荷 args key（CH2 验证格式）
        /// </summary>
        public string ArgsJson;

        /// <summary>
        /// Office ID（Post 后有效；0=Post 失败）
        /// </summary>
        public long OfficeId;

        /// <summary>
        /// 是否已 Closed——回执已收集
        /// </summary>
        public bool IsClosed;

        /// <summary>
        /// 是否已 TimeOut——工单超时判定（超时→诚实 ERR 报错，不再直执）
        /// </summary>
        public bool IsTimedOut;

        /// <summary>
        /// 回执结果文本（Closed 后有效）
        /// </summary>
        public string Result;

        /// <summary>
        /// 工单超时帧数（按工具映射——CH2 MapToolTimeoutFrames 同构）
        /// </summary>
        public long TimeoutFrames;

        /// <summary>派发时刻（Unix 毫秒）——本单进入派发的墙钟时刻（A157：工具卡运行时长起点；0 = 未派发）。</summary>
        public long StartedAtMs;

        /// <summary>闭合时刻（Unix 毫秒）——本单回执 / 直执完成的墙钟时刻（工具卡运行时长终点·单工具口径；0 = 未闭合——回落采样时刻）。</summary>
        public long ClosedAtMs;

        /// <summary>
        /// 执行序值——工具分批调度的分桶依据（A127；宿主内部静态表裁决，非 LLM 传参）。
        /// 缺省 0 = 默认档；timeback-start / timeback-back 各取 ±100 钉死值。
        /// </summary>
        public int Order;

        /// <summary>
        /// 创建工具单 Dog——超时按工具名映射
        /// </summary>
        /// <param name="toolCallId">tool_call_id</param>
        /// <param name="name">工具名</param>
        /// <param name="argsJson">参数整包 JSON</param>
        public ToolOrderDog(string toolCallId, string name, string argsJson)
        {
            ToolCallId = toolCallId;
            Name = name;
            ArgsJson = argsJson;
            OfficeId = 0;
            IsClosed = false;
            IsTimedOut = false;
            Result = "";
            StartedAtMs = 0;
            Order = ToolOrderTable.OrderDefault;
            TimeoutFrames = MapTimeoutFrames(name);
        }

        /// <summary>
        /// 挂单——Post + 写载荷（owner 侧；载荷 args = 整包参数 JSON——CH2 验证格式）
        /// </summary>
        /// <param name="oa">OA 平台</param>
        /// <param name="ownerId">挂单方 ID（宿主 Dog 域）</param>
        public void Post(IOA oa, long ownerId)
        {
            OfficeId = oa.Post(ownerId, "TOOL", Name, TimeoutFrames);
            if (OfficeId > 0)
            {
                oa.SetStr(OfficeId, ownerId, "args", ArgsJson);
            }
        }

        /// <summary>
        /// 每帧轮询——Closed 收集回执 / TimeOut 置标志（CH_Dog_TextMover.Tick 同构）
        /// </summary>
        /// <param name="oa">OA 平台</param>
        public void Tick(IOA oa)
        {
            if (OfficeId <= 0 || IsClosed || IsTimedOut)
            {
                return;
            }
            OfficeState state = oa.GetStatus(OfficeId);
            if (state == OfficeState.Closed)
            {
                // 回执 = Office.Result 载荷（complete_str 写 "result" key——与 oa.get_result_str 积木同源）
                Office office = oa.GetOffice(OfficeId);
                string value;
                if (office.Result.Strs.TryGetValue("result", out value))
                {
                    Result = value;
                }
                else
                {
                    Result = "ERR|EMPTY_RESULT|工单回执无 result";
                }
                IsClosed = true;
                // 单工具计时终点——OA 回执到达帧置闭合时刻（工具卡运行时长按各自回执计）
                ClosedAtMs = System.DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
            }
            else if (state == OfficeState.TimeOut)
            {
                IsTimedOut = true;
            }
        }
        /// <summary>
        /// 运行时长——派发时刻 → 闭合时刻（毫秒·单工具口径：按各自回执计）；未闭合回落采样时刻。未派发（StartedAtMs = 0）返回 -1（未记录，不造值）。
        /// </summary>
        /// <returns>运行时长毫秒（-1 = 未记录）</returns>
        public long ElapsedMs()
        {
            if (StartedAtMs <= 0)
            {
                return -1;
            }
            long endMs = ClosedAtMs;
            if (endMs <= 0)
            {
                endMs = System.DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
            }
            long span = endMs - StartedAtMs;
            if (span < 0)
            {
                return -1;
            }
            return span;
        }

        /// <summary>
        /// 按工具名映射超时帧数——1200 帧 = 120 秒（100ms/帧，Program.FrameTargetMs）；与语料侧 [t=1200] 同口径
        /// </summary>
        /// <param name="name">工具名</param>
        /// <returns>超时帧数</returns>
        public static long MapTimeoutFrames(string name)
        {
            // 2026-11-04（莎拍板）：帧率 50→100ms 后未等比收缩帧数，导致 2400 帧实际等成 240 秒——补正为 1200 帧（120 秒）
            // 历史分级（mau-gen 120s / mau-proj 240s / mau-setup 450s / cs-build 120s / 默认 30s）为估值——实测无超长任务，统一收口
            return 1200;
        }
    }
}