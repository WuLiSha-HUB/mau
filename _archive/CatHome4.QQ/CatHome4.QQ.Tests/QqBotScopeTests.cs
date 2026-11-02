using CatHome4.QQ;
using Xunit;

namespace CatHome4.QQ.Tests
{
    /// <summary>
    /// QQBotConnection.BuildScope 测试（A215）——私聊 / 群聊端点段分派。
    /// 覆盖：私聊 openid / 群聊 gid:mid 取 gid 段 / 群聊无冒号回落整体 / 仅 private 走用户端点。
    /// </summary>
    public class QqBotScopeTests
    {
        /// <summary>
        /// 私聊——openid 原样拼入 /v2/users。
        /// </summary>
        [Fact]
        public void Private_UsersScope()
        {
            Assert.Equal("/v2/users/A1B2C3", QQBotConnection.BuildScope("private", "A1B2C3"));
        }

        /// <summary>
        /// 群聊——targetId 形如 gid:mid，取冒号前的 gid 段。
        /// </summary>
        [Fact]
        public void Group_GidMid_SlicesGid()
        {
            Assert.Equal("/v2/groups/G123", QQBotConnection.BuildScope("group", "G123:MSG456"));
        }

        /// <summary>
        /// 群聊——无冒号时整体作为 gid（回落不截断）。
        /// </summary>
        [Fact]
        public void Group_NoColon_WholeAsGid()
        {
            Assert.Equal("/v2/groups/G123", QQBotConnection.BuildScope("group", "G123"));
        }

        /// <summary>
        /// 非 private 一律走群聊分派——类型判据为白名单式（仅 private 例外）。
        /// </summary>
        [Fact]
        public void NonPrivate_FallsToGroupScope()
        {
            Assert.Equal("/v2/groups/G123", QQBotConnection.BuildScope("", "G123:MSG456"));
        }
    }
}
