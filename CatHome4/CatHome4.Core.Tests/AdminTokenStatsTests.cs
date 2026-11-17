using System;
using System.IO;
using System.Text;
using System.Text.Json;
using Xunit;

namespace CatHome4.Core.Tests
{
    /// <summary>
    /// Token 用量统计面测试（A389）——按猫按日聚合、坏行计数、空数据根、显示名解析。
    /// </summary>
    public class AdminTokenStatsTests
    {
        /// <summary>
        /// 建临时数据根——返回绝对路径（调用方负责删除）；含 Data/tokennumber 树。
        /// </summary>
        /// <returns>数据根</returns>
        private static string NewRoot()
        {
            string root = Path.Combine(Path.GetTempPath(), "cat4stats_" + Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(Path.Combine(root, "Data", "tokennumber"));
            return root;
        }

        /// <summary>
        /// 写一份用量日文件——父目录自动创建。
        /// </summary>
        /// <param name="root">数据根</param>
        /// <param name="catKey">猫 key</param>
        /// <param name="day">日期（yyyyMMdd）</param>
        /// <param name="lines">行文本</param>
        private static void WriteUsage(string root, string catKey, string day, string[] lines)
        {
            string dir = Path.Combine(root, "Data", "tokennumber", catKey);
            Directory.CreateDirectory(dir);
            StringBuilder sb = new StringBuilder();
            for (int i = 0; i < lines.Length; i = i + 1)
            {
                sb.Append(lines[i]);
                sb.Append("\n");
            }
            File.WriteAllText(Path.Combine(dir, day + ".jsonl"), sb.ToString(), new UTF8Encoding(false));
        }

        /// <summary>
        /// 两猫多行 + 一行坏行——按猫按日聚合正确，坏行计入 badLines 不静默。
        /// </summary>
        [Fact]
        public void Build_AggregatesByCatAndDay_CountsBadLines()
        {
            string root = NewRoot();
            try
            {
                WriteUsage(root, "catA", "20261117", new string[]
                {
                    "{\"ts\":1000,\"round\":0,\"req\":1,\"prompt\":100,\"completion\":10,\"cacheHit\":40,\"miss\":60}",
                    "{\"ts\":2000,\"round\":1,\"req\":1,\"prompt\":200,\"completion\":20,\"cacheHit\":150,\"miss\":50}",
                    "这不是 JSON"
                });
                WriteUsage(root, "catB", "20261117", new string[]
                {
                    "{\"ts\":3000,\"round\":0,\"req\":1,\"prompt\":50,\"completion\":5,\"cacheHit\":0,\"miss\":50}"
                });
                string json = CatHome4.Admin.AdminService.BuildTokenUsageStatsJson(root);
                using (JsonDocument doc = JsonDocument.Parse(json))
                {
                    JsonElement docRoot = doc.RootElement;
                    Assert.True(docRoot.GetProperty("ok").GetBoolean());
                    Assert.Equal(1, docRoot.GetProperty("badLines").GetInt64());
                    JsonElement totals = docRoot.GetProperty("totals");
                    Assert.Equal(3, totals.GetProperty("requests").GetInt64());
                    Assert.Equal(350, totals.GetProperty("prompt").GetInt64());
                    Assert.Equal(35, totals.GetProperty("completion").GetInt64());
                    Assert.Equal(190, totals.GetProperty("cacheHit").GetInt64());
                    Assert.Equal(160, totals.GetProperty("miss").GetInt64());
                    JsonElement cats = docRoot.GetProperty("cats");
                    Assert.Equal(2, cats.GetArrayLength());
                    JsonElement catA = cats[0];
                    Assert.Equal("catA", catA.GetProperty("catKey").GetString());
                    Assert.Equal(2, catA.GetProperty("requests").GetInt64());
                    Assert.Equal(300, catA.GetProperty("prompt").GetInt64());
                    JsonElement days = catA.GetProperty("days");
                    Assert.Equal(1, days.GetArrayLength());
                    Assert.Equal("20261117", days[0].GetProperty("day").GetString());
                    Assert.Equal(2000, days[0].GetProperty("lastTs").GetInt64());
                    Assert.Equal("catB", cats[1].GetProperty("catKey").GetString());
                    Assert.Equal(1, cats[1].GetProperty("requests").GetInt64());
                }
            }
            finally
            {
                if (Directory.Exists(root))
                {
                    Directory.Delete(root, true);
                }
            }
        }

        /// <summary>
        /// 数据根不存在——返回 ok 快照、cats 空、合计全零（首启无用量不是错误）。
        /// </summary>
        [Fact]
        public void Build_NoDataRoot_ReturnsEmptySnapshot()
        {
            string missing = Path.Combine(Path.GetTempPath(), "cat4stats_missing_" + Guid.NewGuid().ToString("N"));
            string json = CatHome4.Admin.AdminService.BuildTokenUsageStatsJson(missing);
            using (JsonDocument doc = JsonDocument.Parse(json))
            {
                JsonElement docRoot = doc.RootElement;
                Assert.True(docRoot.GetProperty("ok").GetBoolean());
                Assert.Equal(0, docRoot.GetProperty("cats").GetArrayLength());
                Assert.Equal(0, docRoot.GetProperty("totals").GetProperty("requests").GetInt64());
            }
        }

        /// <summary>
        /// 猫显示名——按传入数据根读 cat.cfg（缺失回落空串，不抛）。
        /// </summary>
        [Fact]
        public void Build_ResolvesDisplayNameFromCatCfg()
        {
            string root = NewRoot();
            try
            {
                WriteUsage(root, "catA", "20261117", new string[]
                {
                    "{\"ts\":1000,\"round\":0,\"req\":1,\"prompt\":10,\"completion\":1,\"cacheHit\":0,\"miss\":10}"
                });
                string catDir = Path.Combine(root, "Data", "sessions", "catA");
                Directory.CreateDirectory(catDir);
                File.WriteAllText(Path.Combine(catDir, "cat.cfg"), "{\"id\":\"catA\",\"displayName\":\"测试猫\"}", new UTF8Encoding(false));
                WriteUsage(root, "catB", "20261117", new string[]
                {
                    "{\"ts\":1000,\"round\":0,\"req\":1,\"prompt\":10,\"completion\":1,\"cacheHit\":0,\"miss\":10}"
                });
                string json = CatHome4.Admin.AdminService.BuildTokenUsageStatsJson(root);
                using (JsonDocument doc = JsonDocument.Parse(json))
                {
                    JsonElement cats = doc.RootElement.GetProperty("cats");
                    Assert.Equal("测试猫", cats[0].GetProperty("displayName").GetString());
                    Assert.Equal("", cats[1].GetProperty("displayName").GetString());
                }
            }
            finally
            {
                if (Directory.Exists(root))
                {
                    Directory.Delete(root, true);
                }
            }
        }
    }
}
