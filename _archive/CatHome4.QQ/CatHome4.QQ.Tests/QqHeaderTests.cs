using CatHome4.Contracts;
using Mau.Runtime;
using Xunit;

namespace CatHome4.QQ.Tests
{
    /// <summary>
    /// QQ 消息头身份标记测试（design-ch4-user-state）——私聊用户名 + 群@ 本人 / 非用户标记。
    /// 覆盖：私聊用当前用户显示名 · 配置缺失回落缺省名 · 群@ 无身份清单 = 非用户 · 清单命中昵称 / 成员 ID = 本人
    ///       · 接受非用户指令开关 = 追加「·视为指令」· 昵称缺失仍带身份标记。
    /// 配置面：用例内自持 ConfigStore 绑定 DataBox（内存态，不落盘）。
    /// </summary>
    public class QqHeaderTests
    {
        /// <summary>
        /// 群@来源构造——昵称 + 角色 + 成员 ID。
        /// </summary>
        /// <param name="name">发送者昵称</param>
        /// <param name="role">群内角色原值</param>
        /// <param name="memberId">member_openid</param>
        /// <returns>来源载荷</returns>
        private static QqSource GroupSource(string name, string role, string memberId)
        {
            return new QqSource("group", "gid:" + memberId, "msg1", name, role, memberId, "");
        }

        /// <summary>
        /// 用户态配置注入——绑定 DataBox 的 ConfigStore（内存态）。
        /// </summary>
        /// <param name="current">当前用户显示名</param>
        /// <param name="acceptNonUser">接受非用户指令（true / false 文本）</param>
        /// <param name="qqIds">群内身份清单</param>
        private static void BindUserState(string current, string acceptNonUser, string qqIds)
        {
            ConfigStore cfg = new ConfigStore();
            cfg.Set(UserStateKeys.Current, current);
            cfg.Set(UserStateKeys.QqAcceptNonUser, acceptNonUser);
            cfg.Set(UserStateKeys.QqIds, qqIds);
            DataBox.Bind<ConfigStore>(cfg);
        }

        /// <summary>
        /// 私聊——消息头用当前用户显示名。
        /// </summary>
        [Fact]
        public void PrivateChat_UsesCurrentUserName()
        {
            BindUserState("Rushiera", "false", "");
            QqSource source = new QqSource("private", "uid1", "msg1", "任意昵称");
            Assert.Equal("[私聊|Rushiera]", QQBotService.BuildHeader(source));
        }

        /// <summary>
        /// 配置缺失——回落缺省用户名（莎）。
        /// </summary>
        [Fact]
        public void PrivateChat_FallsBackToDefaultName()
        {
            DataBox.Bind<ConfigStore>(new ConfigStore());
            QqSource source = new QqSource("private", "uid1", "msg1", "");
            Assert.Equal("[私聊|" + UserStateKeys.DefaultUserName + "]", QQBotService.BuildHeader(source));
        }

        /// <summary>
        /// 群@——身份清单为空 = 一律非用户（带标记）。
        /// </summary>
        [Fact]
        public void GroupChat_NoIdentityList_MarkedNonUser()
        {
            BindUserState("莎", "false", "");
            Assert.Equal("[群@|张三(成员)·非用户]", QQBotService.BuildHeader(GroupSource("张三", "member", "mid1")));
        }

        /// <summary>
        /// 群@——昵称命中身份清单 = 本人（不加非用户标记）。
        /// </summary>
        [Fact]
        public void GroupChat_NicknameHit_MarkedSelf()
        {
            BindUserState("莎", "false", "雾理莎");
            Assert.Equal("[群@|雾理莎(群主)·本人]", QQBotService.BuildHeader(GroupSource("雾理莎", "owner", "mid2")));
        }

        /// <summary>
        /// 群@——成员 ID 命中身份清单 = 本人（昵称不同也认）。
        /// </summary>
        [Fact]
        public void GroupChat_MemberIdHit_MarkedSelf()
        {
            BindUserState("莎", "false", "mid9");
            Assert.Equal("[群@|路人甲(成员)·本人]", QQBotService.BuildHeader(GroupSource("路人甲", "member", "mid9")));
        }

        /// <summary>
        /// 群@——接受非用户指令开关打开 = 非用户 + 「·视为指令」。
        /// </summary>
        [Fact]
        public void GroupChat_AcceptNonUser_AddsAuthorizedMark()
        {
            BindUserState("莎", "true", "");
            Assert.Equal("[群@|张三(成员)·非用户·视为指令]", QQBotService.BuildHeader(GroupSource("张三", "member", "mid3")));
        }

        /// <summary>
        /// 群@——昵称缺失仍带身份标记（标记不依赖昵称）。
        /// </summary>
        [Fact]
        public void GroupChat_NoName_KeepsIdentityMark()
        {
            BindUserState("莎", "false", "");
            Assert.Equal("[群@·非用户]", QQBotService.BuildHeader(GroupSource("", "", "mid4")));
        }
    }
}
