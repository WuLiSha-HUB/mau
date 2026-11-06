using System.Collections.Generic;
using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// 视图卡载荷构造内核——同类视图卡的唯一拼装处。
    /// 部位：工具卡（toolcard）四处构造点收口——实时先行卡 / 中断终态补推 / A128 逐条回填 / 前文派生（事件直落）。
    /// 动机：同一语义两处实现必漏字段——判例 2026-10-02（前文派生路径漏 order → 前端执行序徽标消失）；
    /// 故「该写哪些字段」只允许一处说了算。纯函数、无宿主依赖——可直测。
    /// 扩展：其余同族载荷（user / text / reason 各两处）按同一模式逐步收口。
    /// </summary>
    internal static class ViewCardPayload
    {
        /// <summary>
        /// 工具卡载荷——name / arguments / result / toolIndex / toolTotal / order 六字段的唯一拼装处。
        /// result 传 null = 先行「进行中」卡（不写 result 字段，前端按处理中渲染）；
        /// 传空串 = 合法空结果（写字段）——两者语义不同，不可合并。
        /// </summary>
        /// <param name="name">工具名</param>
        /// <param name="arguments">参数 JSON（原样；先行卡侧已按需注入 catId）</param>
        /// <param name="result">结果文本（null = 先行卡）</param>
        /// <param name="index">并发序号（1-based）</param>
        /// <param name="total">并发总数</param>
        /// <param name="durMs">运行时长（毫秒；-1 = 未记录 / 不适用——进载荷供前端显示）</param>
        /// <param name="queueMs">排队时长——派发 → 认领（毫秒；-1 = 未观测，前端不显示拆分）</param>
        /// <param name="runMs">执行时长——认领 → 闭合（毫秒；-1 = 未记录，前端不显示拆分）</param>
        /// <returns>工具卡载荷字典</returns>
        public static Dictionary<string, object> BuildToolCard(string name, string arguments, string result, int index, int total, long durMs = -1, long queueMs = -1, long runMs = -1)
        {
            Dictionary<string, object> payload = new Dictionary<string, object>();
            payload["name"] = name;
            payload["arguments"] = arguments;
            if (result != null)
            {
                payload["result"] = result;
            }
            payload["toolIndex"] = index;
            payload["toolTotal"] = total;
            payload["order"] = ToolOrderTable.OrderText(name);
            payload["durMs"] = durMs;
            // 两段计时（排队 / 执行）——durMs 仍是总时长（向后兼容）；两值 -1 = 未观测，前端不显示拆分
            payload["queueMs"] = queueMs;
            payload["runMs"] = runMs;
            return payload;
        }
        /// <summary>
        /// 思考块载荷——text / durMs / chars / cps 四字段的唯一拼装处（两处构造点：纯文本轮 / 工具轮）。
        /// 三项计数为后端规整化产出：chars = 正文长度；cps = 字符数 ÷ 思考用时（用时未记录或为 0 时该比值不成立，取 -1）。
        /// </summary>
        /// <param name="text">思考正文</param>
        /// <param name="durMs">思考用时（毫秒；-1 = 未记录——载入顶尾补差等无实时数据路径）</param>
        /// <returns>思考块载荷字典</returns>
        public static Dictionary<string, object> BuildReason(string text, long durMs = -1)
        {
            string body = text;
            if (body == null)
            {
                body = "";
            }
            Dictionary<string, object> payload = new Dictionary<string, object>();
            payload["text"] = body;
            payload["durMs"] = durMs;
            payload["chars"] = body.Length;
            double cps = -1;
            if (durMs > 0)
            {
                cps = System.Math.Round(body.Length * 1000.0 / durMs, 1);
            }
            payload["cps"] = cps;
            return payload;
        }
    }
}
