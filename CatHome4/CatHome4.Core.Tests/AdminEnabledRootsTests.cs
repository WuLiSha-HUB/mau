using CatHome4.Admin;
using Mau.Runtime;
using Xunit;

namespace CatHome4.Core.Tests
{
    /// <summary>
    /// 启用根白名单校验测试（A261）——池外 id 剔除出声 + 根池不可用降级标记。
    /// </summary>
    public class AdminEnabledRootsTests
    {
        /// <summary>
        /// 池外 id——剔除并随 removed 带回；结果保序（workspace 强制在首）。
        /// </summary>
        [Fact]
        public void ValidateEnabledRoots_OutOfPool_RemovedReported()
        {
            try
            {
                BindPool(new string[] { "workspace", "ccbp", "mau" });
                string[] removed;
                bool poolUnavailable;
                string[] result = AdminService.ValidateEnabledRoots(new string[] { "ccbp", "gone", "ccbp" }, out removed, out poolUnavailable);
                Assert.False(poolUnavailable);
                Assert.Equal(new string[] { "workspace", "ccbp" }, result);
                Assert.Equal(new string[] { "gone" }, removed);
            }
            finally
            {
                DataBox.Unbind<WorkspaceConfig>();
            }
        }

        /// <summary>
        /// 池外 id 全部剔除——removed 收录全部被剔项（不静默丢失）。
        /// </summary>
        [Fact]
        public void ValidateEnabledRoots_AllOutOfPool_AllReported()
        {
            try
            {
                BindPool(new string[] { "workspace" });
                string[] removed;
                bool poolUnavailable;
                string[] result = AdminService.ValidateEnabledRoots(new string[] { "gone1", "gone2" }, out removed, out poolUnavailable);
                Assert.False(poolUnavailable);
                Assert.Equal(new string[] { "workspace" }, result);
                Assert.Equal(new string[] { "gone1", "gone2" }, removed);
            }
            finally
            {
                DataBox.Unbind<WorkspaceConfig>();
            }
        }

        /// <summary>
        /// 根池不可用——降级标记为真（白名单按空处理，调用方须出声）。
        /// </summary>
        [Fact]
        public void ValidateEnabledRoots_PoolUnavailable_DegradeFlagged()
        {
            DataBox.Unbind<WorkspaceConfig>();
            string[] removed;
            bool poolUnavailable;
            string[] result = AdminService.ValidateEnabledRoots(new string[] { "ccbp" }, out removed, out poolUnavailable);
            Assert.True(poolUnavailable);
            Assert.Empty(result);
            Assert.Empty(removed);
        }

        /// <summary>
        /// 空白名单——仅常驻根（与未配置语义一致，不记剔除）。
        /// </summary>
        [Fact]
        public void ValidateEnabledRoots_EmptyInput_ResidentOnly()
        {
            try
            {
                BindPool(new string[] { "workspace", "ccbp" });
                string[] removed;
                bool poolUnavailable;
                string[] result = AdminService.ValidateEnabledRoots(new string[0], out removed, out poolUnavailable);
                Assert.False(poolUnavailable);
                Assert.Equal(new string[] { "workspace" }, result);
                Assert.Empty(removed);
            }
            finally
            {
                DataBox.Unbind<WorkspaceConfig>();
            }
        }

        /// <summary>
        /// 绑定全局根池——测试专用（DataBox 服务表喂表；调用方负责 Unbind）。
        /// </summary>
        /// <param name="ids">根 id 清单（首项即常驻基准）</param>
        private static void BindPool(string[] ids)
        {
            WorkspaceConfig ws = new WorkspaceConfig();
            WorkspaceConfig.RootEntry[] roots = new WorkspaceConfig.RootEntry[ids.Length];
            for (int i = 0; i < ids.Length; i = i + 1)
            {
                WorkspaceConfig.RootEntry entry = new WorkspaceConfig.RootEntry();
                entry.Id = ids[i];
                entry.Path = "C:/test/" + ids[i];
                entry.Writable = true;
                roots[i] = entry;
            }
            ws.Roots = roots;
            DataBox.Bind<WorkspaceConfig>(ws);
        }
    }
}
