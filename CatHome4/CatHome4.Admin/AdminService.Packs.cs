using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using System.Text.Json;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Http;
using Mau.Runtime;

using static CatHome4.Admin.CatHostService;

namespace CatHome4.Admin
{
    /// <summary>
    /// Admin 配置面分部——加载包池与猫级挂载（加载包机制：packs.json 池 + cat.cfg packs 字段）。
    /// 池定义全局唯一（key → 描述 + 路径清单），猫按 key 挂载；未挂载的包不可注入。
    /// 消费面：pack 内置工具（BuildPackPayload 委托）+ info 环境信息（BuildMountedPacksInfo）。
    /// </summary>
    internal static partial class AdminService
    {
        /// <summary>
        /// 加载包定义——池条目（key 唯一索引 / 描述 / 路径清单）。
        /// </summary>
        internal sealed class PackDefinition
        {
            /// <summary>包 key——池内唯一索引（pack 工具入参）</summary>
            public string Key { get; set; }

            /// <summary>描述——info 展示用（约定 ≤20 字）</summary>
            public string Desc { get; set; }

            /// <summary>路径清单——受控根 id: 前缀或绝对路径；目录项按一级 *.md 展开</summary>
            public string[] Paths { get; set; }
        }

        /// <summary>
        /// 包池文件路径——Data/config/packs.json。
        /// </summary>
        /// <returns>池文件绝对路径</returns>
        private static string GetPackPoolPath()
        {
            return Path.Combine(_dataRoot, "Data", "config", "packs.json");
        }

        /// <summary>
        /// 包池读取——防御式解析（文件缺失/损坏 = 空池，不阻断宿主）。
        /// JSON 形态：{ "packs": { "overwork": { "desc": "收工加载包", "paths": ["CCBP:L2/..."] } } }
        /// </summary>
        /// <returns>包定义列表（池序；空池=空列表）</returns>
        internal static List<PackDefinition> LoadPackPool()
        {
            List<PackDefinition> list = new List<PackDefinition>();
            try
            {
                string path = GetPackPoolPath();
                if (!File.Exists(path))
                {
                    return list;
                }
                string json = File.ReadAllText(path);
                using (JsonDocument doc = JsonUtil.ParseStrict(json))
                {
                    JsonElement root = doc.RootElement;
                    JsonElement packs;
                    if (!root.TryGetProperty("packs", out packs) || packs.ValueKind != JsonValueKind.Object)
                    {
                        return list;
                    }
                    foreach (JsonProperty prop in packs.EnumerateObject())
                    {
                        PackDefinition def = new PackDefinition();
                        def.Key = prop.Name;
                        def.Desc = "";
                        def.Paths = new string[0];
                        if (prop.Value.ValueKind == JsonValueKind.Object)
                        {
                            def.Desc = GetStringProp(prop.Value, "desc");
                            def.Paths = GetStringArrayProp(prop.Value, "paths");
                        }
                        list.Add(def);
                    }
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "包池读取失败：" + ex.Message, "CONFIG");
            }
            return list;
        }

        /// <summary>
        /// 猫级挂载清单——cat.cfg packs 字段（缺失/损坏 = 未挂载任何包）。
        /// </summary>
        /// <param name="catKey">猫 key（majordomo=默认猫）</param>
        /// <returns>已挂载包 key 数组（空=未挂载）</returns>
        internal static string[] GetMountedPacks(string catKey)
        {
            if (catKey == null || catKey.Length == 0)
            {
                return new string[0];
            }
            CatCfgData cfg = LoadCatCfg(Path.Combine(_dataRoot, "Data", "sessions", catKey, "cat.cfg"));
            if (cfg == null || cfg.Packs == null)
            {
                return new string[0];
            }
            return cfg.Packs;
        }

