using CatHome4.Contracts;
using Xunit;

namespace CatHome4.Core.Tests
{
    /// <summary>
    /// 启动降级登记表测试（A260）——登记 / 去重 / 快照隔离 / 摘要口径。
    /// 全局静态表：每个用例前后清空（单集合内不走并行——见 Collection 声明）。
    /// </summary>
    [Collection("GlobalToolState")]
    public class StartupDegradationTests
    {
        /// <summary>
        /// 登记一条降级——字段原样落表，HasAny / Count 同步。
        /// </summary>
        [Fact]
        public void Report_SingleEntry_FillsAllFields()
        {
            StartupDegradation.Clear();
            StartupDegradation.Report("ROOT_UNREACHABLE", StartupDegradation.KindEnv, "文件工具",
                StartupDegradation.StateFallback, "workspace 根不可达：D:/old", "面板改根", "S2");
            Assert.True(StartupDegradation.HasAny);
            Assert.Equal(1, StartupDegradation.Count);
            StartupDegradeEntry[] entries = StartupDegradation.Snapshot();
            Assert.Equal(1, entries.Length);
            Assert.Equal("ROOT_UNREACHABLE", entries[0].Code);
            Assert.Equal("env", entries[0].Kind);
            Assert.Equal("文件工具", entries[0].Ability);
            Assert.Equal("fallback", entries[0].State);
            Assert.Equal("S2", entries[0].Stage);
            Assert.Equal(1, entries[0].Count);
            StartupDegradation.Clear();
        }

        /// <summary>
        /// 去重口径——同 Code + Ability 累加次数，不新增条目；详情保留首条。
        /// </summary>
        [Fact]
        public void Report_SameCodeAndAbility_AccumulatesCount()
        {
            StartupDegradation.Clear();
            StartupDegradation.Report("CFG_CORRUPT", StartupDegradation.KindData, "外观配置池",
                StartupDegradation.StateEmpty, "第一次", "处理 A", "S7");
            StartupDegradation.Report("CFG_CORRUPT", StartupDegradation.KindData, "外观配置池",
                StartupDegradation.StateEmpty, "第二次", "处理 B", "S7");
            Assert.Equal(1, StartupDegradation.Count);
            StartupDegradeEntry[] entries = StartupDegradation.Snapshot();
            Assert.Equal(2, entries[0].Count);
            Assert.Equal("第一次", entries[0].Detail);
            StartupDegradation.Clear();
        }

        /// <summary>
        /// 同 Code 不同 Ability——按两条登记（能力面不同即不同缺陷）。
        /// </summary>
        [Fact]
        public void Report_SameCodeDifferentAbility_SeparateEntries()
        {
            StartupDegradation.Clear();
            StartupDegradation.Report("CFG_CORRUPT", StartupDegradation.KindData, "LLM 端点池",
                StartupDegradation.StateEmpty, "x", "y", "S5");
            StartupDegradation.Report("CFG_CORRUPT", StartupDegradation.KindData, "QQ 通道（Bot 配置）",
                StartupDegradation.StateEmpty, "x", "y", "S7");
            Assert.Equal(2, StartupDegradation.Count);
            StartupDegradation.Clear();
        }

        /// <summary>
        /// 快照隔离——改动快照对象不影响登记表（深拷贝）。
        /// </summary>
        [Fact]
        public void Snapshot_IsDeepCopy()
        {
            StartupDegradation.Clear();
            StartupDegradation.Report("PORT_FALLBACK", StartupDegradation.KindEnv, "外观层（主端口）",
                StartupDegradation.StateFallback, "端口 8079 不可用", "关掉占用进程", "S6");
            StartupDegradeEntry[] first = StartupDegradation.Snapshot();
            first[0].Code = "TAMPERED";
            StartupDegradeEntry[] second = StartupDegradation.Snapshot();
            Assert.Equal("PORT_FALLBACK", second[0].Code);
            StartupDegradation.Clear();
        }

        /// <summary>
        /// 摘要行——无降级为空串；有降级含条数与码（多次登记标 ×N）。
        /// </summary>
        [Fact]
        public void BuildSummaryLine_FormatsCodesAndCounts()
        {
            StartupDegradation.Clear();
            Assert.Equal("", StartupDegradation.BuildSummaryLine());
            StartupDegradation.Report("FLOWS_EMPTY", StartupDegradation.KindEnv, "工具面（全部 Flow）",
                StartupDegradation.StateEmpty, "d", "f", "S4");
            StartupDegradation.Report("FLOWS_EMPTY", StartupDegradation.KindEnv, "工具面（全部 Flow）",
                StartupDegradation.StateEmpty, "d", "f", "S4");
            string line = StartupDegradation.BuildSummaryLine();
            Assert.Contains("1 项", line);
            Assert.Contains("FLOWS_EMPTY×2", line);
            StartupDegradation.Clear();
        }

        /// <summary>
        /// 内部缺陷标记——internal 类可单独检出（进「需上报」栏，不当作环境问题忽略）。
        /// </summary>
        [Fact]
        public void HasInternal_DetectsInternalKind()
        {
            StartupDegradation.Clear();
            StartupDegradation.Report("BOOT_STEP_FAILED", StartupDegradation.KindEnv, "观测日志",
                StartupDegradation.StateEmpty, "d", "f", "S3");
            Assert.False(StartupDegradation.HasInternal);
            StartupDegradation.Report("BOOT_LOGIC_FAULT", StartupDegradation.KindInternal, "会话",
                StartupDegradation.StateUnavailable, "d", "f", "S5");
            Assert.True(StartupDegradation.HasInternal);
            StartupDegradation.Clear();
        }
    }
}
