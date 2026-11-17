using System;
using System.Collections.Generic;
using System.IO;
using CH4;
using Xunit;

namespace CatHome4.Core.Tests
{
    /// <summary>
    /// 延迟指令队列测试（design-ch4-delay §八）——假时钟：到点投递 / 保序 / 积压一次性注入 / 上限 / 改时刻 / 取消 / 越权 / 持久化往返。
    /// 静态队列 → GlobalToolState 集合串行（测试隔离纪律）；落盘产物根参数化到临时目录。
    /// </summary>
    [Collection("GlobalToolState")]
    public class DelayQueueTests : IDisposable
    {
        /// <summary>测试临时目录——落盘往返用（Dispose 清理）</summary>
        private readonly string _dir;

        /// <summary>假时钟当前值——毫秒</summary>
        private long _now = 1000000;

        /// <summary>
        /// 建立测试环境——重置队列 + 注入假时钟 + 临时目录。
        /// </summary>
        public DelayQueueTests()
        {
            DelayQueue.ResetForTest();
            DelayQueue.NowProvider = FakeNow;
            _dir = Path.Combine(Path.GetTempPath(), "ch4-delay-test-" + Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(_dir);
        }

        /// <summary>
        /// 清理——重置队列 + 删临时目录（失败不掩盖测试结论）。
        /// </summary>
        public void Dispose()
        {
            DelayQueue.ResetForTest();
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
                Console.WriteLine("[测试清理] 延迟队列临时目录删除失败: " + ex.Message);
            }
        }

        /// <summary>假时钟——返回可控当前毫秒</summary>
        /// <returns>当前毫秒</returns>
        private long FakeNow()
        {
            return _now;
        }

        /// <summary>
        /// 到点前不投递、到点后投递——时间戳驱动（无内部计数）。
        /// </summary>
        [Fact]
        public void Pump_DeliversOnlyWhenDue()
        {
            DelayQueue.Add("catA", "hello", "delay", _now + 1000);
            List<string> got = new List<string>();
            int first = DelayQueue.Pump(delegate (string cat, string content, string source) { got.Add(content); return true; });
            Assert.Equal(0, first);
            Assert.Empty(got);
            _now = _now + 1000;
            int second = DelayQueue.Pump(delegate (string cat, string content, string source) { got.Add(content); return true; });
            Assert.Equal(1, second);
            Assert.Single(got);
            Assert.Equal("hello", got[0]);
            Assert.Empty(DelayQueue.List("catA"));
        }

        /// <summary>
        /// 保序——多条到点按 DueAt 升序投递（同刻按登记序）。
        /// </summary>
        [Fact]
        public void Pump_DeliversInDueOrder()
        {
            DelayQueue.Add("catA", "third", "delay", _now + 3000);
            DelayQueue.Add("catA", "first", "delay", _now + 1000);
            DelayQueue.Add("catA", "second", "delay", _now + 2000);
            _now = _now + 5000;
            List<string> got = new List<string>();
            int count = DelayQueue.Pump(delegate (string cat, string content, string source) { got.Add(content); return true; });
            Assert.Equal(3, count);
            Assert.Equal("first", got[0]);
            Assert.Equal("second", got[1]);
            Assert.Equal("third", got[2]);
        }

        /// <summary>
        /// 积压一次性注入——停机期间到期的多条在单帧内按序全部投递（不丢不并不覆盖）。
        /// </summary>
        [Fact]
        public void Pump_BacklogBurstInOneFrame()
        {
            DelayQueue.Add("catA", "a", "delay", _now - 5000);
            DelayQueue.Add("catA", "b", "delay", _now - 3000);
            DelayQueue.Add("catA", "c", "delay", _now + 1000);
            List<string> got = new List<string>();
            int count = DelayQueue.Pump(delegate (string cat, string content, string source) { got.Add(content); return true; });
            Assert.Equal(2, count);
            Assert.Equal(2, got.Count);
            Assert.Equal("a", got[0]);
            Assert.Equal("b", got[1]);
        }

