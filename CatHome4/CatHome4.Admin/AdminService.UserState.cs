using System;
using System.Collections.Generic;
using System.Text;
using System.Text.Json;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Http;
using Mau.Runtime;
using CatHome4.Contracts;

namespace CatHome4.Admin
{
    /// <summary>
    /// Admin 用户态分部——当前用户（莎 / Rushiera）与 QQ 非用户指令开关（规格：Project/CH4/design-ch4-user-state.md）。
    /// 存储 = 配置群 user.cfg（user.current / user.qq_accept_nonuser / user.qq_ids）；本分部承载档案表 + 读写端点 + 注入段构建。
    /// 消费面：注入面（Program.Chat.cs BuildInjectPrompt）+ QQ 消息头（QQBotService.BuildHeader）+ 面板侧栏弹层（/api/v1/user-state）。
    /// </summary>
    internal static partial class AdminService
    {
        /// <summary>
        /// 用户档案——候选用户（显示名 / 一行说明）。
        /// </summary>
        internal sealed class UserProfile
        {
            /// <summary>显示名——配置值即显示名（QQ 消息头与面板侧栏直接消费）</summary>
            public string Name;

            /// <summary>一行说明——面板弹层选项副标题</summary>
            public string Desc;
        }

        /// <summary>
        /// 用户档案表——当前用户唯一候选源（首位 = 缺省用户）。
        /// </summary>
        private static readonly UserProfile[] _userProfiles = new UserProfile[]
        {
            new UserProfile
            {
                Name = UserStateKeys.DefaultUserName,
                Desc = "源实例——CatHome 4 与 CCBP 知识网络的搭建者"
            },
            new UserProfile
            {
                Name = "Rushiera",
                Desc = "莎的朋友——本支主理（2026-09-25 起）"
            }
        };

        // [段1] 读取面
        /// <summary>
        /// 缺省用户显示名——档案表首位（配置缺失 / 非法时回落）。
        /// </summary>
        /// <returns>显示名</returns>
        internal static string DefaultUserName()
        {
            return _userProfiles[0].Name;
        }

        /// <summary>
        /// 用户档案查询——按显示名精确匹配（不存在返回 null）。
        /// </summary>
        /// <param name="name">显示名</param>
        /// <returns>档案或 null</returns>
        internal static UserProfile FindUserProfile(string name)
        {
            if (name == null || name.Length == 0)
            {
                return null;
            }
            for (int i = 0; i < _userProfiles.Length; i = i + 1)
            {
                if (string.Equals(_userProfiles[i].Name, name, StringComparison.Ordinal))
                {
                    return _userProfiles[i];
                }
            }
            return null;
        }

        /// <summary>
        /// 当前用户显示名——配置群读取（缺失 / 空回落缺省用户）。
        /// </summary>
        /// <returns>显示名</returns>
        internal static string CurrentUserName()
        {
            ConfigStore cfg = ResolveUserConfig();
            if (cfg == null)
            {
                return DefaultUserName();
            }
            string name = cfg.Get(UserStateKeys.Current, "");
            if (name.Length == 0)
            {
                return DefaultUserName();
            }
            return name;
        }

        /// <summary>
        /// 接受非用户的指令——配置群读取（缺失 / 非法回落 false）。
        /// </summary>
        /// <returns>是否接受</returns>
        internal static bool AcceptNonUserCommands()
        {
            ConfigStore cfg = ResolveUserConfig();
            if (cfg == null)
            {
                return false;
            }
            string value = cfg.Get(UserStateKeys.QqAcceptNonUser, "false");
            if (value == "true" || value == "1")
            {
                return true;
            }
            return false;
        }

        /// <summary>
        /// 当前用户群内身份清单——逗号分隔（缺失 / 空 = 空数组）。
        /// </summary>
        /// <returns>身份项数组</returns>
        internal static string[] CurrentUserQqIds()
        {
            ConfigStore cfg = ResolveUserConfig();
            if (cfg == null)
            {
                return new string[0];
            }
            return SplitIdentityList(cfg.Get(UserStateKeys.QqIds, ""));
        }

        /// <summary>
        /// 配置存储解析——静态注入面优先，未注入回落 DataBox（测试环境）。
        /// </summary>
        /// <returns>配置存储或 null</returns>
        private static ConfigStore ResolveUserConfig()
        {
            if (_globalConfig != null)
            {
                return _globalConfig;
            }
            ConfigStore cfg = null;
            DataBox.TryResolve<ConfigStore>(out cfg);
            return cfg;
        }

