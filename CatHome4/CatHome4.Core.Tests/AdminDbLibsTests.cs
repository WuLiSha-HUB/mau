using System;
using System.Collections.Generic;
using System.IO;
using CatHome4.Admin;
using Mau.Runtime;
using Xunit;

namespace CatHome4.Core.Tests
{
    /// <summary>
    /// 猫配置面库清单测试（A275）——库池可取的两种状态必须可区分：服务未绑定（不可取）与注册表为空（可取但无库）。
    /// </summary>
    public class AdminDbLibsTests
    {
        /// <summary>
        /// 服务已绑定——清单可取，且与注册表登记的库数一致。
        /// </summary>
        [Fact]
        public void BuildAllDbLibs_ServiceBound_ListsRegisteredLibs()
        {
            string dbRoot = NewTempRoot();
            try
            {
                DbService service = new DbService(dbRoot);
                Assert.True(service.Create("smoke-lib", "").Ok);
                DataBox.Bind<DbService>(service);
                bool available;
                List<object> libs = AdminService.BuildAllDbLibs(out available);
                Assert.True(available);
                Assert.Single(libs);
            }
            finally
            {
                DataBox.Unbind<DbService>();
                SafeDelete(dbRoot);
            }
        }

        /// <summary>
        /// 服务在、注册表为空——可取但无库（与「不可取」区分，前端据此显示「尚无库」而非降级告警）。
        /// </summary>
        [Fact]
        public void BuildAllDbLibs_ServiceBoundNoLib_AvailableButEmpty()
        {
            string dbRoot = NewTempRoot();
            try
            {
                DbService service = new DbService(dbRoot);
                DataBox.Bind<DbService>(service);
                bool available;
                List<object> libs = AdminService.BuildAllDbLibs(out available);
                Assert.True(available);
                Assert.Empty(libs);
            }
            finally
            {
                DataBox.Unbind<DbService>();
                SafeDelete(dbRoot);
            }
        }

        /// <summary>
        /// 服务未绑定——可用性为假且列表为空（调用方须出声，不得当作「无库」静默）。
        /// </summary>
        [Fact]
        public void BuildAllDbLibs_ServiceMissing_FlagsUnavailable()
        {
            DataBox.Unbind<DbService>();
            bool available = true;
            List<object> libs = AdminService.BuildAllDbLibs(out available);
            Assert.False(available);
            Assert.Empty(libs);
        }

        /// <summary>
        /// 建临时目录——库根（每例独立，跑完清理）。
        /// </summary>
        /// <returns>绝对路径</returns>
        private static string NewTempRoot()
        {
            string path = Path.Combine(Path.GetTempPath(), "ch4-dblibs-" + Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(path);
            return path;
        }

        /// <summary>
        /// 清理临时目录——失败不抛（测试清理不掩盖断言）。
        /// </summary>
        /// <param name="path">目录路径</param>
        private static void SafeDelete(string path)
        {
            try
            {
                if (Directory.Exists(path))
                {
                    Directory.Delete(path, true);
                }
            }
            catch (Exception ex)
            {
                // 清理失败不构成测试失败（临时目录残留可接受）——异常仅记录到调试输出
                System.Diagnostics.Debug.WriteLine(ex.Message);
            }
        }
    }
}
