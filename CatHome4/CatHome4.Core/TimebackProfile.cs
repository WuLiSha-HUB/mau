using System;
using System.Collections.Generic;

namespace CH4
{
    /// <summary>
    /// timeback 域类型档案——`type` → 域内可用工具白名单（fail-closed · design-ch4-timeback-type §二 / §三）。
    /// 语义：域开着时只有本 type 名单内的工具可用；名单外一律拒（`ERR｜TIMEBACK_PROFILE`，不静默）。
    /// 与「域外专属集」（`image-inject` / `browser-*` 域外不可用——`IsTimebackScopedTool`）是**两个正交维度**：
    ///   本表只决定「域内给不给」；域外专属集决定「域外给不给」。两者不可合并（合并会让 text-read 变成域内专属）。
    /// 会话授权面（每猫工具面 M2）之外的工具**不做先验**——授权面自会拒绝，本表只判「是否属于本 type」。
    /// 不进域：系统信息类（`info` / `host-flows`）——域是"做事的地方"，不是"问系统状态的地方"（莎 2026-10-09）。
    /// 维护：名单增删即改本文件；拼写漂移由 `Unknown()` 对账（宿主启动记账 + 测试）。
    /// </summary>
    public static class TimebackProfile
    {
        /// <summary>域类型·浏览器——浏览网页取证（页面原文 / 截图 / 取 DOM / 页签）；名单含浏览器族 → 开锚按需预热浏览器实例。识图不在本域（见 <see cref="ImageVision"/>）。</summary>
        public const string BrowserVision = "browser_vision";

        /// <summary>域类型·识图——直读原图取证（GUI 形态 / 图中逻辑）；回执给 ASCII 图证 + 结论，原图不常驻主干。</summary>
        public const string ImageVision = "image_vision";

        /// <summary>域类型·文本检索——文本 / 文档只读检索。</summary>
        public const string TextSearch = "text_search";

        /// <summary>域类型·代码审查——基础读面 + cs 只读面（纯取证：不含跑测面）。</summary>
        public const string CodeReview = "code_review";

        /// <summary>域类型·代码实现——基础读面 + cs 全套十三件 + 文本写面 + 语料检查（域内完成读 / 改 / 编译 / 跑测）。</summary>
        public const string CodeWrite = "code_write";

        /// <summary>文本读面——被多个 type 复用（域内只读取证：文本检索 + 内容检索）。</summary>
        private static readonly string[] TextReadNames =
        {
            "text-read", "text-read_lines", "text-read_between", "text-grep"
        };

        /// <summary>文件结构面——目录 / 文件名 / 产物版本（域内只读取证）。</summary>
        private static readonly string[] FileShapeNames =
        {
            "file-tree", "file-find", "file-version"
        };

        /// <summary>基础读面 = 文本读面 + 文件结构面——只读类 type 的共同底座。</summary>
        private static readonly string[] ReadBaseNames = Concat(TextReadNames, FileShapeNames);

        /// <summary>browser_vision 名单——浏览器族五件 + 基础读面（识图已移出本域，见 ImageVisionNames）。</summary>
        private static readonly string[] BrowserVisionNames = Concat(new string[]
        {
            "browser-open", "browser-read", "browser-eval", "browser-shot", "browser-tabs"
        }, ReadBaseNames);

        /// <summary>image_vision 名单——识图两件 + 基础读面（要「看见」用 image-inject；只要文字才 image-analyze）。</summary>
        private static readonly string[] ImageVisionNames = Concat(new string[]
        {
            "image-inject", "image-analyze"
        }, ReadBaseNames);

        /// <summary>text_search 名单——基础读面。</summary>
        private static readonly string[] TextSearchNames = ReadBaseNames;

        /// <summary>code_review 名单——基础读面 + cs 只读七件（纯取证：读码 / 查引用 / 查死码，不含任何跑测面）。</summary>
        private static readonly string[] CodeReviewNames = Concat(ReadBaseNames, new string[]
        {
            "cs-check", "cs-list", "cs-read", "cs-find", "cs-find_ref", "cs-dead", "cs-comment_check"
        });

        /// <summary>code_write 名单——基础读面 + cs 全套十三件 + 文本写面 + 语料检查。
        /// 域内一次完成「读 → 改 → 编译 → 跑测」：编译输出 / 跑测流水 / 诊断清单一律随域回收，不持久化进主干
        /// （`cs-test` 在名单内——实现域自带验证环；未通过的读数经台账 `FAIL` 态回报，不记 OK）。</summary>
        private static readonly string[] CodeWriteNames = Concat(new string[]
        {
            "cs-check", "cs-list", "cs-read", "cs-patch", "cs-member", "cs-comment",
            "cs-find", "cs-find_ref", "cs-dead", "cs-comment_check", "cs-format", "cs-build", "cs-test",
            "text-replace", "text-write", "text-append",
            "mau-verify"
        }, ReadBaseNames);

        /// <summary>
        /// 全部域类型——枚举顺序即 start 回执的枚举展示序（渐进扩展唯一入口）。
        /// </summary>
        /// <returns>类型名数组</returns>
        public static string[] Types()
        {
            return new string[] { BrowserVision, ImageVision, TextSearch, CodeReview, CodeWrite };
        }

