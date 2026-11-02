using System.Text;

namespace CH4
{
    /// <summary>
    /// timeback 域规范卡——域内规范与回执骨架的**告知面**（design-ch4-timeback-type §十一 · 2026-10-09 莎裁）。
    /// 形态：一条 systemauto user 消息，开域后由批后段注入；**落在作用域区间内 → 随区间回收一并消失**。
    /// 动机：规范原先住在 start 结果里（工具结果通道 + 每域重复一份）→ 同形模板随会话堆积，
    ///   模型对「哪份是活跃的」无语义锚点 → 倾向直接补一份同形 close（判例见 §十一 · `_log` 批 4）。
    /// 分工：本件是**骨架文本的唯一承载处**——start 结果只留结构化头（§十一 · §四 载体注）。
    /// </summary>
    internal static class TimebackCard
    {
        /// <summary>建议长度（前文条目）——五 type 统一值；建议非闸门，待归档统计校准（§十一）。</summary>
        public const int SuggestedLength = 100;

        /// <summary>域内不可用摘要——锁定族 · 系统信息类 · 共同禁面（§二 共同禁面表的口径摘写）。</summary>
        private const string LockedText = "Note · sleep · timer（域内锁定）｜系统信息类 info / host-flows｜"
            + "powershell / temp-exec / file-move·file-delete·file-copy / config-* / mau-gen·mau-proj·mau-setup / host-reload / restart-*";

        /// <summary>
        /// 本域边界——一句话说明这个域只做什么（§十一 卡草案 · 四 type 各一份）。
        /// </summary>
        /// <param name="type">域类型（`TimebackProfile` 枚举）</param>
        /// <returns>边界说明（未登记类型给占位句）</returns>
        public static string Boundary(string type)
        {
            if (type == TimebackProfile.BrowserVision)
            {
                return "网页取证——网页原文与截图留在域内，主干只接结论与位置";
            }
            if (type == TimebackProfile.ImageVision)
            {
                return "看图取证——原图随域回收，主干只接 ASCII 图证与结论";
            }
            if (type == TimebackProfile.TextSearch)
            {
                return "文本 / 文档检索——查证体量留在域内，主干只接结论与位置";
            }
            if (type == TimebackProfile.CodeReview)
            {
                return "只读取证——读码 / 查引用 / 查死码；不含编译与跑测（那属 code_write）";
            }
            if (type == TimebackProfile.CodeWrite)
            {
                return "实现域——读 / 改 / 编译 / 跑测一域内完成；遇计划外问题先 back 回主干";
            }
            return "（未登记边界的域类型）";
        }

        /// <summary>
        /// 域内可用工具摘要——**由 `TimebackProfile` 派生**（白名单唯一真相源；手写副本必然漂移）。
        /// </summary>
        /// <param name="type">域类型</param>
        /// <returns>以「 · 」分隔的工具名清单</returns>
        public static string AvailableText(string type)
        {
            string[] names = TimebackProfile.Tools(type);
            StringBuilder text = new StringBuilder();
            for (int i = 0; i < names.Length; i = i + 1)
            {
                if (i > 0)
                {
                    text.Append(" · ");
                }
                text.Append(names[i]);
            }
            return text.ToString();
        }