        /// <summary>
        /// 包解析入口——pack 内置工具数据面（Core 经静态委托调用：catKey + packKey → 结果 JSON）。
        /// 成功：{"ok":true,"key":"..","desc":"..","paths":[".."]}；失败：{"ok":false,"error":"ERR|..."}。
        /// 三道门：池内存在 → 本猫已挂载 → 返回路径清单（读取与展开归 Core，与前文注入同源）。
        /// </summary>
        /// <param name="catKey">猫 key</param>
        /// <param name="packKey">包 key</param>
        /// <returns>结果 JSON 文本</returns>
        internal static string BuildPackPayload(string catKey, string packKey)
        {
            List<PackDefinition> pool = LoadPackPool();
            StringBuilder available = new StringBuilder();
            for (int i = 0; i < pool.Count; i = i + 1)
            {
                if (available.Length > 0)
                {
                    available.Append(",");
                }
                available.Append(pool[i].Key);
            }
            string availableText = available.ToString();
            if (availableText.Length == 0)
            {
                availableText = "(空池)";
            }
            PackDefinition target = null;
            for (int i = 0; i < pool.Count; i = i + 1)
            {
                if (string.Equals(pool[i].Key, packKey, StringComparison.Ordinal))
                {
                    target = pool[i];
                    break;
                }
            }
            if (target == null)
            {
                return JsonUtil.Serialize(new
                {
                    ok = false,
                    error = "ERR|PACK_KEY_NOT_FOUND|包不存在: " + packKey + " | 池内可用: " + availableText
                });
            }
            string[] mounted = GetMountedPacks(catKey);
            bool isMounted = false;
            for (int i = 0; i < mounted.Length; i = i + 1)
            {
                if (string.Equals(mounted[i], packKey, StringComparison.Ordinal))
                {
                    isMounted = true;
                    break;
                }
            }
            if (!isMounted)
            {
                StringBuilder mountedText = new StringBuilder();
                for (int i = 0; i < mounted.Length; i = i + 1)
                {
                    if (mountedText.Length > 0)
                    {
                        mountedText.Append(",");
                    }
                    mountedText.Append(mounted[i]);
                }
                string mountedShow = mountedText.ToString();
                if (mountedShow.Length == 0)
                {
                    mountedShow = "(未挂载)";
                }
                return JsonUtil.Serialize(new
                {
                    ok = false,
                    error = "ERR|PACK_NOT_MOUNTED|包未挂载: " + packKey + " | 本猫已挂载: " + mountedShow + " | 池内可用: " + availableText
                });
            }
            if (target.Paths == null || target.Paths.Length == 0)
            {
                return JsonUtil.Serialize(new
                {
                    ok = false,
                    error = "ERR|PACK_EMPTY|包无路径清单: " + packKey
                });
            }
            return JsonUtil.Serialize(new
            {
                ok = true,
                key = target.Key,
                desc = target.Desc,
                paths = target.Paths
            });
        }
        /// <summary>
        /// 挂载包结构化清单——info 环境信息消费（分类 JSON 块；BuildMountedPacksInfo 的单一数据源）。
        /// 判据：未挂载=空列表；池内已删的挂载 key 原样列出（desc 空——挂载与池不一致可见，不静默吞）。
        /// </summary>
        /// <param name="catKey">猫 key</param>
        /// <returns>逐项 {key, desc}</returns>
        internal static List<Dictionary<string, object>> BuildMountedPackItems(string catKey)
        {
            List<Dictionary<string, object>> items = new List<Dictionary<string, object>>();
            string[] mounted = GetMountedPacks(catKey);
            if (mounted.Length == 0)
            {
                return items;
            }
            List<PackDefinition> pool = LoadPackPool();
            for (int i = 0; i < mounted.Length; i = i + 1)
            {
                string desc = "";
                for (int k = 0; k < pool.Count; k = k + 1)
                {
                    if (string.Equals(pool[k].Key, mounted[i], StringComparison.Ordinal))
                    {
                        desc = pool[k].Desc;
                        break;
                    }
                }
                Dictionary<string, object> item = new Dictionary<string, object>();
                item["key"] = mounted[i];
                item["desc"] = desc;
                items.Add(item);
            }
            return items;
        }

        /// <summary>
        /// 池内包清单——前端配置页与猫配置挂载面消费（key + 描述 + 路径项数）。
        /// 返回对象列表（JSON 化由调用方 Results.Json 承担——不可返回已序列化字符串，否则前端拿到的是字符串）。
        /// </summary>
        /// <returns>对象列表</returns>
        internal static List<object> BuildAllPacks()
        {
            List<PackDefinition> pool = LoadPackPool();
            List<object> list = new List<object>();
            for (int i = 0; i < pool.Count; i = i + 1)
            {
                int pathCount = 0;
                if (pool[i].Paths != null)
                {
                    pathCount = pool[i].Paths.Length;
                }
                list.Add(new
                {
                    key = pool[i].Key,
                    desc = pool[i].Desc,
                    pathCount = pathCount
                });
            }
            return list;
        }