        /// <summary>回执类别·审查类——findings 给「位置」清单（主干按位置回读）。</summary>
        public const string KindReview = "review";

        /// <summary>回执类别·实现类——findings 给「变更 + 可复跑跑测」（主干按位置回读 + 照命令复跑）。</summary>
        public const string KindWrite = "write";

        /// <summary>回执类别·识图类——findings 给「ASCII 图证 + 结论」（图无法按位置回读：证据必须随回执带回主干）。</summary>
        public const string KindImage = "image";

        /// <summary>
        /// 域类型的回执类别——决定 findings 骨架形态（design-ch4-timeback-type §四）。
        /// 审查类给「成果位置」（读到的才算数）；实现类给「变更 + 跑测」（改过的、跑过的才算数）。
        /// </summary>
        /// <param name="type">域类型</param>
        /// <returns>类别（`KindReview` / `KindWrite`）</returns>
        public static string Kind(string type)
        {
            if (type == CodeWrite)
            {
                return KindWrite;
            }
            if (type == ImageVision)
            {
                return KindImage;
            }
            return KindReview;
        }

        /// <summary>
        /// 类型合法性——开锚参数面校验（非法即拒开锚，附合法枚举）。
        /// </summary>
        /// <param name="type">类型名</param>
        /// <returns>true=合法</returns>
        public static bool IsValid(string type)
        {
            if (type == null || type.Length == 0)
            {
                return false;
            }
            return Contains(Types(), type);
        }

        /// <summary>
        /// 类型 → 域内可用工具名单（未知名 = 空数组）。
        /// </summary>
        /// <param name="type">类型名</param>
        /// <returns>工具名数组（引用直返——调用方只读）</returns>
        public static string[] Tools(string type)
        {
            if (type == BrowserVision)
            {
                return BrowserVisionNames;
            }
            if (type == ImageVision)
            {
                return ImageVisionNames;
            }
            if (type == TextSearch)
            {
                return TextSearchNames;
            }
            if (type == CodeReview)
            {
                return CodeReviewNames;
            }
            if (type == CodeWrite)
            {
                return CodeWriteNames;
            }
            return new string[0];
        }

        /// <summary>
        /// 域内可用性判定——域开着时该工具是否属于本 type 白名单（fail-closed）。
        /// 注意：timeback 两件自身不由本表管辖（域机制自用），例外在调用点处理。
        /// </summary>
        /// <param name="type">域类型</param>
        /// <param name="toolName">工具名</param>
        /// <returns>true=本 type 域内可用</returns>
        public static bool Allows(string type, string toolName)
        {
            if (toolName == null || toolName.Length == 0)
            {
                return false;
            }
            return Contains(Tools(type), toolName);
        }

        /// <summary>
        /// 浏览器预热判据——名单含浏览器族才预热实例（免"每次开域起一个浏览器后台"）。
        /// </summary>
        /// <param name="type">域类型</param>
        /// <returns>true=需要预热</returns>
        public static bool NeedsBrowser(string type)
        {
            return type == BrowserVision;
        }

        /// <summary>
        /// 类型枚举展示文本——参数错误与回执共用出口。
        /// </summary>
        /// <returns>以「 | 」分隔的类型清单</returns>
        public static string TypeListText()
        {
            return string.Join(" | ", Types());
        }

        /// <summary>
        /// 对账——白名单内不在工具池的名字（拼写漂移 / 工具退役后漏改）。
        /// 调用面：宿主启动装配记账（与执行序登记同族）+ 测试。
        /// </summary>
        /// <param name="poolNames">工具池全量名</param>
        /// <returns>缺失名数组（顺序 = 名单序；空 = 全部命中）</returns>
        public static string[] Unknown(string[] poolNames)
        {
            List<string> missing = new List<string>();
            string[] types = Types();
            for (int t = 0; t < types.Length; t = t + 1)
            {
                string[] names = Tools(types[t]);
                for (int i = 0; i < names.Length; i = i + 1)
                {
                    if (!Contains(poolNames, names[i]))
                    {
                        if (!missing.Contains(names[i]))
                        {
                            missing.Add(names[i]);
                        }
                    }
                }
            }
            return missing.ToArray();
        }

        /// <summary>
        /// 拼合两个名单——基础读面复用（避免同一串名字多处手写漂移）。
        /// </summary>
        /// <param name="first">前段</param>
        /// <param name="second">后段</param>
        /// <returns>拼合结果</returns>
        private static string[] Concat(string[] first, string[] second)
        {
            string[] all = new string[first.Length + second.Length];
            for (int i = 0; i < first.Length; i = i + 1)
            {
                all[i] = first[i];
            }
            for (int i = 0; i < second.Length; i = i + 1)
            {
                all[first.Length + i] = second[i];
            }
            return all;
        }

        /// <summary>
        /// 名单线性查找——名单规模小（＜20），线性查找免建索引（勿增实体）。
        /// </summary>
        /// <param name="names">名单</param>
        /// <param name="name">待查名</param>
        /// <returns>true=命中</returns>
        private static bool Contains(string[] names, string name)
        {
            if (names == null || name == null)
            {
                return false;
            }
            for (int i = 0; i < names.Length; i = i + 1)
            {
                if (string.Equals(names[i], name, StringComparison.Ordinal))
                {
                    return true;
                }
            }
            return false;
        }
    }
}