        /// <summary>
        /// findings 骨架——按域类型类别分派（原 `ChatSession.FindingsSkeletonFor` 迁入 · §十一 单一承载处）。
        /// **审查类**（browser_vision / text_search / code_review）给「成果位置」——主干按位置回读；
        /// **实现类**（code_write）给「变更 + 可复跑跑测」——主干按位置回读改动、照命令复跑，`变更` 段须与宿主工具台账对得上；
        /// **识图类**（image_vision）给「ASCII 图证 + 结论」——图无法按位置回读（再看一眼 = 再开一域），证据只能随回执带回。
        /// </summary>
        /// <param name="type">域类型（TimebackProfile 枚举）</param>
        /// <returns>骨架文本（含引导句 / 段定义 / 硬约束句）</returns>
        public static string Findings(string type)
        {
            if (TimebackProfile.Kind(type) == TimebackProfile.KindImage)
            {
                return "findings 写「看到了什么 + 证据在哪」——图不随回执回来，证据必须写进正文（段内无内容写「（无）」）：\n"
                    + "  结论：<一句话答主干的问题>\n"
                    + "  图证：<ASCII 图 · ≤24 行 × ≤80 列 · 只画判定所需区域 · 字符 + - | / \\ . # @>\n"
                    + "  证据位置：<原图路径或 URL + 图中关键区域（如「左下状态条」）>\n"
                    + "  未竟：<没看清 / 没覆盖的>\n"
                    + "  卡点与失败：<卡在哪 · 失败原因>\n"
                    + "每条 ≤1 行 · 每段 ≤5 条；`图证` 只承载布局 / 位置 / 状态，颜色 / 字号 / 纹理用一行文字补，不拿 ASCII 硬画。\n"
                    + "🔴 结论必须被 `图证` 或 `证据位置` 支撑——只有结论没有证据 = 必红；图证与结论对不上时先回看原图再写。\n"
                    + "例：结论：工具栏贴顶、状态条在左下 · 图证：+------+/| tool |/… · 证据位置：data:browser/<cat>/out/a.png（左下角）";
            }
            if (TimebackProfile.Kind(type) == TimebackProfile.KindWrite)
            {
                return "findings 写「改了什么 + 怎么复验」——主干按位置回读改动、照命令复跑（段内无内容写「（无）」）：\n"
                    + "  变更：<逐条：文件:行区间 — 改了什么>\n"
                    + "  跑测：<可复制命令> → 期望 <判据>\n"
                    + "  未竟：<没做完的>\n"
                    + "  卡点与解法：<卡在哪 · 怎么绕过去——为什么这么改只在这里留得下>\n"
                    + "  失败：<失败原因>\n"
                    + "每条 ≤1 行 · 每段 ≤5 条；位置与命令必须是这趟真改过 / 真跑过的——没跑的、凭印象复述的一律不写。\n"
                    + "🔴 每行只写下方台账里有对应调用的——`变更` 段逐条对台账里的写操作条目：台账有而未提 = 漏报；提了台账没有 = 幻觉；台账为空而 findings 非空 = 必红。\n"
                    + "例：变更：mau:CatHome4/CatHome4.Core/TimebackProfile.cs:120-140 — 新增白名单对账出口；跑测：dotnet test <csproj> --filter <名> → 期望 12 绿";
            }
            return "findings 只写「成果在哪」，不写「结论是什么」——域内不下判断；主干按位置回读，以回读到的真实内容为准（段内无内容写「（无）」）：\n"
                + "  成果：<逐条：位置（文件:行区间 / URL / 截图路径）+ 一句话说那里是什么——主干据此回读>\n"
                + "  未竟：<没查完 / 没覆盖的>\n"
                + "  卡点：<被什么挡住 / 读不通的地方>\n"
                + "  失败：<失败原因>\n"
                + "每条 ≤1 行 · 每段 ≤5 条；位置必须是这趟真实读到 / 打开过的——没读到的、凭印象复述的一律不写。\n"
                + "🔴 每行只写下方台账里有对应调用的——写完自问「这行来自哪次调用」，答不出即删；台账为空而 findings 非空 = 必红。\n"
                + "例：成果：mau:CatHome4/Program.Tools.cs 的 timeback 描述块——findings 参数说明所在行（行号以你实读到的为准）";
        }
        /// <summary>
        /// 收域后回读规范——附于 back 返回体的 findings **之后**（载体注 §十一 同源 · 2026-10-10 莎裁止血）。
        /// 动机：findings 是域内自述，主干须按位置回读实况才算收域完成；规范原先只在开域的域规范卡里
        /// （回收时刻早已离开注意力）→ 在**动作点**再挂一条，令不变量与动作同处一屏。
        /// </summary>
        /// <param name="type">域类型（TimebackProfile 枚举）</param>
        /// <returns>回读规范文本</returns>
        public static string Readback(string type)
        {
            if (TimebackProfile.Kind(type) == TimebackProfile.KindImage)
            {
                return "🔴 回读规范（收域第一步）——findings 是自述不是实况：核 `图证` 是否真的支撑 `结论`；要再亲眼看一眼原图 → 另开一个识图域，别拿结论当实况";
            }
            if (TimebackProfile.Kind(type) == TimebackProfile.KindWrite)
            {
                return "🔴 回读规范（收域第一步）——findings 是自述不是实况：按 `变更` 的位置逐条读回改动面";
            }
            return "🔴 回读规范（收域第一步）——findings 是自述不是实况：按 `成果` 的位置逐条读回原文";
        }

