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
        /// 管理员投影——adminKeys 落盘并在 GetGroup / ListGroups 投影中保留；非成员项自动剔除（派单权限判据源）。
        /// </summary>
        [Fact]
        public void Create_AdminKeys_SurviveProjection()
        {
            string result = GroupStore.CreateOrUpdate("施工队", new string[] { "majordomo", "coder1" }, "", new string[] { "majordomo", "outsider" });
            Assert.StartsWith("ok|", result, StringComparison.Ordinal);
            GroupSummary g = GroupStore.GetGroup("施工队");
            Assert.NotNull(g);
            if (g == null)
            {
                return;
            }
            Assert.Equal(new string[] { "majordomo" }, g.AdminKeys);
            List<GroupSummary> list = GroupStore.ListGroups();
            Assert.Single(list);
            Assert.Equal(new string[] { "majordomo" }, list[0].AdminKeys);
        }
        /// <summary>
        /// 订阅名单与游标——subscriberKeys 只保留成员；游标缺省 0、推进后可读回；静默阈值缺省 300 秒。
        /// </summary>
        [Fact]
        public void Subscribers_KeepMembers_And_CursorRoundTrip()
        {
            GroupStore.CreateOrUpdate("施工队", new string[] { "majordomo", "coder1" }, "", null, new string[] { "coder1", "outsider" });
            GroupSummary g = GroupStore.GetGroup("施工队");
            Assert.NotNull(g);
            if (g == null)
            {
                return;
            }
            Assert.Equal(new string[] { "coder1" }, g.SubscriberKeys);
            Assert.Equal(0, GroupStore.GetSubscriberCursor("施工队", "coder1"));
            Assert.Equal(GroupStore.DefaultSubscribeIdleSec, GroupStore.GetSubscribeIdleSec(g));
            Assert.StartsWith("ok|", GroupStore.SetSubscriberCursor("施工队", "coder1", 7), StringComparison.Ordinal);
            Assert.Equal(7, GroupStore.GetSubscriberCursor("施工队", "coder1"));
        }

        /// <summary>
        /// 游标收敛——取消订阅删游标；重新订阅从当前末序号起（不补历史）；原订阅者游标保留。
        /// </summary>
        [Fact]
        public void Cursors_Reseed_On_SubscriptionChange()
        {
            GroupStore.CreateOrUpdate("施工队", new string[] { "majordomo", "coder1" }, "", null, new string[] { "coder1" });
            Assert.Equal(0, GroupStore.GetSubscriberCursor("施工队", "coder1"));
            Assert.StartsWith("ok|", GroupStore.AppendMessage("施工队", "majordomo", "note", "第一条", ""), StringComparison.Ordinal);
            Assert.StartsWith("ok|", GroupStore.AppendMessage("施工队", "majordomo", "note", "第二条", ""), StringComparison.Ordinal);
            Assert.StartsWith("ok|", GroupStore.SetSubscriberCursor("施工队", "coder1", 1), StringComparison.Ordinal);
            // 改组不动原订阅者游标
            GroupStore.CreateOrUpdate("施工队", new string[] { "majordomo", "coder1" }, "", null, new string[] { "coder1" });
            Assert.Equal(1, GroupStore.GetSubscriberCursor("施工队", "coder1"));
            // 取消订阅 → 游标删除
            GroupStore.CreateOrUpdate("施工队", new string[] { "majordomo", "coder1" }, "", null, new string[0]);
            GroupSummary g = GroupStore.GetGroup("施工队");
            Assert.NotNull(g);
            if (g == null)
            {
                return;
            }
            Assert.False(g.SubscribeCursors.ContainsKey("coder1"));
            Assert.Equal(0, GroupStore.GetSubscriberCursor("施工队", "coder1"));
            // 重新订阅 → 从当前末序号（2）起，不补历史
            GroupStore.CreateOrUpdate("施工队", new string[] { "majordomo", "coder1" }, "", null, new string[] { "coder1" });
            Assert.Equal(2, GroupStore.GetSubscriberCursor("施工队", "coder1"));
        }

        /// <summary>
        /// 发送方校验——非成员署名拒绝；空发送方（人 / 外部通道）放行。
        /// </summary>
        [Fact]
        public void Append_FromNotMember_Rejected()
        {
            GroupStore.CreateOrUpdate("施工队", new string[] { "majordomo" }, "");
            Assert.StartsWith("ERR|GROUP_FROM_NOT_MEMBER", GroupStore.AppendMessage("施工队", "outsider", "note", "冒充", ""), StringComparison.Ordinal);
            Assert.StartsWith("ok|", GroupStore.AppendMessage("施工队", "", "note", "人说的", ""), StringComparison.Ordinal);
        }

        /// <summary>
        /// 尾部读取——超 limit 返回最后 N 条并置 hasMore；不足时 hasMore=false。
        /// </summary>
        [Fact]
        public void ReadTail_ReturnsLastAndHasMore()
        {
            GroupStore.CreateOrUpdate("施工队", new string[] { "majordomo" }, "");
            for (int i = 1; i <= 5; i = i + 1)
            {
                Assert.StartsWith("ok|", GroupStore.AppendMessage("施工队", "majordomo", "note", "第" + i.ToString() + "条", ""), StringComparison.Ordinal);
            }
            bool hasMore = false;
            List<GroupMessage> tail = GroupStore.ReadTailMessages("施工队", 3, out hasMore);
            Assert.True(hasMore);
            Assert.Equal(3, tail.Count);
            Assert.Equal(3, tail[0].Seq);
            Assert.Equal(5, tail[2].Seq);
            List<GroupMessage> all = GroupStore.ReadTailMessages("施工队", 10, out hasMore);
            Assert.False(hasMore);
            Assert.Equal(5, all.Count);
        }

        /// <summary>
        /// 清空消息流——旧流原地归档为 .bak；统计与订阅游标归零；新流 seq 从 1 起重开。
        /// </summary>
        [Fact]
        public void ClearMessages_ArchivesAndResets()
        {
            GroupStore.CreateOrUpdate("施工队", new string[] { "majordomo", "coder1" }, "", null, new string[] { "coder1" });
            Assert.StartsWith("ok|", GroupStore.AppendMessage("施工队", "majordomo", "note", "第一条", ""), StringComparison.Ordinal);
            Assert.StartsWith("ok|", GroupStore.AppendMessage("施工队", "majordomo", "note", "第二条", ""), StringComparison.Ordinal);
            Assert.StartsWith("ok|", GroupStore.SetSubscriberCursor("施工队", "coder1", 2), StringComparison.Ordinal);
            string result = GroupStore.ClearMessages("施工队");
            Assert.StartsWith("ok|施工队|messages.", result, StringComparison.Ordinal);
            Assert.EndsWith(".jsonl.bak", result, StringComparison.Ordinal);
            GroupSummary g = GroupStore.GetGroup("施工队");
            Assert.NotNull(g);
            if (g == null)
            {
                return;
            }
            Assert.Equal(0, g.LastSeq);
            Assert.Equal(0, g.MsgCount);
            Assert.Empty(g.SubscribeCursors);
            string archivedName = result.Split('|')[2];
            Assert.True(File.Exists(Path.Combine(_dir, "施工队", archivedName)));
            Assert.StartsWith("ok|", GroupStore.AppendMessage("施工队", "majordomo", "note", "清空后第一条", ""), StringComparison.Ordinal);
            List<GroupMessage> all = GroupStore.ReadMessages("施工队", 0, 0);
            Assert.Single(all);
            Assert.Equal(1, all[0].Seq);
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
            GroupStore.CreateOrUpdate("施工队", new string[] { "majordomo", "coder1" }, "");
            Assert.StartsWith("ok|", GroupStore.AppendMessage("施工队", "majordomo", "delegation", "第一条", "", "coder1"), StringComparison.Ordinal);
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
        /// 停用门禁——停用组追加消息被拒（错误码明确「未启用」）；重新启用后恢复可写。
        /// </summary>
        [Fact]
        public void DisabledGroup_AppendRejected()
        {
            GroupStore.CreateOrUpdate("停用队", new string[] { "majordomo" }, "");
            Assert.True(GroupStore.IsRunning("停用队"));
            Assert.StartsWith("ok|", GroupStore.SetEnabled("停用队", false), StringComparison.Ordinal);
            Assert.StartsWith("ok|", GroupStore.StopGroup("停用队"), StringComparison.Ordinal);
            Assert.False(GroupStore.IsRunning("停用队"));
            Assert.StartsWith("ERR|GROUP_NOT_RUNNING", GroupStore.AppendMessage("停用队", "majordomo", "note", "hi", "", ""), StringComparison.Ordinal);
            Assert.StartsWith("ok|", GroupStore.StartGroup("停用队"), StringComparison.Ordinal);
            Assert.True(GroupStore.IsRunning("停用队"));
            Assert.StartsWith("ok|", GroupStore.AppendMessage("停用队", "majordomo", "note", "hi", "", ""), StringComparison.Ordinal);
        }
        /// <summary>
        /// 启动加载——停用组保持轻量条目（不扫消息流、统计归零）；运行态为 false。
        /// </summary>
        [Fact]
        public void Load_DisabledGroup_KeepsLightEntry()
        {
            GroupStore.CreateOrUpdate("轻量队", new string[] { "majordomo" }, "");
            Assert.StartsWith("ok|", GroupStore.AppendMessage("轻量队", "majordomo", "note", "a", "", ""), StringComparison.Ordinal);
            Assert.StartsWith("ok|", GroupStore.AppendMessage("轻量队", "majordomo", "note", "b", "", ""), StringComparison.Ordinal);
            Assert.StartsWith("ok|", GroupStore.SetEnabled("轻量队", false), StringComparison.Ordinal);
            GroupStore.Configure(_dir);
            GroupStore.Load();
            GroupSummary g = GroupStore.GetGroup("轻量队");
            Assert.NotNull(g);
            if (g == null)
            {
                return;
            }
            Assert.False(g.Running);
            Assert.Equal(0, g.MsgCount);
            Assert.Equal(0, g.LastSeq);
        }
        /// <summary>
        /// 投影副本必须携带运行态——启用组 GetGroup 返回的副本 Running 为 true（防新增字段漏拷）。
        /// </summary>
        [Fact]
        public void GetGroup_ProjectionCarriesRunning()
        {
            GroupStore.CreateOrUpdate("投影队", new string[] { "majordomo" }, "");
            GroupSummary g = GroupStore.GetGroup("投影队");
            Assert.NotNull(g);
            if (g == null)
            {
                return;
            }
            Assert.True(g.Running);
        }
    }
}
