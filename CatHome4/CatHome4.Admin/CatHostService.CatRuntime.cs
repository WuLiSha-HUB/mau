using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using Mau.Runtime;
using static CatHome4.Admin.AdminService;

namespace CatHome4.Admin
{
    /// <summary>
    /// 猫运行态持久化面（A285：自 AdminService 抽出）——Data/runtime/cats.json。
    /// 分离契约：cat.cfg 只承载用户配置（persona/toolNames/injectList/enabledRoots 等）；运行态（running/port）独立落盘。
    /// 语义：缺失记录 = 静默态（不拉起）；port=0 = 未监听。
    /// 写时机：cat.start / cat.stop / 启动换端口 / 旧配置一次性迁移——用户配置改动不触本文件，启动链不写 cat.cfg。
    /// </summary>
    internal static partial class CatHostService
    {
        /// <summary>
        /// 猫运行态条目——running + port。
        /// </summary>
        private sealed class CatRuntimeEntry
        {
            /// <summary>是否运行态——启动扫描拉起依据</summary>
            public bool Running;

            /// <summary>监听端口——0=未监听</summary>
            public int Port;
        }

        /// <summary>
        /// 运行态文件路径——Data/runtime/cats.json。
        /// </summary>
        /// <returns>绝对路径</returns>
        private static string CatRuntimePath()
        {
            return Path.Combine(_dataRoot, "Data", "runtime", "cats.json");
        }

        /// <summary>
        /// 读取运行态全表——防御式（缺失/损坏返回空表；不抛异常、不阻断启动）。
        /// </summary>
        /// <returns>猫 id → 运行态条目</returns>
        private static Dictionary<string, CatRuntimeEntry> LoadCatRuntime()
        {
            Dictionary<string, CatRuntimeEntry> map = new Dictionary<string, CatRuntimeEntry>();
            string path = CatRuntimePath();
            if (!File.Exists(path))
            {
                return map;
            }
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(File.ReadAllText(path)))
                {
                    JsonElement catsEl;
                    if (doc.RootElement.TryGetProperty("cats", out catsEl) && catsEl.ValueKind == JsonValueKind.Object)
                    {
                        foreach (JsonProperty prop in catsEl.EnumerateObject())
                        {
                            CatRuntimeEntry entry = new CatRuntimeEntry();
                            JsonElement runningEl;
                            if (prop.Value.TryGetProperty("running", out runningEl) && runningEl.ValueKind == JsonValueKind.True)
                            {
                                entry.Running = true;
                            }
                            JsonElement portEl;
                            if (prop.Value.TryGetProperty("port", out portEl) && portEl.ValueKind == JsonValueKind.Number)
                            {
                                entry.Port = portEl.GetInt32();
                            }
                            map[prop.Name] = entry;
                        }
                    }
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "猫运行态读取失败（按空表处理）：" + ex.Message, "CHAT");
            }
            return map;
        }

        /// <summary>
        /// 写入运行态全表——原子写（临时文件 + 改名）。
        /// </summary>
        /// <param name="map">猫 id → 运行态条目</param>
        private static void SaveCatRuntime(Dictionary<string, CatRuntimeEntry> map)
        {
            System.Collections.Generic.List<(string Key, object Value)> items = new System.Collections.Generic.List<(string Key, object Value)>();
            foreach (KeyValuePair<string, CatRuntimeEntry> pair in map)
            {
                items.Add((pair.Key, JsonUtil.Raw(JsonUtil.Object(("running", pair.Value.Running), ("port", pair.Value.Port)))));
            }
            string payload = JsonUtil.Object(("cats", JsonUtil.Raw(JsonUtil.Object(items.ToArray()))));
            try
            {
                string path = CatRuntimePath();
                string dir = Path.GetDirectoryName(path);
                if (dir != null && dir.Length > 0)
                {
                    Directory.CreateDirectory(dir);
                }
                ConfigStore.AtomicWrite(path, payload);
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "猫运行态写入失败：" + ex.Message, "CHAT");
            }
        }

        /// <summary>
        /// 更新单猫运行态——读改写（保留其它猫记录）。
        /// </summary>
        /// <param name="id">猫 id</param>
        /// <param name="running">是否运行</param>
        /// <param name="port">监听端口（静默态传 0）</param>
        private static void SetCatRuntime(string id, bool running, int port)
        {
            if (id == null || id.Length == 0)
            {
                return;
            }
            Dictionary<string, CatRuntimeEntry> map = LoadCatRuntime();
            map[id] = new CatRuntimeEntry() { Running = running, Port = port };
            SaveCatRuntime(map);
        }

        /// <summary>
        /// 移除单猫运行态——cat.delete 调用。
        /// </summary>
        /// <param name="id">猫 id</param>
        private static void RemoveCatRuntime(string id)
        {
            if (id == null || id.Length == 0)
            {
                return;
            }
            Dictionary<string, CatRuntimeEntry> map = LoadCatRuntime();
            if (map.Remove(id))
            {
                SaveCatRuntime(map);
            }
        }

        /// <summary>
        /// 旧配置一次性迁移——cat.cfg 携带 running/port 且运行态无记录时补写运行态条目。
        /// </summary>
        /// <param name="id">猫 id</param>
        /// <param name="running">旧 cfg 的 running</param>
        /// <param name="port">旧 cfg 的 port</param>
        /// <param name="map">运行态表（就地修改）</param>
        /// <returns>true=发生迁移（调用方负责统一落盘）</returns>
        private static bool MigrateCatRuntime(string id, bool running, int port, Dictionary<string, CatRuntimeEntry> map)
        {
            if (map.ContainsKey(id))
            {
                return false;
            }
            if (!running && port == 0)
            {
                return false;
            }
            map[id] = new CatRuntimeEntry() { Running = running, Port = port };
            return true;
        }
    }
}