        /// <summary>
        /// 未受理条目保留——投递委托返回 false 时条目留在表中待重投（停机态语义）。
        /// </summary>
        [Fact]
        public void Pump_RejectedEntryStaysQueued()
        {
            DelayQueue.Add("catA", "keep", "restart", _now);
            int rejected = DelayQueue.Pump(delegate (string cat, string content, string source) { return false; });
            Assert.Equal(0, rejected);
            Assert.Single(DelayQueue.List("catA"));
            List<string> got = new List<string>();
            int delivered = DelayQueue.Pump(delegate (string cat, string content, string source) { got.Add(content); return true; });
            Assert.Equal(1, delivered);
            Assert.Equal("keep", got[0]);
        }

        /// <summary>
        /// 单会话上限——第 51 条拒绝并给出 DELAY_LIMIT（其他会话不受影响）。
        /// </summary>
        [Fact]
        public void Add_RejectsOverLimit()
        {
            for (int i = 0; i < DelayQueue.MaxPerSession; i = i + 1)
            {
                string ok = DelayQueue.Add("catA", "item" + i.ToString(), "delay", _now + 10000);
                Assert.StartsWith("{\"ok\":true", ok);
            }
            string over = DelayQueue.Add("catA", "overflow", "delay", _now + 10000);
            Assert.StartsWith("ERR|DELAY_LIMIT", over);
            string other = DelayQueue.Add("catB", "fine", "delay", _now + 10000);
            Assert.StartsWith("{\"ok\":true", other);
        }

        /// <summary>
        /// 参数面零容忍——空内容 / 非法时刻拒绝。
        /// </summary>
        [Fact]
        public void Add_RejectsBadArgs()
        {
            Assert.StartsWith("ERR|BAD_ARGS", DelayQueue.Add("catA", "", "delay", _now + 1000));
            Assert.StartsWith("ERR|BAD_ARGS", DelayQueue.Add("catA", "x", "delay", 0));
            Assert.StartsWith("ERR|DELAY_NO_CAT", DelayQueue.Add("", "x", "delay", _now + 1000));
        }

        /// <summary>
        /// 改时刻——重设 DueAt 后按新时刻投递（旧时刻不再触发）。
        /// </summary>
        [Fact]
        public void SetDueAt_ReschedulesEntry()
        {
            string added = DelayQueue.Add("catA", "resched", "delay", _now + 1000);
            Assert.StartsWith("{\"ok\":true", added);
            long id = DelayQueue.List("catA")[0].Id;
            string set = DelayQueue.SetDueAt("catA", id, _now + 5000);
            Assert.StartsWith("ok", set);
            List<string> got = new List<string>();
            _now = _now + 1000;
            int none = DelayQueue.Pump(delegate (string cat, string content, string source) { got.Add(content); return true; });
            Assert.Equal(0, none);
            _now = _now + 4000;
            int one = DelayQueue.Pump(delegate (string cat, string content, string source) { got.Add(content); return true; });
            Assert.Equal(1, one);
            Assert.Equal("resched", got[0]);
        }

        /// <summary>
        /// 取消——条目移除且不再投递；跨会话取消拒绝（越权出声）。
        /// </summary>
        [Fact]
        public void Cancel_RemovesEntryAndRejectsCrossSession()
        {
            DelayQueue.Add("catA", "drop", "delay", _now + 1000);
            long id = DelayQueue.List("catA")[0].Id;
            Assert.StartsWith("ERR|DELAY_NOT_FOUND", DelayQueue.Cancel("catB", id));
            Assert.StartsWith("ok", DelayQueue.Cancel("catA", id));
            Assert.Empty(DelayQueue.List("catA"));
            _now = _now + 2000;
            List<string> got = new List<string>();
            int count = DelayQueue.Pump(delegate (string cat, string content, string source) { got.Add(content); return true; });
            Assert.Equal(0, count);
            Assert.Empty(got);
        }

