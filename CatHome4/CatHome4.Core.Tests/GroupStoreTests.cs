using System;
using System.Collections.Generic;
using System.IO;
using CH4;
using Xunit;

namespace CatHome4.Core.Tests
{
    /// <summary>
    /// 组数据面（GroupStore）测试（A300）——组 CRUD / 成员按 key 去重 / 消息序号单调 / 落盘往返 / 坏行跳过 / 删组入回收。
    /// 静态存储 → GlobalToolState 集合串行（测试隔离纪律）；产物根参数化到临时目录。
    /// </summary>
    [Collection("GlobalToolState")]
    public class GroupStoreTests : IDisposable
    {
        /// <summary>测试临时目录——组根（Dispose 清理）</summary>
        private readonly string _dir;

        /// <summary>
        /// 建立测试环境——临时目录作为组根并加载。
        /// </summary>
        public GroupStoreTests()
        {
            _dir = Path.Combine(Path.GetTempPath(), "ch4-group-test-" + Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(_dir);
            GroupStore.Configure(_dir);
            GroupStore.Load();
        }

        /// <summary>
        /// 清理——解绑组根 + 删临时目录（清理失败不掩盖测试结论）。
        /// </summary>
        public void Dispose()
        {
            GroupStore.Configure("");
            try
            {
                if (Directory.Exists(_dir))
                {
                    Directory.Delete(_dir, true);
                }
            }
            catch (Exception ex)
            {
                // 清理失败不影响断言结论（临时目录随系统清理）
                Console.WriteLine("[测试清理] 组临时目录删除失败: " + ex.Message);
            }
        }

        /// <summary>
        /// 新建组——成员去空白 / 去重且保持序；默认启用；列表可见。
        /// </summary>
        [Fact]
        public void Create_List_ReturnsMembersAndDefaults()
        {
            string result = GroupStore.CreateOrUpdate("施工队", new string[] { "majordomo", "coder1", " ", "majordomo" }, "CH4");
            Assert.StartsWith("ok|", result, StringComparison.Ordinal);
            GroupSummary g = GroupStore.GetGroup("施工队");
            Assert.NotNull(g);
            if (g == null)
            {
                return;
            }
            Assert.Equal(new string[] { "majordomo", "coder1" }, g.MemberKeys);
            Assert.Equal("CH4", g.Project);
            Assert.True(g.Enabled);
            Assert.Single(GroupStore.ListGroups());
        }

        /// <summary>
        /// 组名非法——路径分隔 / 空名 / 超长一律拒绝（容器 Key 空间约束）。
        /// </summary>
        [Fact]
        public void Create_InvalidName_Rejected()
        {
            Assert.StartsWith("ERR|GROUP_NAME_INVALID", GroupStore.CreateOrUpdate("a/b", new string[] { "x" }, ""), StringComparison.Ordinal);
            Assert.StartsWith("ERR|GROUP_NAME_INVALID", GroupStore.CreateOrUpdate("", new string[] { "x" }, ""), StringComparison.Ordinal);
            Assert.StartsWith("ERR|GROUP_NAME_INVALID", GroupStore.CreateOrUpdate("有 空格", new string[] { "x" }, ""), StringComparison.Ordinal);
        }

        /// <summary>
        /// 空成员——组不允许无成员（拒绝且不建目录）。
        /// </summary>
        [Fact]
        public void Create_EmptyMembers_Rejected()
        {
            Assert.StartsWith("ERR|GROUP_MEMBERS_EMPTY", GroupStore.CreateOrUpdate("施工队", new string[0], ""), StringComparison.Ordinal);
            Assert.Empty(GroupStore.ListGroups());
        }

        /// <summary>
        /// 消息追加——seq 单调递增；after 游标与 limit 生效；统计同步。
        /// </summary>
        [Fact]
        public void Append_SeqMonotonic_AndReadAfter()
        {
            GroupStore.CreateOrUpdate("施工队", new string[] { "majordomo" }, "");
            Assert.StartsWith("ok|", GroupStore.AppendMessage("施工队", "majordomo", "delegation", "第一条", ""), StringComparison.Ordinal);
            Assert.StartsWith("ok|", GroupStore.AppendMessage("施工队", "coder1", "receipt", "收到", "d-1"), StringComparison.Ordinal);
            List<GroupMessage> all = GroupStore.ReadMessages("施工队", 0, 0);
            Assert.Equal(2, all.Count);
            Assert.Equal(1, all[0].Seq);
            Assert.Equal(2, all[1].Seq);
            Assert.Equal("delegation", all[0].Type);
            Assert.Equal("d-1", all[1].Refs);
            List<GroupMessage> after = GroupStore.ReadMessages("施工队", 1, 0);
            Assert.Single(after);
            Assert.Equal(2, after[0].Seq);
            GroupSummary g = GroupStore.GetGroup("施工队");
            Assert.NotNull(g);
            if (g == null)
            {
                return;
            }
            Assert.Equal(2, g.LastSeq);
            Assert.Equal(2, g.MsgCount);
        }

        /// <summary>
        /// 消息参数面——非法类型 / 空正文 / 组不存在一律拒绝（不静默落一行坏数据）。
        /// </summary>
        [Fact]
        public void Append_InvalidTypeOrText_Rejected()
        {
            GroupStore.CreateOrUpdate("施工队", new string[] { "majordomo" }, "");
            Assert.StartsWith("ERR|GROUP_TYPE_INVALID", GroupStore.AppendMessage("施工队", "majordomo", "bogus", "x", ""), StringComparison.Ordinal);
            Assert.StartsWith("ERR|GROUP_TEXT_EMPTY", GroupStore.AppendMessage("施工队", "majordomo", "note", "  ", ""), StringComparison.Ordinal);
            Assert.StartsWith("ERR|GROUP_NOT_FOUND", GroupStore.AppendMessage("不存在", "majordomo", "note", "x", ""), StringComparison.Ordinal);
            Assert.Empty(GroupStore.ReadMessages("施工队", 0, 0));
        }

        /// <summary>
        /// 落盘往返——重新加载后成员 / 项目 / 消息统计一致（重启续接）。
        /// </summary>
        [Fact]
        public void Persist_RoundTrip_KeepsSeqAndMembers()
        {
            GroupStore.CreateOrUpdate("施工队", new string[] { "majordomo", "coder1" }, "CH4");
            GroupStore.AppendMessage("施工队", "majordomo", "note", "重启前", "");
            GroupStore.Configure(_dir);
            GroupStore.Load();
            GroupSummary g = GroupStore.GetGroup("施工队");
            Assert.NotNull(g);
            if (g == null)
            {
                return;
            }
            Assert.Equal(1, g.LastSeq);
            Assert.Equal(1, g.MsgCount);
            Assert.Equal("CH4", g.Project);
            Assert.Equal(new string[] { "majordomo", "coder1" }, g.MemberKeys);
            Assert.Single(GroupStore.ReadMessages("施工队", 0, 0));
        }

        /// <summary>
        /// 坏行跳过——不阻断加载，统计只计合法行（坏行另行出声）。
        /// </summary>
        [Fact]
        public void Load_BadLine_SkippedAndCounted()
        {
            GroupStore.CreateOrUpdate("施工队", new string[] { "majordomo" }, "");
            GroupStore.AppendMessage("施工队", "majordomo", "note", "好行", "");
            string path = Path.Combine(_dir, "施工队", "messages.jsonl");
            File.AppendAllText(path, "{坏行}\n");
            GroupStore.Configure(_dir);
            GroupStore.Load();
            GroupSummary g = GroupStore.GetGroup("施工队");
            Assert.NotNull(g);
            if (g == null)
            {
                return;
            }
            Assert.Equal(1, g.MsgCount);
            Assert.Equal(1, g.LastSeq);
            Assert.Single(GroupStore.ReadMessages("施工队", 0, 0));
        }

        /// <summary>
        /// 启停——切换后落盘（重新加载保持停用）；不存在的组拒绝。
        /// </summary>
        [Fact]
        public void SetEnabled_TogglesAndPersists()
        {
            GroupStore.CreateOrUpdate("施工队", new string[] { "majordomo" }, "");
            Assert.StartsWith("ok|", GroupStore.SetEnabled("施工队", false), StringComparison.Ordinal);
            GroupSummary g = GroupStore.GetGroup("施工队");
            Assert.NotNull(g);
            if (g == null)
            {
                return;
            }
            Assert.False(g.Enabled);
            GroupStore.Configure(_dir);
            GroupStore.Load();
            GroupSummary reloaded = GroupStore.GetGroup("施工队");
            Assert.NotNull(reloaded);
            if (reloaded == null)
            {
                return;
            }
            Assert.False(reloaded.Enabled);
            Assert.StartsWith("ERR|GROUP_NOT_FOUND", GroupStore.SetEnabled("不存在", true), StringComparison.Ordinal);
        }

        /// <summary>
        /// 删组——整目录移入 _trash（可恢复）；缓存移除；再次删除报不存在。
        /// </summary>
        [Fact]
        public void Delete_MovesGroupToTrash()
        {
            GroupStore.CreateOrUpdate("施工队", new string[] { "majordomo" }, "");
            Assert.StartsWith("ok|", GroupStore.Delete("施工队"), StringComparison.Ordinal);
            Assert.Null(GroupStore.GetGroup("施工队"));
            Assert.True(Directory.Exists(Path.Combine(_dir, "_trash")));
            Assert.Empty(GroupStore.ListGroups());
            Assert.StartsWith("ERR|GROUP_NOT_FOUND", GroupStore.Delete("施工队"), StringComparison.Ordinal);
        }

        /// <summary>
        /// 读上限——limit 生效（超限截断在 MaxReadLimit 内）。
        /// </summary>
        [Fact]
        public void ReadMessages_LimitApplied()
        {
            GroupStore.CreateOrUpdate("施工队", new string[] { "majordomo" }, "");
            for (int i = 0; i < 5; i = i + 1)
            {
                GroupStore.AppendMessage("施工队", "majordomo", "note", "m" + i.ToString(), "");
            }
            Assert.Equal(2, GroupStore.ReadMessages("施工队", 0, 2).Count);
            Assert.Equal(5, GroupStore.ReadMessages("施工队", 0, 0).Count);
        }

        /// <summary>
        /// 已读推进——只前进（回退请求幂等保持原值）；落盘后重新加载保持；未读按游标派生。
        /// </summary>
        [Fact]
        public void MarkRead_AdvancesOnlyAndPersists()
        {
            GroupStore.CreateOrUpdate("施工队", new string[] { "majordomo", "coder1" }, "");
            GroupStore.AppendMessage("施工队", "majordomo", "note", "一", "");
            GroupStore.AppendMessage("施工队", "majordomo", "note", "二", "");
            GroupStore.AppendMessage("施工队", "majordomo", "note", "三", "");
            Assert.StartsWith("ok|", GroupStore.MarkRead("施工队", "coder1", 2), StringComparison.Ordinal);
            Assert.Equal(1, GroupStore.GetUnread("施工队", "coder1"));
            // 回退请求——幂等保持 2
            Assert.StartsWith("ok|", GroupStore.MarkRead("施工队", "coder1", 1), StringComparison.Ordinal);
            Assert.Equal(1, GroupStore.GetUnread("施工队", "coder1"));
            // 无游标成员 = 全部未读
            Assert.Equal(3, GroupStore.GetUnread("施工队", "majordomo"));
            // 越界推进收敛到最大序号
            GroupStore.MarkRead("施工队", "majordomo", 99);
            Assert.Equal(0, GroupStore.GetUnread("施工队", "majordomo"));
            // 落盘往返
            GroupStore.Configure(_dir);
            GroupStore.Load();
            Assert.Equal(1, GroupStore.GetUnread("施工队", "coder1"));
            Assert.Equal(0, GroupStore.GetUnread("施工队", "majordomo"));
        }

        /// <summary>
        /// 已读参数面——空猫 key / 负序号 / 组不存在一律拒绝。
        /// </summary>
        [Fact]
        public void MarkRead_InvalidArgs_Rejected()
        {
            GroupStore.CreateOrUpdate("施工队", new string[] { "majordomo" }, "");
            Assert.StartsWith("ERR|GROUP_CAT_EMPTY", GroupStore.MarkRead("施工队", "", 1), StringComparison.Ordinal);
            Assert.StartsWith("ERR|GROUP_SEQ_INVALID", GroupStore.MarkRead("施工队", "majordomo", -1), StringComparison.Ordinal);
            Assert.StartsWith("ERR|GROUP_NOT_FOUND", GroupStore.MarkRead("不存在", "majordomo", 1), StringComparison.Ordinal);
        }
    }
}
