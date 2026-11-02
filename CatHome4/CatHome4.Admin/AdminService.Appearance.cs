using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Http;
using Mau.Runtime;

using static CatHome4.Admin.CatHostService;

namespace CatHome4.Admin
{
    /// <summary>
    /// Admin 域外观配置池面（A244）——池 CRUD 端点 + 每猫外观引用解析。
    /// 与 LLM API / QQ Bot 池同构；无密钥面——单文件落盘，不设 secrets 层。
    /// </summary>
    internal static partial class AdminService
    {
        // [段1] 池 CRUD 端点（config 页签「外观池」区消费）

        /// <summary>外观池列表——GET /api/v1/appearance-apis。</summary>
        /// <returns>列表 JSON——version + items</returns>
        internal static IResult HandleAppearanceApisGet()
        {
            List<object> items = new List<object>();
            CH_AppearanceConfigStore store = null;
            DataBox.TryResolve<CH_AppearanceConfigStore>(out store);
            if (store != null)
            {
                CH_AppearanceConfig[] configs = store.GetAll();
                for (int i = 0; i < configs.Length; i = i + 1)
                {
                    CH_AppearanceConfig c = configs[i];
                    items.Add(new
                    {
                        appearanceId = c.AppearanceId.ToString("D"),
                        displayName = c.DisplayName,
                        petKind = c.PetKind,
                        ttsStyle = c.TtsStyle,
                        roundSummary = c.RoundSummary,
                        summaryAudio = c.SummaryAudio,
                        isDefault = c.IsDefault
                    });
                }
            }
            var resp = new
            {
                version = 1,
                items = items
            };
            return Results.Json(resp);
        }

        /// <summary>外观池新建——POST /api/v1/appearance-apis（body: displayName + 四字段）。</summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleAppearanceApisPost(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string displayName = "";
            string petKind = "";
            string ttsStyle = "";
            bool roundSummary = false;
            bool summaryAudio = false;
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    displayName = GetJsonString(root, "displayName");
                    petKind = GetJsonString(root, "petKind");
                    ttsStyle = GetJsonString(root, "ttsStyle");
                    roundSummary = GetJsonBoolOrDefault(root, "roundSummary");
                    summaryAudio = GetJsonBoolOrDefault(root, "summaryAudio");
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            if (displayName.Length == 0)
            {
                return Results.Json(new { ok = false, error = "displayName 为空" });
            }
            CH_AppearanceConfigStore store = null;
            DataBox.TryResolve<CH_AppearanceConfigStore>(out store);
            if (store == null)
            {
                return Results.Json(new { ok = false, error = "外观池未绑定" });
            }
            CH_AppearanceConfig config = new CH_AppearanceConfig();
            config.AppearanceId = Guid.NewGuid();
            config.DisplayName = displayName;
            config.PetKind = petKind;
            config.TtsStyle = ttsStyle;
            config.RoundSummary = roundSummary;
            config.SummaryAudio = summaryAudio;
            store.Save(config);
            LogStore.Add("CatHome4", 1, "外观池新增条目「" + displayName + "」（" + config.AppearanceId.ToString("D") + "）", "CONFIG");
            return Results.Json(new { ok = true, appearanceId = config.AppearanceId.ToString("D") });
        }