        /// <summary>
        /// 池序列化——包定义列表 → 池 JSON 文本（管理面写入唯一出口）。
        /// </summary>
        /// <param name="pool">包定义列表</param>
        /// <returns>池 JSON 文本</returns>
        private static string SerializePool(List<PackDefinition> pool)
        {
            Dictionary<string, object> map = new Dictionary<string, object>();
            for (int i = 0; i < pool.Count; i = i + 1)
            {
                map[pool[i].Key] = new
                {
                    desc = pool[i].Desc,
                    paths = pool[i].Paths
                };
            }
            return JsonUtil.Serialize(new { packs = map });
        }

        /// <summary>
        /// 池读取——GET /api/v1/packs（管理面：key + 描述 + 路径清单）。
        /// </summary>
        /// <returns>池 JSON</returns>
        internal static IResult HandlePacksGet()
        {
            List<PackDefinition> pool = LoadPackPool();
            List<object> list = new List<object>();
            for (int i = 0; i < pool.Count; i = i + 1)
            {
                list.Add(new
                {
                    key = pool[i].Key,
                    desc = pool[i].Desc,
                    paths = pool[i].Paths
                });
            }
            return Results.Json(new { ok = true, packs = list });
        }

        /// <summary>
        /// 池写入——POST /api/v1/packs（body: {key, desc, paths[]}；同名覆盖 = 增改一体）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandlePacksPost(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string key = "";
            string desc = "";
            List<string> paths = new List<string>();
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    key = GetJsonString(root, "key").Trim();
                    desc = GetJsonString(root, "desc").Trim();
                    JsonElement pathsEl;
                    if (root.TryGetProperty("paths", out pathsEl) && pathsEl.ValueKind == JsonValueKind.Array)
                    {
                        for (int i = 0; i < pathsEl.GetArrayLength(); i = i + 1)
                        {
                            JsonElement item = pathsEl[i];
                            if (item.ValueKind == JsonValueKind.String)
                            {
                                string got = item.GetString();
                                if (got != null && got.Trim().Length > 0)
                                {
                                    paths.Add(got.Trim());
                                }
                            }
                        }
                    }
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            if (key.Length == 0)
            {
                return Results.Json(new { ok = false, error = "key 为空" });
            }
            if (paths.Count == 0)
            {
                return Results.Json(new { ok = false, error = "paths 为空（包至少一个路径项）" });
            }
            List<PackDefinition> pool = LoadPackPool();
            bool found = false;
            for (int i = 0; i < pool.Count; i = i + 1)
            {
                if (string.Equals(pool[i].Key, key, StringComparison.Ordinal))
                {
                    pool[i].Desc = desc;
                    pool[i].Paths = paths.ToArray();
                    found = true;
                    break;
                }
            }
            if (!found)
            {
                PackDefinition def = new PackDefinition();
                def.Key = key;
                def.Desc = desc;
                def.Paths = paths.ToArray();
                pool.Add(def);
            }
            try
            {
                ConfigStore.AtomicWrite(GetPackPoolPath(), SerializePool(pool));
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 3, "包池写入失败：" + ex.Message, "CONFIG");
                return Results.Json(new { ok = false, error = "写入失败: " + ex.Message });
            }
            LogStore.Add("CatHome4", 1, "包池写入：" + key + "（" + paths.Count.ToString() + " 个路径项）", "CONFIG");
            return Results.Json(new { ok = true, key = key, count = pool.Count });
        }

        /// <summary>
        /// 池删除——POST /api/v1/packs/delete（body: {key}）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandlePacksDelete(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string key = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    key = GetJsonString(doc.RootElement, "key").Trim();
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            if (key.Length == 0)
            {
                return Results.Json(new { ok = false, error = "key 为空" });
            }
            List<PackDefinition> pool = LoadPackPool();
            List<PackDefinition> kept = new List<PackDefinition>();
            bool removed = false;
            for (int i = 0; i < pool.Count; i = i + 1)
            {
                if (string.Equals(pool[i].Key, key, StringComparison.Ordinal))
                {
                    removed = true;
                    continue;
                }
                kept.Add(pool[i]);
            }
            if (!removed)
            {
                return Results.Json(new { ok = false, error = "包不存在: " + key });
            }
            try
            {
                ConfigStore.AtomicWrite(GetPackPoolPath(), SerializePool(kept));
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 3, "包池写入失败：" + ex.Message, "CONFIG");
                return Results.Json(new { ok = false, error = "写入失败: " + ex.Message });
            }
            return Results.Json(new { ok = true, key = key, count = kept.Count });
        }
    }
}