        /// <summary>
        /// 持久化往返——落盘后重载条目仍在（跨宿主重启保留语义）；序号不回退。
        /// </summary>
        [Fact]
        public void Persist_RoundTrip()
        {
            string path = Path.Combine(_dir, "delays.json");
            DelayQueue.Configure(path);
            DelayQueue.Add("catA", "survive", "sleep", _now + 10000);
            long firstId = DelayQueue.List("catA")[0].Id;
            // 模拟宿主重启——内存清零后按落盘路径重载
            DelayQueue.ResetForTest();
            DelayQueue.NowProvider = FakeNow;
            DelayQueue.Configure(path);
            DelayQueue.Load();
            DelayEntry[] loaded = DelayQueue.List("catA");
            Assert.Single(loaded);
            Assert.Equal("survive", loaded[0].Content);
            Assert.Equal("sleep", loaded[0].Source);
            Assert.Equal(firstId, loaded[0].Id);
            // 重载后登记新条目——序号继续递增（不回退）
            DelayQueue.Add("catA", "next", "delay", _now + 20000);
            long nextId = 0;
            DelayEntry[] all = DelayQueue.List("catA");
            for (int i = 0; i < all.Length; i = i + 1)
            {
                if (all[i].Content == "next")
                {
                    nextId = all[i].Id;
                }
            }
            Assert.True(nextId > firstId);
        }

        /// <summary>
        /// 列表 JSON——含 now 与绝对 dueAt（前端倒计时数据源）。
        /// </summary>
        [Fact]
        public void BuildListJson_CarriesAbsoluteTimestamps()
        {
            DelayQueue.Add("catA", "json", "delay", _now + 7000);
            string json = DelayQueue.BuildListJson("catA");
            Assert.Contains("\"ok\":true", json);
            Assert.Contains("\"now\":" + _now.ToString(), json);
            Assert.Contains("\"dueAt\":" + (_now + 7000).ToString(), json);
            Assert.Contains("\"content\":\"json\"", json);
            string empty = DelayQueue.BuildListJson("catX");
            Assert.Contains("\"entries\":[]", empty);
        }

        /// <summary>
        /// 循环重排——loop 条目触发后不出表：按「原预定点 + 周期」推进相位 + Fired 累计。
        /// </summary>
        [Fact]
        public void Pump_LoopReschedulesAndCountsFired()
        {
            DelayQueue.Add("catA", "tick", "timer", _now + 1000, 5000, true);
            List<string> got = new List<string>();
            _now = _now + 1000;
            int first = DelayQueue.Pump(delegate (string cat, string content, string source) { got.Add(content); return true; });
            Assert.Equal(1, first);
            DelayEntry[] after = DelayQueue.List("catA");
            Assert.Single(after);
            Assert.True(after[0].Loop);
            Assert.Equal(1, after[0].Fired);
            Assert.Equal(_now + 5000, after[0].DueAt);
            int none = DelayQueue.Pump(delegate (string cat, string content, string source) { got.Add(content); return true; });
            Assert.Equal(0, none);
            _now = _now + 5000;
            int second = DelayQueue.Pump(delegate (string cat, string content, string source) { got.Add(content); return true; });
            Assert.Equal(1, second);
            Assert.Equal(2, DelayQueue.List("catA")[0].Fired);
        }

        /// <summary>
        /// 循环重排按时间轴相位——停机期间错过的周期跳过不补，下一次落在「原预定点 + N 周期」的首个未来点（不按投递时刻重算）。
        /// </summary>
        [Fact]
        public void Pump_LoopRescheduleKeepsWallClockPhase()
        {
            DelayQueue.Add("catA", "late", "timer", _now + 1000, 60000, true);
            _now = _now + 600000;
            int count = DelayQueue.Pump(delegate (string cat, string content, string source) { return true; });
            Assert.Equal(1, count);
            Assert.Equal(_now + 1000, DelayQueue.List("catA")[0].DueAt);
        }

