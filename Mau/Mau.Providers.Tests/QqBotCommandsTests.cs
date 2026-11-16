using System;
using System.Collections.Generic;
using Xunit;

namespace Mau.Providers.Tests
{
    /// <summary>
    /// 硬编码命令测试（A386）——/botinfo 的群真实名与权限三态、/homeinfo 的 JSON 转 Markdown。
    /// 判据来源：群里看到的 /botinfo 只有 OpenID 且取不到官方数据时一律报「关闭」；
    /// /homeinfo 直接贴宿主 info 的整块 JSON，群里人读不了。
    /// </summary>
    public class QqBotCommandsTests
    {
        /// <summary>测试群 OpenID（末 8 位 = F8A00F0A）。</summary>
        private const string GroupA = "AAAACCCC11110000FFFF8888F8A00F0A";

        /// <summary>
        /// bot_state 失败体（11253 接口需白名单）——官方数据取不到。
        /// </summary>
        /// <param name="openId">群 OpenID</param>
        /// <returns>JSON 文本</returns>
        private static string StateDenied(string openId)
        {
            return "{\"code\":11253,\"message\":\"接口需白名单\"}";
        }

        /// <summary>
        /// bot_state 成功体——故意不带 allow_proactive_msg（权限未知）。
        /// </summary>
        /// <param name="openId">群 OpenID</param>
        /// <returns>JSON 文本</returns>
        private static string StateNoProactive(string openId)
        {
            return "{\"member_role\":\"member\",\"recv_msg_setting\":\"all\"}";
        }

        /// <summary>
        /// /homeinfo——固定输出宿主环境三块（版本 / 时间 / 系统），不依赖任何注入面。
        /// 反向判据：会话态数据（受控根 / 计量 / 加载包）一个都不许出现。
        /// </summary>
        [Fact]
        public void HomeInfoRendersHostEnvBlocksOnly()
        {
            QqBotCommandContext ctx = new QqBotCommandContext();
            string text = QqBotCommands.Handle("/homeinfo", ctx);
            Assert.True(text.StartsWith("# CH4 宿主状态", StringComparison.Ordinal), "缺 Markdown 主标题：" + text);
            Assert.True(text.Contains("## 版本"), "缺版本块：" + text);
            Assert.True(text.Contains("## 时间"), "缺时间块：" + text);
            Assert.True(text.Contains("## 系统"), "缺系统块：" + text);
            Assert.True(text.Contains("**编译时刻**"), "缺编译时刻：" + text);
            Assert.True(text.Contains("**当前时间**"), "缺当前时间：" + text);
            Assert.True(text.Contains("**运行时**"), "缺运行时：" + text);
            Assert.False(text.Contains("受控根"), "会话态块（受控根）不该出现：" + text);
            Assert.False(text.Contains("前文计量"), "会话态块（计量）不该出现：" + text);
            Assert.False(text.Contains("加载包"), "会话态块（加载包）不该出现：" + text);
        }
        /// <summary>
        /// /homeinfo——在线机器人以 Markdown 表格渲染（名称 + AppID）；空清单给显式空态。
        /// </summary>
        [Fact]
        public void HomeInfoRendersOnlineBotsTable()
        {
            QqBotCommandContext ctx = new QqBotCommandContext();
            QqBotOnlineBot one = new QqBotOnlineBot();
            one.DisplayName = "CatHome招待所前台";
            one.AppId = "1905759910";
            ctx.OnlineBots.Add(one);
            QqBotOnlineBot two = new QqBotOnlineBot();
            two.DisplayName = "鲸子";
            two.AppId = "1905593657";
            ctx.OnlineBots.Add(two);
            string text = QqBotCommands.Handle("/homeinfo", ctx);
            Assert.True(text.Contains("## 在线机器人"), "缺在线机器人块：" + text);
            Assert.True(text.Contains("| 名称 | AppID |"), "缺表格头：" + text);
            Assert.True(text.Contains("| 鲸子 | 1905593657 |"), "缺第二行：" + text);
            QqBotCommandContext blank = new QqBotCommandContext();
            string blankText = QqBotCommands.Handle("/homeinfo", blank);
            Assert.True(blankText.Contains("（无在线机器人）"), "空清单未给显式空态：" + blankText);
        }

        /// <summary>
        /// /botinfo——官方接口不可用时群标题用真实群名、权限取目录缓存。
        /// </summary>
        [Fact]
        public void BotInfoShowsGroupTitleAndCachedPermission()
        {
            QqBotCommandContext ctx = new QqBotCommandContext();
            ctx.DisplayName = "猫窝机器人";
            ctx.AppId = "1905759910";
            ctx.KnownGroups.Add(GroupA);
            QqBotGroupEntry entry = new QqBotGroupEntry();
            entry.OpenId = GroupA;
            entry.Title = "猫窝大队";
            entry.Role = "member";
            entry.RecvSetting = "all";
            entry.AllowProactive = true;
            entry.LastSeen = "2026-11-16 18:00:00";
            ctx.GroupEntries[GroupA] = entry;
            ctx.BotStateFetcher = StateDenied;
            string text = QqBotCommands.Handle("/botinfo", ctx);
            Assert.True(text.Contains("## 群 猫窝大队"), "群标题未用群真实名：" + text);
            Assert.False(text.Contains(GroupA), "仍输出完整 OpenID：" + text);
            Assert.True(text.Contains("主动发言**：允许"), "降级未取目录缓存权限：" + text);
            Assert.True(text.Contains("取自本地观测"), "降级未标注数据来源：" + text);
        }

        /// <summary>
        /// /botinfo——目录里没有群名时退 OpenID 末 8 位。
        /// </summary>
        [Fact]
        public void BotInfoFallsBackToTail8WhenTitleMissing()
        {
            QqBotCommandContext ctx = new QqBotCommandContext();
            ctx.DisplayName = "猫窝机器人";
            ctx.AppId = "1905759910";
            ctx.KnownGroups.Add(GroupA);
            QqBotGroupEntry entry = new QqBotGroupEntry();
            entry.OpenId = GroupA;
            ctx.GroupEntries[GroupA] = entry;
            ctx.BotStateFetcher = StateDenied;
            string text = QqBotCommands.Handle("/botinfo", ctx);
            Assert.True(text.Contains("## 群 F8A00F0A"), "无群名时未退末 8 位：" + text);
            Assert.False(text.Contains(GroupA), "仍输出完整 OpenID：" + text);
        }

        /// <summary>
        /// /botinfo——官方体缺 allow_proactive_msg 时报未知，不报「不允许」。
        /// </summary>
        [Fact]
        public void BotInfoReportsUnknownProactiveInsteadOfClosed()
        {
            QqBotCommandContext ctx = new QqBotCommandContext();
            ctx.DisplayName = "猫窝机器人";
            ctx.AppId = "1905759910";
            ctx.KnownGroups.Add(GroupA);
            ctx.BotStateFetcher = StateNoProactive;
            string text = QqBotCommands.Handle("/botinfo", ctx);
            Assert.True(text.Contains("主动发言**：未知"), "字段缺失被报成否定：" + text);
            Assert.False(text.Contains("主动发言**：不允许"), "字段缺失被报成「不允许」：" + text);
        }
    }
}
