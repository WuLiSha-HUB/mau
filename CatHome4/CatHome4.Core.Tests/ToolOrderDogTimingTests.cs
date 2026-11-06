using System;
using Xunit;

namespace CatHome4.Core.Tests
{
    /// <summary>
    /// 工具单计时（单工具口径）测试——运行时长按各自闭合时刻计，不再取批统一采样时刻。
    /// </summary>
    public class ToolOrderDogTimingTests
    {
        /// <summary>
        /// 同派发、异闭合的两工具——各自时长不同（单工具口径：批统一恒等的反例）。
        /// </summary>
        [Fact]
        public void ElapsedMs_PerDogClosedAt_DiffersByCompletion()
        {
            CH4.ToolOrderDog fast = new CH4.ToolOrderDog("1", "tool.fast", "{}");
            fast.StartedAtMs = 1000;
            fast.ClosedAtMs = 1200;
            CH4.ToolOrderDog slow = new CH4.ToolOrderDog("2", "tool.slow", "{}");
            slow.StartedAtMs = 1000;
            slow.ClosedAtMs = 5000;
            Assert.Equal(200, fast.ElapsedMs());
            Assert.Equal(4000, slow.ElapsedMs());
        }

        /// <summary>
        /// 未闭合——回落采样时刻（仍得正时长，不返回 -1）。
        /// </summary>
        [Fact]
        public void ElapsedMs_Unclosed_FallsBackToNow()
        {
            CH4.ToolOrderDog dog = new CH4.ToolOrderDog("1", "tool.a", "{}");
            dog.StartedAtMs = 1000;
            long ms = dog.ElapsedMs();
            Assert.True(ms > 0);
        }

        /// <summary>
        /// 未派发——返回 -1（未记录，不造值）。
        /// </summary>
        [Fact]
        public void ElapsedMs_NotDispatched_ReturnsMinusOne()
        {
            CH4.ToolOrderDog dog = new CH4.ToolOrderDog("1", "tool.a", "{}");
            Assert.Equal(-1, dog.ElapsedMs());
        }
        /// <summary>
        /// 两段计时——排队（派发→认领）+ 执行（认领→闭合），两者之和等于总时长。
        /// </summary>
        [Fact]
        public void QueueMs_RunMs_SplitTotal()
        {
            CH4.ToolOrderDog dog = new CH4.ToolOrderDog("1", "tool.a", "{}");
            dog.StartedAtMs = 1000;
            dog.ClaimedAtMs = 9000;
            dog.ClosedAtMs = 9100;
            Assert.Equal(8000, dog.QueueMs());
            Assert.Equal(100, dog.RunMs());
            Assert.Equal(8100, dog.ElapsedMs());
        }
        /// <summary>
        /// 认领未观测（快单在两轮轮询之间直接闭合）——两段均返回 -1，不造值。
        /// </summary>
        [Fact]
        public void QueueMs_RunMs_Unclaimed_ReturnsMinusOne()
        {
            CH4.ToolOrderDog dog = new CH4.ToolOrderDog("1", "tool.a", "{}");
            dog.StartedAtMs = 1000;
            dog.ClosedAtMs = 1200;
            Assert.Equal(-1, dog.QueueMs());
            Assert.Equal(-1, dog.RunMs());
        }
    }
}
