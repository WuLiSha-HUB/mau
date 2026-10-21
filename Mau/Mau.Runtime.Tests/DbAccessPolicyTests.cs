using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using Mau.Runtime;
using Xunit;

namespace Mau.Runtime.Tests
{
    /// <summary>
    /// 库访问策略测试（A277）——fail-closed 语义、白名单判定、默认猫全放、服务层拦截与清单过滤。
    /// </summary>
    public sealed class DbAccessPolicyTests
    {
        /// <summary>
        /// 空 catId——无猫上下文（管理端点 / 单测 / CLI）放行。
        /// </summary>
        [Fact]
        public void EmptyCatId_Allowed()
        {
            DbAccessPolicy.Clear();
            try
            {
                Assert.True(DbAccessPolicy.IsAllowed("", "any-lib"));
                Assert.Null(DbAccessPolicy.DenyReason(null, "any-lib"));
            }
            finally
            {
                DbAccessPolicy.Clear();
            }
        }

        /// <summary>
        /// 默认猫全放——登记 allLibs 后任意库放行（库表变动不需重建策略）。
        /// </summary>
        [Fact]
        public void DefaultCat_AllLibs()
        {
            DbAccessPolicy.Clear();
            try
            {
                DbAccessPolicy.Register("majordomo", true, new string[0]);
                Assert.True(DbAccessPolicy.IsAllowed("majordomo", "whatever"));
                Assert.True(DbAccessPolicy.IsAllowed("majordomo", "another-lib"));
            }
            finally
            {
                DbAccessPolicy.Clear();
            }
        }

        /// <summary>
        /// 登记空集合——无库权限（fail-closed），文案点明 dbLibs 为空。
        /// </summary>
        [Fact]
        public void RegisteredEmpty_AllDenied()
        {
            DbAccessPolicy.Clear();
            try
            {
                DbAccessPolicy.Register("cat-a", false, new string[0]);
                string? reason = DbAccessPolicy.DenyReason("cat-a", "any-lib");
                Assert.NotNull(reason);
                Assert.Contains("dbLibs 为空", reason);
            }
            finally
            {
                DbAccessPolicy.Clear();
            }
        }

        /// <summary>
        /// 白名单子集——只放登记库，其余拒；库名大小写不敏感。
        /// </summary>
        [Fact]
        public void RegisteredSubset_OnlyListed()
        {
            DbAccessPolicy.Clear();
            try
            {
                DbAccessPolicy.Register("cat-b", false, new string[] { "lib-a" });
                Assert.True(DbAccessPolicy.IsAllowed("cat-b", "lib-a"));
                Assert.True(DbAccessPolicy.IsAllowed("cat-b", "LIB-A"));
                string? reason = DbAccessPolicy.DenyReason("cat-b", "lib-b");
                Assert.NotNull(reason);
                Assert.Contains("未授权", reason);
            }
            finally
            {
                DbAccessPolicy.Clear();
            }
        }

        /// <summary>
        /// 未登记猫——拒（fail-closed），文案点明未登记。
        /// </summary>
        [Fact]
        public void Unregistered_Denied()
        {
            DbAccessPolicy.Clear();
            try
            {
                string? reason = DbAccessPolicy.DenyReason("cat-x", "lib-a");
                Assert.NotNull(reason);
                Assert.Contains("未登记", reason);
            }
            finally
            {
                DbAccessPolicy.Clear();
            }
        }

        /// <summary>
        /// 注销后回到未登记态——拒。
        /// </summary>
        [Fact]
        public void Unregister_ThenDenied()
        {
            DbAccessPolicy.Clear();
            try
            {
                DbAccessPolicy.Register("cat-c", true, new string[0]);
                Assert.True(DbAccessPolicy.IsAllowed("cat-c", "lib-a"));
                DbAccessPolicy.Unregister("cat-c");
                Assert.False(DbAccessPolicy.IsAllowed("cat-c", "lib-a"));
            }
            finally
            {
                DbAccessPolicy.Clear();
            }
        }

        /// <summary>
        /// 服务层拦截——未授权库写入返回 DB_LIB_DENIED（唯一判定点 = OpenLib）。
        /// </summary>
        [Fact]
        public void DbService_DeniedLib_ReturnsCode()
        {
            DbAccessPolicy.Clear();
            string root = NewRoot();
            try
            {
                DbService db = new DbService(root);
                Assert.True(db.Create("liba", "").Ok);
                Assert.True(db.Create("libb", "").Ok);
                DbAccessPolicy.Register("cat-d", false, new string[] { "libb" });
                DbResult put = db.Put("liba", "标题", "正文", "cat-d");
                Assert.False(put.Ok);
                Assert.Equal("DB_LIB_DENIED", put.Code);
                DbResult query = db.Query("liba", "", 10, "cat-d");
                Assert.Equal("DB_LIB_DENIED", query.Code);
                DbResult get = db.Get("liba", "id", "1", "cat-d");
                Assert.Equal("DB_LIB_DENIED", get.Code);
                DbResult update = db.Update("liba", "标题", "正文", "cat-d");
                Assert.Equal("DB_LIB_DENIED", update.Code);
                DbResult delete = db.Delete("liba", "标题", "cat-d");
                Assert.Equal("DB_LIB_DENIED", delete.Code);
                Assert.True(db.Put("libb", "标题", "正文", "cat-d").Ok);
            }
            finally
            {
                DbAccessPolicy.Clear();
                SafeDelete(root);
            }
        }

        /// <summary>
        /// 清单过滤——ListLibs 只返回授权库（未授权库不出现在清单）。
        /// </summary>
        [Fact]
        public void DbService_ListLibs_Filtered()
        {
            DbAccessPolicy.Clear();
            string root = NewRoot();
            try
            {
                DbService db = new DbService(root);
                Assert.True(db.Create("liba", "").Ok);
                Assert.True(db.Create("libb", "").Ok);
                DbAccessPolicy.Register("cat-e", false, new string[] { "libb" });
                DbResult filtered = db.ListLibs("cat-e");
                Assert.True(filtered.Ok);
                DbResult all = db.ListLibs("");
                Assert.True(all.Ok);
                Assert.Equal(2, CountLibs(all.Data));
                Assert.Equal(1, CountLibs(filtered.Data));
            }
            finally
            {
                DbAccessPolicy.Clear();
                SafeDelete(root);
            }
        }

        /// <summary>
        /// 临时库根——每次测试独立目录。
        /// </summary>
        /// <returns>临时目录绝对路径</returns>
        private static string NewRoot()
        {
            string root = Path.Combine(Path.GetTempPath(), "mau_db_acl_" + Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(root);
            return root;
        }

        /// <summary>
        /// 库清单条数——解析载荷 libs 数组长度。
        /// </summary>
        /// <param name="payload">载荷 JSON</param>
        /// <returns>库条数</returns>
        private static int CountLibs(string payload)
        {
            using (JsonDocument doc = JsonUtil.ParseStrict(payload, "test.db.acl"))
            {
                JsonElement libs;
                if (!doc.RootElement.TryGetProperty("libs", out libs))
                {
                    return 0;
                }
                return libs.GetArrayLength();
            }
        }

        /// <summary>
        /// 清理临时目录——失败不阻断（临时目录残留可接受）。
        /// </summary>
        /// <param name="dir">目录路径</param>
        private static void SafeDelete(string dir)
        {
            try
            {
                if (Directory.Exists(dir))
                {
                    Directory.Delete(dir, true);
                }
            }
            catch (Exception ex)
            {
                System.Diagnostics.Debug.WriteLine(ex.Message);
            }
        }
    }
}
