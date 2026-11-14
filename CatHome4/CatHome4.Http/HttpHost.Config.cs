using System;
using System.Collections.Generic;
using System.Text.Json;
using System.Threading;
using System.Threading.Channels;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.Logging;
using Mau.Runtime;

namespace CatHome4.Http
{
    /// <summary>
    /// HttpHost 配置区面分部——GET/POST /api/v1/config 读写 + 敏感键掩码。
    /// P7b partial 拆分——自 HttpHost.cs 原样搬移，逻辑零改动。
    /// </summary>
    public sealed partial class HttpHost
    {
        /// <summary>配置读取——GET /api/v1/config（A364 纯 KV）。
        /// 返回 Data/config 下全部已落盘键值（key/value 两字段，无声明面 / 无默认值 / 无来源标注）；
        /// 未落盘的键不出现在本面——默认值由消费侧硬编码，前端表单自行写死注释与默认说明。
        /// 敏感键（api_key/secret/token）掩码展示——snapshot 边界铁律同源。</summary>
        /// <returns>配置 JSON——版本 + items[key/value]</returns>
        private IResult HandleConfigGet()
        {
            // [段1] 解析配置存储——未绑定返回空列表（壳形态可渲染）
            ConfigStore cfg = null!;
            bool bound = DataBox.TryResolve<ConfigStore>(out cfg);
            List<object> items = new List<object>();
            if (!bound || cfg == null)
            {
                var emptyResp = new
                {
                    version = 1,
                    items = items
                };
                return Results.Json(emptyResp);
            }

            // [段2] 纯 KV 读面——全部已落盘键值原样输出；敏感键掩码
            KeyValuePair<string, string>[] all = cfg.All();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                string value = all[i].Value;
                if (IsSecretKey(all[i].Key))
                {
                    value = MaskSecret(value);
                }

                items.Add(new
                {
                    key = all[i].Key,
                    value = value
                });
            }

            var resp = new
            {
                version = 1,
                items = items
            };
            return Results.Json(resp);
        }

        /// <summary>
        /// 配置写入——POST /api/v1/config（body: {"key":"...","value":"..."}）。
        /// 落盘 llm.cfg（ConfigStore.Set + Save 原子写）；敏感键拒绝掩码值回写（防掩码覆盖）。
        /// 注意：运行时生效待 P7（DeepSeekLlmRuntime 构造期读配置——本轮写配置 = 落盘 + 提示重启）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON——ok/key/frame</returns>
        private async Task<IResult> HandleConfigPost(HttpContext ctx)
        {
            // [段1] body 解析——防御式 JSON {key,value}
            string body = "";
            using (System.IO.StreamReader reader = new System.IO.StreamReader(ctx.Request.Body))
            {
                body = await reader.ReadToEndAsync();
            }
            string key = "";
            string value = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    if (root.TryGetProperty("key", out JsonElement k))
                    {
                        key = k.GetString() ?? "";
                    }
                    if (root.TryGetProperty("value", out JsonElement v))
                    {
                        value = v.GetString() ?? "";
                    }
                }
            }
            catch (Exception)
            {
                // body 非 JSON——key/value 保持空串走下方校验拒绝
                key = "";
                value = "";
            }
            // [段2] 校验——key 非空 + 空值走清除通道 + 掩码回写拒绝
            if (key.Length == 0)
            {
                return Results.Json(new { ok = false, error = "key 为空" });
            }
            if (value.Length == 0)
            {
                // 空值 = 清除该项落盘值（回到默认；A364 纯 KV 语义——空串不是合法取值）
                ConfigStore clearCfg = null!;
                if (!DataBox.TryResolve<ConfigStore>(out clearCfg) || clearCfg == null)
                {
                    return Results.Json(new { ok = false, error = "配置存储未绑定" });
                }
                string clearError;
                if (!clearCfg.ResetToDefault(key, out clearError))
                {
                    return Results.Json(new { ok = false, error = clearError });
                }
                return Results.Json(new { ok = true, key = key, value = "", cleared = true, frame = FlowRunner.GlobalFrame });
            }
            if (value.IndexOf("****", StringComparison.Ordinal) >= 0)
            {
                return Results.Json(new { ok = false, error = "value 含掩码标记——请输入真实值" });
            }
            // [段3] 写入落盘——纯 KV（A364）：写死防御（键名 / 长度 / 掩码）+ 内存 + 原子写回滚
            ConfigStore cfg = null!;
            bool bound = DataBox.TryResolve<ConfigStore>(out cfg);
            if (!bound || cfg == null)
            {
                return Results.Json(new { ok = false, error = "配置存储未绑定" });
            }
            string checkError;
            if (!cfg.SetChecked(key, value, out checkError))
            {
                return Results.Json(new { ok = false, error = checkError });
            }
            long frame = FlowRunner.GlobalFrame;
            // 回执值掩码——敏感键不回显新值明文（与 GET 掩码同规）
            string maskedValue;
            if (IsSecretKey(key))
            {
                maskedValue = MaskSecret(value);
            }
            else
            {
                maskedValue = value;
            }
            var resp = new
            {
                ok = true,
                key = key,
                value = maskedValue,
                frame = frame
            };
            return Results.Json(resp);
        }

        /// <summary>
        /// 敏感键判定——api_key/secret/token 系内容掩码展示（边界铁律：密钥不进快照/回显明文）
        /// </summary>
        /// <param name="key">配置键</param>
        /// <returns>是否敏感</returns>
        private static bool IsSecretKey(string key)
        {
            return key.IndexOf("api_key", StringComparison.OrdinalIgnoreCase) >= 0
                || key.IndexOf("secret", StringComparison.OrdinalIgnoreCase) >= 0
                || key.IndexOf("token", StringComparison.OrdinalIgnoreCase) >= 0
                || key.IndexOf("console_hash", StringComparison.OrdinalIgnoreCase) >= 0;
        }

        /// <summary>
        /// 敏感值掩码——前4 + **** + 后4（短值全掩码）
        /// </summary>
        /// <param name="value">原始值</param>
        /// <returns>掩码文本</returns>
        private static string MaskSecret(string value)
        {
            if (value == null || value.Length == 0)
            {
                return "";
            }
            if (value.Length <= 8)
            {
                return "****";
            }
            return value.Substring(0, 4) + "****" + value.Substring(value.Length - 4);
        }
    }
}