        /// <summary>
        /// 按来源批量取消——只清指定来源（sleep），其余来源与其他会话不受影响。
        /// </summary>
        [Fact]
        public void CancelBySource_RemovesOnlyMatchingSource()
        {
            DelayQueue.Add("catA", "wait1", "sleep", _now + 1000);
            DelayQueue.Add("catA", "wait2", "sleep", _now + 2000);
            DelayQueue.Add("catA", "alarm", "timer", _now + 3000);
            DelayQueue.Add("catB", "other", "sleep", _now + 4000);
            DelayEntry[] killed = DelayQueue.CancelBySource("catA", "sleep");
            Assert.Equal(2, killed.Length);
            Assert.Equal("wait1", killed[0].Content);
            Assert.Equal("wait2", killed[1].Content);
            DelayEntry[] left = DelayQueue.List("catA");
            Assert.Single(left);
            Assert.Equal("alarm", left[0].Content);
            Assert.Single(DelayQueue.List("catB"));
            Assert.Empty(DelayQueue.CancelBySource("catA", "sleep"));
        }

        /// <summary>
        /// 循环标记切换——无循环时长的条目拒绝开启；跨会话拒绝；有则切换成功。
        /// </summary>
        [Fact]
        public void SetLoop_TogglesAndRejectsWithoutInterval()
        {
            DelayQueue.Add("catA", "once", "delay", _now + 1000, 0, false);
            DelayQueue.Add("catA", "cyc", "delay", _now + 2000, 30000, false);
            long onceId = DelayQueue.List("catA")[0].Id;
            long cycId = DelayQueue.List("catA")[1].Id;
            Assert.StartsWith("ERR|BAD_ARGS", DelayQueue.SetLoop("catA", onceId, true));
            Assert.StartsWith("ERR|DELAY_NOT_FOUND", DelayQueue.SetLoop("catB", cycId, true));
            Assert.StartsWith("ok", DelayQueue.SetLoop("catA", cycId, true));
            Assert.True(DelayQueue.List("catA")[1].Loop);
            Assert.StartsWith("ok", DelayQueue.SetLoop("catA", cycId, false));
            Assert.False(DelayQueue.List("catA")[1].Loop);
        }

        /// <summary>
        /// 落盘往返——loop / intervalMs / fired 随条目保留（跨宿主重启）。
        /// </summary>
        [Fact]
        public void Persist_RoundTripsLoopFields()
        {
            string path = Path.Combine(_dir, "delays-loop.json");
            DelayQueue.Configure(path);
            DelayQueue.Add("catA", "cyc", "timer", _now + 1000, 7000, true);
            _now = _now + 1000;
            DelayQueue.Pump(delegate (string cat, string content, string source) { return true; });
            DelayQueue.ResetForTest();
            DelayQueue.NowProvider = FakeNow;
            DelayQueue.Configure(path);
            DelayQueue.Load();
            DelayEntry[] loaded = DelayQueue.List("catA");
            Assert.Single(loaded);
            Assert.True(loaded[0].Loop);
            Assert.Equal(7000, loaded[0].IntervalMs);
            Assert.Equal(1, loaded[0].Fired);
        }

        /// <summary>
        /// at 解析——HH:mm 落在未来：时刻精确匹配，且触发点在一天之内。
        /// </summary>
        [Fact]
        public void ResolveAtTime_HHmmResolvesWithinOneDay()
        {
            long now = _now;
            DateTime localNow = DateTimeOffset.FromUnixTimeMilliseconds(now).ToLocalTime().DateTime;
            DateTime want = localNow.AddHours(1);
            string hm = want.ToString("HH:mm");
            long due;
            string err = CH4.ChatSession.ResolveAtTime(hm, now, out due);
            Assert.Equal("", err);
            Assert.True(due > now);
            Assert.True(due <= now + CH4.ChatSession.DayIntervalMs);
            DateTime dueLocal = DateTimeOffset.FromUnixTimeMilliseconds(due).ToLocalTime().DateTime;
            Assert.Equal(want.Hour, dueLocal.Hour);
            Assert.Equal(want.Minute, dueLocal.Minute);
        }

