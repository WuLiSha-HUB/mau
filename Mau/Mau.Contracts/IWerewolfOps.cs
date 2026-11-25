namespace Mau.Contracts
{
    /// <summary>
    /// 狼人杀本体对接面（A409）——工具组 WwCat 的执行面契约：把猫的动作转到本体，回执带回裁剪视图。
    /// 身份由宿主注入（catId）——🔴 本组零配置项：出站直接把 catId 当凭证打给本体，由本体自行匹配座位。
    /// CH4 侧不存座位表、不存本体 Key；工具参数面禁出现 key / seat / role / server（猫永远不知道自己是哪个 Key）。
    /// 契约面只用 BCL 类型（同 IQQBotOps 口径）。
    /// </summary>
    public interface IWerewolfOps
    {
        /// <summary>
        /// 当前牌面——GET /api/v1/state，按调用者座位裁剪。
        /// 成功返回本体视图 JSON（含 phase / turn / me / alivePlayers / legalActions / privateKnowledge?）；
        /// 失败以 ERR| 开头（配置缺失 / 不可达 / 本体错误码直通）。
        /// </summary>
        /// <param name="catId">调用者猫 key（宿主注入保留键）</param>
        /// <returns>视图 JSON 文本或 ERR| 文本</returns>
        string State(string catId);

        /// <summary>
        /// 我是谁——GET /api/v1/whoami（局号 / 座位 / 角色 / 存活；不含 Key）。
        /// </summary>
        /// <param name="catId">调用者猫 key（宿主注入保留键）</param>
        /// <returns>视图 JSON 文本或 ERR| 文本</returns>
        string WhoAmI(string catId);

        /// <summary>
        /// 增量事件——GET /api/v1/events?after=，已按调用者视角裁剪。
        /// </summary>
        /// <param name="catId">调用者猫 key（宿主注入保留键）</param>
        /// <param name="after">起始序号（不含；负值归零）</param>
        /// <returns>事件数组 JSON 文本或 ERR| 文本</returns>
        string Events(string catId, int after);

        /// <summary>
        /// 提交一个动作——POST /api/v1/action，合法性由本体裁决（工具零业务判断）。
        /// 成功返回本体受理回执 JSON；失败以 ERR| 开头（本体错误码直通，不吞）。
        /// </summary>
        /// <param name="catId">调用者猫 key（宿主注入保留键）</param>
        /// <param name="action">动作名（speak / vote / abstain / lastWords / wolfChat / wolfKill / seerCheck / witchSave / witchPoison / witchNone）</param>
        /// <param name="targetSeat">目标座位号文本（空 = 不带 targetSeat 字段）</param>
        /// <param name="text">正文（空 = 不带 text 字段）</param>
        /// <returns>受理回执 JSON 文本或 ERR| 文本</returns>
        string Act(string catId, string action, string targetSeat, string text);
    }
}