        /// <summary>
        /// 身份清单拆分——逗号分隔 + 去空白 + 去空项。
        /// </summary>
        /// <param name="text">清单文本</param>
        /// <returns>身份项数组</returns>
        internal static string[] SplitIdentityList(string text)
        {
            if (text == null || text.Length == 0)
            {
                return new string[0];
            }
            List<string> list = new List<string>();
            string[] parts = text.Split(',');
            for (int i = 0; i < parts.Length; i = i + 1)
            {
                string token = parts[i].Trim();
                if (token.Length > 0)
                {
                    list.Add(token);
                }
            }
            return list.ToArray();
        }

        // [段2] 注入面已退役——身份不再进系统提示词（2026-11-02）
        // 【当前用户】+【QQ 渠道】两段改由 `ccbp:USER.md` 承载（知识文件按 injectList 注入，只留一份）。
        // user.cfg 保留 user.current / user.qq_ids / user.qq_accept_nonuser——消费面只剩 QQ 消息头（QQBotService.BuildHeader）
        // 与面板侧栏弹层（/api/v1/user-state），与本分部以下端点。
        // [段3] 管理端点
        /// <summary>
        /// 用户态读取——GET /api/v1/user-state（当前用户 + 档案候选 + QQ 开关 + 群内身份）。
        /// </summary>
        /// <returns>用户态 JSON</returns>
        internal static IResult HandleUserStateGet()
        {
            List<object> users = new List<object>();
            for (int i = 0; i < _userProfiles.Length; i = i + 1)
            {
                users.Add(new { name = _userProfiles[i].Name, desc = _userProfiles[i].Desc });
            }
            string[] ids = CurrentUserQqIds();
            var resp = new
            {
                ok = true,
                current = CurrentUserName(),
                acceptNonUser = AcceptNonUserCommands(),
                qqIds = string.Join(",", ids),
                users = users
            };
            return Results.Json(resp);
        }

        /// <summary>
        /// 用户态写入——POST /api/v1/user-state（body: {"current":"..","acceptNonUser":true,"qqIds":".."}）。
        /// 字段级语义：未提交字段逐字保留；qqIds 提交空串 = 清空（群@ 一律按非用户）。写走 SetChecked（白名单 + 值域 + 原子写回滚）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleUserStatePost(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            ConfigStore cfg = ResolveUserConfig();
            if (cfg == null)
            {
                return Results.Json(new { ok = false, error = "配置存储未绑定" });
            }
            ConfigSchema schema = null;
            DataBox.TryResolve<ConfigSchema>(out schema);
            string current = "";
            string qqIds = "";
            bool qqIdsPresent = false;
            bool acceptNonUser = false;
            bool acceptPresent = false;
            try
            {
                using (JsonDocument doc = JsonDocument.Parse(body))
                {
                    JsonElement root = doc.RootElement;
                    current = GetJsonString(root, "current");
                    JsonElement idsEl;
                    if (root.TryGetProperty("qqIds", out idsEl) && idsEl.ValueKind == JsonValueKind.String)
                    {
                        qqIds = idsEl.GetString() ?? "";
                        qqIdsPresent = true;
                    }
                    JsonElement acceptEl;
                    if (root.TryGetProperty("acceptNonUser", out acceptEl) && (acceptEl.ValueKind == JsonValueKind.True || acceptEl.ValueKind == JsonValueKind.False))
                    {
                        acceptNonUser = acceptEl.GetBoolean();
                        acceptPresent = true;
                    }
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            string error = "";
            if (current.Length > 0)
            {
                if (FindUserProfile(current) == null)
                {
                    return Results.Json(new { ok = false, error = "未知用户: " + current });
                }
                if (!cfg.SetChecked(UserStateKeys.Current, current, schema, out error))
                {
                    return Results.Json(new { ok = false, error = error });
                }
            }
            if (acceptPresent)
            {
                string flag = "false";
                if (acceptNonUser)
                {
                    flag = "true";
                }
                if (!cfg.SetChecked(UserStateKeys.QqAcceptNonUser, flag, schema, out error))
                {
                    return Results.Json(new { ok = false, error = error });
                }
            }
            if (qqIdsPresent)
            {
                if (!cfg.SetChecked(UserStateKeys.QqIds, qqIds.Trim(), schema, out error))
                {
                    return Results.Json(new { ok = false, error = error });
                }
            }
            LogStore.Add("CatHome4", 1, "用户态已写入：当前用户=" + CurrentUserName()
                + " | 接受非用户指令=" + (AcceptNonUserCommands() ? "true" : "false")
                + " | 群内身份 " + CurrentUserQqIds().Length.ToString() + " 项", "CONFIG");
            var resp = new
            {
                ok = true,
                current = CurrentUserName(),
                acceptNonUser = AcceptNonUserCommands(),
                qqIds = string.Join(",", CurrentUserQqIds())
            };
            return Results.Json(resp);
        }
    }
}