        /// <summary>
        /// at 解析——HH:mm 今日已过：顺延到明日同一时刻（闹钟直觉）。
        /// </summary>
        [Fact]
        public void ResolveAtTime_HHmmPastRollsToNextDay()
        {
            long now = _now;
            DateTime localNow = DateTimeOffset.FromUnixTimeMilliseconds(now).ToLocalTime().DateTime;
            DateTime want = localNow.AddHours(-2);
            string hm = want.ToString("HH:mm");
            long due;
            string err = CH4.ChatSession.ResolveAtTime(hm, now, out due);
            Assert.Equal("", err);
            Assert.True(due > now);
            Assert.True(due <= now + CH4.ChatSession.DayIntervalMs);
            DateTime dueLocal = DateTimeOffset.FromUnixTimeMilliseconds(due).ToLocalTime().DateTime;
            Assert.Equal(want.Hour, dueLocal.Hour);
            Assert.Equal(want.Minute, dueLocal.Minute);
        }

        /// <summary>
        /// at 解析——带日期形态必须是未来时刻，过去直接拒绝。
        /// </summary>
        [Fact]
        public void ResolveAtTime_DateFormRejectsPast()
        {
            long now = _now;
            DateTime localNow = DateTimeOffset.FromUnixTimeMilliseconds(now).ToLocalTime().DateTime;
            string past = localNow.AddDays(-1).ToString("yyyy-MM-dd HH:mm");
            long due;
            string err = CH4.ChatSession.ResolveAtTime(past, now, out due);
            Assert.StartsWith("ERR|BAD_ARGS", err);
            Assert.Equal(0, due);
        }

        /// <summary>
        /// at 解析——格式非法即拒绝（不做模糊猜测）。
        /// </summary>
        [Fact]
        public void ResolveAtTime_RejectsBadFormat()
        {
            long due;
            Assert.StartsWith("ERR|BAD_ARGS", CH4.ChatSession.ResolveAtTime("明天早上", _now, out due));
            Assert.StartsWith("ERR|BAD_ARGS", CH4.ChatSession.ResolveAtTime("25:99", _now, out due));
        }

        /// <summary>
        /// timer 参数面互斥——at 与相对时长 / loop 互斥，repeat 只随 at 且值域受限。
        /// </summary>
        [Fact]
        public void ValidateTimerArgs_RejectsConflictingPaths()
        {
            Assert.StartsWith("ERR|BAD_ARGS", CH4.ChatSession.ValidateTimerArgs("09:00", true, false, ""));
            Assert.StartsWith("ERR|BAD_ARGS", CH4.ChatSession.ValidateTimerArgs("09:00", false, true, ""));
            Assert.StartsWith("ERR|BAD_ARGS", CH4.ChatSession.ValidateTimerArgs("", false, false, "daily"));
            Assert.StartsWith("ERR|BAD_ARGS", CH4.ChatSession.ValidateTimerArgs("09:00", false, false, "weekly"));
            Assert.Equal("", CH4.ChatSession.ValidateTimerArgs("09:00", false, false, "daily"));
            Assert.Equal("", CH4.ChatSession.ValidateTimerArgs("", true, true, ""));
        }

        /// <summary>
        /// daily 周期相位——隔三天再泵：错过的两天跳过不补，下一次仍落在原相位点。
        /// </summary>
        [Fact]
        public void Pump_DailyLoopSkipsMissedDays()
        {
            long baseAt = _now + 1000;
            DelayQueue.Add("catA", "日报", "timer", baseAt, CH4.ChatSession.DayIntervalMs, true);
            _now = baseAt + CH4.ChatSession.DayIntervalMs * 3;
            int count = DelayQueue.Pump(delegate (string cat, string content, string source) { return true; });
            Assert.Equal(1, count);
            Assert.Equal(baseAt + CH4.ChatSession.DayIntervalMs * 4, DelayQueue.List("catA")[0].DueAt);
        }
    }
}
