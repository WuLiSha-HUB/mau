using System;
using System.Collections.Generic;

namespace Mau.Runtime
{
    /// <summary>
    /// 库访问策略表——多猫库级授权（`cat.cfg` 的 `dbLibs` 白名单在执行面的唯一判定点）。
    /// 语义（莎 2026-10-21 定 · fail-closed）：空 catId = 无猫上下文（管理端点 / 单测 / CLI）放行；
    /// 已登记猫按白名单逐库判定（空集合 = 无库权限）；未登记猫 = 无权限（拒，失败可见）。
    /// 全放标志：默认猫（majordomo）由登记方置 allLibs——库表变动不需重建策略。
    /// 线程模型：写 = 宿主主线程（catcfg.apply / 会话构造）；读 = 积木执行（主线程 Tick 内）——锁保护保险。
    /// </summary>
    public static class DbAccessPolicy
    {
        /// <summary>每猫白名单——catId → 允许库集合（空集合 = 无库权限）</summary>
        private static readonly Dictionary<string, HashSet<string>> _byCat = new Dictionary<string, HashSet<string>>(StringComparer.Ordinal);

        /// <summary>全放行猫——默认猫（majordomo）</summary>
        private static readonly HashSet<string> _allLibs = new HashSet<string>(StringComparer.Ordinal);

        /// <summary>策略表锁</summary>
        private static readonly object _gate = new object();

        /// <summary>
        /// 登记每猫库白名单——catcfg.apply / 会话构造时调用（覆盖已存在同 ID）。
        /// </summary>
        /// <param name="catId">猫 key（空=nop）</param>
        /// <param name="allLibs">true=全放行（默认猫）</param>
        /// <param name="libs">允许库 ID 白名单（null=空集合）</param>
        public static void Register(string? catId, bool allLibs, string[] libs)
        {
            if (catId == null || catId.Length == 0)
            {
                return;
            }
            HashSet<string> set = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
            if (libs != null)
            {
                for (int i = 0; i < libs.Length; i = i + 1)
                {
                    string lib = Normalize(libs[i]);
                    if (lib.Length > 0)
                    {
                        set.Add(lib);
                    }
                }
            }
            lock (_gate)
            {
                _byCat[catId] = set;
                if (allLibs)
                {
                    _allLibs.Add(catId);
                }
                else
                {
                    _allLibs.Remove(catId);
                }
            }
        }

        /// <summary>
        /// 注销每猫库白名单——cat.delete 时调用。
        /// </summary>
        /// <param name="catId">猫 key（空=nop）</param>
        public static void Unregister(string catId)
        {
            if (catId == null || catId.Length == 0)
            {
                return;
            }
            lock (_gate)
            {
                _byCat.Remove(catId);
                _allLibs.Remove(catId);
            }
        }

        /// <summary>
        /// 清空策略表——宿主重载配置面时用（测试隔离同用）。
        /// </summary>
        public static void Clear()
        {
            lock (_gate)
            {
                _byCat.Clear();
                _allLibs.Clear();
            }
        }

        /// <summary>
        /// 库是否对猫放行——判定唯一出口（空 catId 回退当前猫，仍空则放行）。
        /// </summary>
        /// <param name="catId">显式猫 key（OA 面 argsJson 注入；空=当前猫）</param>
        /// <param name="libId">库名（大小写不敏感）</param>
        /// <returns>true=放行</returns>
        public static bool IsAllowed(string? catId, string libId)
        {
            return DenyReason(catId, libId) == null;
        }

        /// <summary>
        /// 库访问拒绝原因——null=放行；非空=拒绝文案（具名出声，供工具回执）。
        /// </summary>
        /// <param name="catId">显式猫 key（空=当前猫）</param>
        /// <param name="libId">库名（大小写不敏感）</param>
        /// <returns>拒绝文案或 null</returns>
        public static string? DenyReason(string? catId, string libId)
        {
            string cat = ResolveCat(catId);
            if (cat.Length == 0)
            {
                return null;
            }
            string lib = Normalize(libId);
            lock (_gate)
            {
                if (_allLibs.Contains(cat))
                {
                    return null;
                }
                HashSet<string>? set;
                if (!_byCat.TryGetValue(cat, out set))
                {
                    return "猫未登记库权限（" + cat + "）——cat.cfg dbLibs 缺失或策略未加载";
                }
                if (set.Count == 0)
                {
                    return "猫无库权限（" + cat + "）——cat.cfg dbLibs 为空";
                }
                if (!set.Contains(lib))
                {
                    return "库未授权（" + lib + " 不在猫 " + cat + " 的 dbLibs 白名单）";
                }
                return null;
            }
        }

        /// <summary>
        /// 解析生效猫 key——显式 catId 优先，空回退当前猫（AsyncLocal），仍空返回空串。
        /// </summary>
        /// <param name="catId">显式猫 key</param>
        /// <returns>生效猫 key（空=无猫上下文）</returns>
        private static string ResolveCat(string? catId)
        {
            if (catId != null && catId.Length > 0)
            {
                return catId;
            }
            string? current = FileSystemRegistry.CurrentCatKey;
            if (current != null && current.Length > 0)
            {
                return current;
            }
            return "";
        }

        /// <summary>
        /// 库名归一——去空白 + 转小写（与库 ID 形态一致）。
        /// </summary>
        /// <param name="libId">库名原始值</param>
        /// <returns>归一后的库名（可空串）</returns>
        private static string Normalize(string libId)
        {
            if (libId == null)
            {
                return "";
            }
            return libId.Trim().ToLowerInvariant();
        }
    }
}