        /// <summary>
        /// 浏览器操作指引——仅 `browser_vision` 域追加（§十一 卡扩展 · 2026-10-10）。
        /// 承载时机敏感、易踩的操作面（寻路 / 取值 / 截图落点 / 看图分流 / 扒图链 / 边界）。
        /// </summary>
        private const string BrowserGuideText = "⑥ 浏览器操作（本域专属）：\n"
            + "  · 寻路：open 只开页，不能点击/输入——翻页改 URL，或先 read(mode=links) 拿候选\n"
            + "  · 读页：read(mode=text|ax|links) 落盘；DOM 属性 / 内嵌数据 / img 直链一律用 eval 取\n"
            + "  · 截图：shot(full) 落盘返回路径——存 data:browser/<cat>/out/（tree 自 data:browser 下扫会漏 out，给全路径才有）\n"
            + "  · 页签：tabs 保留来过哪一页，可回退对比\n"
            + "  · 流程：开列表页（query 拼进 URL 直开）→ 取下一跳 → 逐个开详情（截图 / 取值）→ 回卷\n"
            + "  · 看图：本域**不含识图**——截图经 back 的「成果」带回路径，要「看见」另开 image_vision 域\n"
            + "  · 扒图：eval 取 img 直链 → 出域后 powershell 落盘 → 图片包裹回主干\n"
            + "  · 边界：原图直链常 403（CDN 缩略图可取）；未登录态不影响截图；网络可达性因机而异";

        /// <summary>
        /// 看图操作指引——仅 `image_vision` 域追加（§十一 卡扩展 · A293 2026-11-02）。
        /// 承载识图域的三件易错面：直读 vs OCR 分流 / ASCII 图证规格 / 多图与证据位置。
        /// </summary>
        private const string ImageGuideText = "⑦ 看图操作（本域专属）：\n"
            + "  · 分流：要「亲眼看」用 image-inject（原图进前文，随域回收）；只要文字（OCR / 逐字转录）用 image-analyze\n"
            + "  · 图证：findings 的 `图证` 用 ASCII 画判定所需区域——≤24 行 × ≤80 列，字符 + - | / \\ . # @\n"
            + "  · 只画结构：布局 / 位置 / 状态用 ASCII；颜色 / 字号 / 纹理写一行文字，别拿字符硬凑\n"
            + "  · 多图：一域内可连插多图对比，findings 每条对应一张，`证据位置` 写清是哪张的哪个区域\n"
            + "  · 落点：本地路径 / http(s) URL 都吃；截图路径直接来自上一域 `成果`（data:browser/<cat>/out/——给全路径）\n"
            + "  · 边界：单图 ≤32MiB；判断不了就说判断不了（不做模糊描述），需要更大信息量另开一域";

        /// <summary>
        /// 域规范卡全文——注入用的 systemauto user 文本（五段：边界 / 可用 / 不可用 / 骨架 / 建议长度；`browser_vision` 追加第⑥段、`image_vision` 追加第⑦段）。
        /// </summary>
        /// <param name="type">域类型</param>
        /// <param name="purpose">用途标签</param>
        /// <param name="id">作用域编号</param>
        /// <param name="anchor">锚点（start 声明消息索引）</param>
        /// <returns>卡文本（单条 user 消息载荷）</returns>
        public static string Card(string type, string purpose, long id, int anchor)
        {
            StringBuilder text = new StringBuilder();
            text.Append("（系统自动 · timeback #").Append(id.ToString()).Append(" 域规范）");
            text.Append("域已开：").Append(type).Append(" · 用途「").Append(purpose).Append("」· 锚点 ").Append(anchor.ToString());
            text.Append("\n\n① 本域边界：").Append(Boundary(type));
            text.Append("\n② 域内可用：").Append(AvailableText(type));
            text.Append("\n③ 域内不可用：").Append(LockedText);
            text.Append("\n④ 回执骨架（back 的 findings 按此写）：\n").Append(Findings(type));
            text.Append("\n⑤ 建议长度：本域建议 ≤").Append(SuggestedLength.ToString()).Append(" 条前文条目——超出先阶段性 back（建议非闸门）");
            if (type == TimebackProfile.BrowserVision)
            {
                text.Append("\n\n").Append(BrowserGuideText);
            }
            if (type == TimebackProfile.ImageVision)
            {
                text.Append("\n\n").Append(ImageGuideText);
            }
            return text.ToString();
        }
    }
}