        /// <summary>外观池编辑——POST /api/v1/appearance-apis/edit（body: appearanceId + 四字段）。</summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleAppearanceApisEdit(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string appearanceId = "";
            string displayName = "";
            string petKind = "";
            string ttsStyle = "";
            bool roundSummary = false;
            bool summaryAudio = false;
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    appearanceId = GetJsonString(root, "appearanceId");
                    displayName = GetJsonString(root, "displayName");
                    petKind = GetJsonString(root, "petKind");
                    ttsStyle = GetJsonString(root, "ttsStyle");
                    roundSummary = GetJsonBoolOrDefault(root, "roundSummary");
                    summaryAudio = GetJsonBoolOrDefault(root, "summaryAudio");
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            Guid id;
            if (!Guid.TryParse(appearanceId, out id) || id == Guid.Empty)
            {
                return Results.Json(new { ok = false, error = "appearanceId 非法" });
            }
            if (displayName.Length == 0)
            {
                return Results.Json(new { ok = false, error = "displayName 为空" });
            }
            CH_AppearanceConfigStore store = null;
            DataBox.TryResolve<CH_AppearanceConfigStore>(out store);
            if (store == null)
            {
                return Results.Json(new { ok = false, error = "外观池未绑定" });
            }
            CH_AppearanceConfig existing;
            if (!store.TryGet(id, out existing))
            {
                return Results.Json(new { ok = false, error = "外观不存在" });
            }
            CH_AppearanceConfig config = new CH_AppearanceConfig();
            config.AppearanceId = id;
            config.DisplayName = displayName;
            config.PetKind = petKind;
            config.TtsStyle = ttsStyle;
            config.RoundSummary = roundSummary;
            config.SummaryAudio = summaryAudio;
            config.IsDefault = existing.IsDefault;
            store.Save(config);
            LogStore.Add("CatHome4", 1, "外观池「" + displayName + "」已更新（" + id.ToString("D") + "）", "CONFIG");
            return Results.Json(new { ok = true, appearanceId = id.ToString("D") });
        }

        /// <summary>
        /// 外观池删除——POST /api/v1/appearance-apis/delete（body: appearanceId）。
        /// 引用它的猫解析为 null → 回退默认外观（默认项亦可删——与 LLM API 池 M3 拍板同）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleAppearanceApisDelete(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string appearanceId = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    appearanceId = GetJsonString(doc.RootElement, "appearanceId");
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            Guid id;
            if (!Guid.TryParse(appearanceId, out id) || id == Guid.Empty)
            {
                return Results.Json(new { ok = false, error = "appearanceId 非法" });
            }
            CH_AppearanceConfigStore store = null;
            DataBox.TryResolve<CH_AppearanceConfigStore>(out store);
            if (store == null)
            {
                return Results.Json(new { ok = false, error = "外观池未绑定" });
            }
            if (!store.Delete(id))
            {
                return Results.Json(new { ok = false, error = "外观不存在" });
            }
            // 引用方即时回退——重启态猫经 catcfg.apply 逐只刷新（池删除不免除引用校验）
            LogStore.Add("CatHome4", 1, "外观池条目已删除：" + id.ToString("D"), "CONFIG");
            return Results.Json(new { ok = true, appearanceId = id.ToString("D") });
        }

        /// <summary>
        /// 外观池设为默认——POST /api/v1/appearance-apis/default（body: appearanceId）。
        /// 默认外观语义：appearanceId 零值的猫固定走它（每次调用实时解析——切换立即生效）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleAppearanceApisDefault(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string appearanceId = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    appearanceId = GetJsonString(doc.RootElement, "appearanceId");
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            Guid id;
            if (!Guid.TryParse(appearanceId, out id) || id == Guid.Empty)
            {
                return Results.Json(new { ok = false, error = "appearanceId 非法" });
            }
            CH_AppearanceConfigStore store = null;
            DataBox.TryResolve<CH_AppearanceConfigStore>(out store);
            if (store == null)
            {
                return Results.Json(new { ok = false, error = "外观池未绑定" });
            }
            if (!store.SetDefault(id))
            {
                return Results.Json(new { ok = false, error = "外观不存在" });
            }
            LogStore.Add("CatHome4", 1, "外观池已设为默认：" + id.ToString("D"), "CONFIG");
            return Results.Json(new { ok = true });
        }

        // [段2] 每猫引用解析与下拉清单

        /// <summary>
        /// 解析猫的有效外观配置——cat.cfg appearanceId 零值/缺失=跟随全局默认外观，非零=按身份取。
        /// 引用失效（条目已删）或池空返回 null——调用方取各字段保守值（roundSummary=false）。
        /// </summary>
        /// <param name="catKey">猫 key（majordomo=默认猫；多猫=会话 ID）</param>
        /// <param name="followDefault">输出：true=跟随默认外观；false=猫显式绑定（引用失效时同样为 false）</param>
        /// <returns>生效配置；无默认/引用失效 null</returns>
        internal static CH_AppearanceConfig ResolveEffectiveAppearance(string catKey, out bool followDefault)
        {
            followDefault = true;
            Guid appearanceId = Guid.Empty;
            if (catKey != null && catKey.Length > 0)
            {
                CatCfgData cfg = LoadCatCfg(Path.Combine(_dataRoot, "Data", "sessions", catKey, "cat.cfg"));
                appearanceId = ResolveAppearanceId(cfg);
            }
            if (_appearanceStore == null)
            {
                return null;
            }
            if (appearanceId == Guid.Empty)
            {
                return _appearanceStore.ResolveDefault();
            }
            followDefault = false;
            CH_AppearanceConfig config;
            if (_appearanceStore.TryGet(appearanceId, out config))
            {
                return config;
            }
            return null;
        }

        /// <summary>解析猫的轮末总结生效值——无生效外观时保守 false（与迁入前默认一致）。</summary>
        /// <param name="catKey">猫 key</param>
        /// <returns>true=每轮结束调度总结服务</returns>
        internal static bool ResolveEffectiveRoundSummary(string catKey)
        {
            bool ignoredFollow;
            CH_AppearanceConfig appearance = ResolveEffectiveAppearance(catKey, out ignoredFollow);
            if (appearance == null)
            {
                return false;
            }
            return appearance.RoundSummary;
        }
        /// <summary>解析猫的桌宠种类生效值——无生效外观 / 字段为空时保守空串（A251：空 = 无桌宠，前端按空值渲染，不回落默认素材）。</summary>
        /// <param name="catKey">猫 key</param>
        /// <returns>素材名；空=无桌宠（前端不显示）</returns>
        internal static string ResolveEffectivePetKind(string catKey)
        {
            bool ignoredFollow;
            CH_AppearanceConfig appearance = ResolveEffectiveAppearance(catKey, out ignoredFollow);
            if (appearance == null || appearance.PetKind == null)
            {
                return "";
            }
            return appearance.PetKind;
        }
        /// <summary>解析猫的总结自动出声配置——无生效外观时保守关闭（不出声）。</summary>
        /// <param name="catKey">猫 key</param>
        /// <returns>[0]=启用标志（"1"/"0"）；[1]=音色槽位值（空=服务默认音色）</returns>
        internal static string[] ResolveSummaryAudio(string catKey)
        {
            bool ignoredFollow;
            CH_AppearanceConfig appearance = ResolveEffectiveAppearance(catKey, out ignoredFollow);
            if (appearance == null)
            {
                return new string[] { "0", "" };
            }
            string voice = appearance.TtsStyle == null ? "" : appearance.TtsStyle;
            return new string[] { appearance.SummaryAudio ? "1" : "0", voice };
        }

        /// <summary>外观下拉清单——池全量（id + 显示名 + 默认标记；猫配置弹层消费）。</summary>
        /// <returns>下拉项列表</returns>
        private static List<object> BuildAppearanceOptions()
        {
            List<object> options = new List<object>();
            if (_appearanceStore != null)
            {
                CH_AppearanceConfig[] configs = _appearanceStore.GetAll();
                for (int i = 0; i < configs.Length; i = i + 1)
                {
                    options.Add(new
                    {
                        appearanceId = configs[i].AppearanceId.ToString("D"),
                        displayName = configs[i].DisplayName,
                        isDefault = configs[i].IsDefault
                    });
                }
            }
            return options;
        }

        /// <summary>从 cat.cfg 数据解析外观配置身份（空/非法=Guid.Empty=跟随默认外观）。</summary>
        /// <param name="cfg">cat.cfg 数据（可 null）</param>
        /// <returns>外观配置身份；零值=跟随默认外观</returns>
        /// <param name="cfg">猫配置数据</param>
        /// <returns>外观配置身份（Guid.Empty=跟随默认外观）</returns>
        internal static Guid ResolveAppearanceId(CatCfgData cfg)
        {
            if (cfg == null || cfg.AppearanceId == null || cfg.AppearanceId.Length == 0)
            {
                return Guid.Empty;
            }
            Guid parsed;
            if (!Guid.TryParse(cfg.AppearanceId, out parsed))
            {
                return Guid.Empty;
            }
            return parsed;
        }

        /// <summary>读取 JSON 对象布尔属性——防御式（缺字段/类型不符返回 false）。</summary>
        /// <param name="obj">JSON 元素</param>
        /// <param name="prop">属性名</param>
        /// <returns>布尔值</returns>
        private static bool GetJsonBoolOrDefault(JsonElement obj, string prop)
        {
            JsonElement value;
            if (obj.TryGetProperty(prop, out value))
            {
                if (value.ValueKind == JsonValueKind.True)
                {
                    return true;
                }
                if (value.ValueKind == JsonValueKind.False)
                {
                    return false;
                }
            }
            return false;
        }
    }
}
