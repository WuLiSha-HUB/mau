using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using Mau.Runtime;
using Xunit;

namespace Mau.Runtime.Tests
{
    /// <summary>
    /// 条目库服务测试（A267）——六用例主链 + 追加式落盘 + 聚合取 v 最大 + 坏行出声 + 硬限拒写。
    /// </summary>
    public sealed class DbServiceTests
    {
        /// <summary>
        /// 六用例主链——建库 / 增 / 查 / 读（标题与 ID 同源）/ 改（v+1）/ 删（墓碑保留、标题释放）。
        /// </summary>
        [Fact]
        public void SixUseCases_FullChain()
        {
            string root = NewRoot();
            try
            {
                DbService db = new DbService(root);
                Assert.True(db.Create("liba", "").Ok);
                DbResult put = db.Put("liba", "时间back 域类型门禁", "正文一", "");
                Assert.True(put.Ok);
                Assert.Equal(1, ReadInt(put.Data, "id"));
                DbResult query = db.Query("liba", "门禁", 10, "");
                Assert.True(query.Ok);
                Assert.Equal(1, ReadInt(query.Data, "total"));
                Assert.Equal(1, ReadInt(query.Data, "matched"));
                Assert.Equal(1, ReadInt(query.Data, "shown"));
                DbResult byTitle = db.Get("liba", "title", "时间back 域类型门禁", "");
                DbResult byId = db.Get("liba", "id", "1", "");
                Assert.True(byTitle.Ok);
                Assert.True(byId.Ok);
                Assert.Equal(ReadStr(byTitle.Data, "body"), ReadStr(byId.Data, "body"));
                DbResult update = db.Update("liba", "时间back 域类型门禁", "正文二", "");
                Assert.True(update.Ok);
                Assert.Equal(2, ReadInt(update.Data, "v"));
                Assert.Equal("正文二", ReadStr(db.Get("liba", "id", "1", "").Data, "body"));
                DbResult delete = db.Delete("liba", "时间back 域类型门禁", "");
                Assert.True(delete.Ok);
                Assert.Equal(0, ReadInt(db.Query("liba", "", 10, "").Data, "total"));
                DbResult gone = db.Get("liba", "id", "1", "");
                Assert.False(gone.Ok);
                Assert.Equal("DB_ENTRY_NOT_FOUND", gone.Code);
                Assert.Equal(4, File.ReadAllLines(EntriesPath(root, "liba")).Length);
            }
            finally
            {
                SafeDelete(root);
            }
        }

        /// <summary>
        /// 100 条写入——行数 = 101（含头行）；查询三数自证（total / matched / shown）。
        /// </summary>
        [Fact]
        public void HundredPuts_AppendOnlyAndCounts()
        {
            string root = NewRoot();
            try
            {
                DbService db = new DbService(root);
                Assert.True(db.Create("bulk", "").Ok);
                for (int i = 1; i <= 100; i = i + 1)
                {
                    DbResult put = db.Put("bulk", "条目" + i.ToString(), "正文" + i.ToString(), "");
                    Assert.True(put.Ok);
                }
                Assert.Equal(101, File.ReadAllLines(EntriesPath(root, "bulk")).Length);
                DbResult all = db.Query("bulk", "", 200, "");
                Assert.True(all.Ok);
                Assert.Equal(100, ReadInt(all.Data, "total"));
                Assert.Equal(100, ReadInt(all.Data, "matched"));
                Assert.Equal(100, ReadInt(all.Data, "shown"));
                DbResult page = db.Query("bulk", "", 10, "");
                Assert.Equal(10, ReadInt(page.Data, "shown"));
                Assert.Equal(100, ReadInt(page.Data, "matched"));
            }
            finally
            {
                SafeDelete(root);
            }
        }

        /// <summary>
        /// 唯一键与不存在——同标题重复增 → DB_TITLE_EXISTS；改 / 删不存在标题 → DB_TITLE_NOT_FOUND。
        /// </summary>
        [Fact]
        public void TitleKeyConflicts()
        {
            string root = NewRoot();
            try
            {
                DbService db = new DbService(root);
                Assert.True(db.Create("keys", "").Ok);
                Assert.True(db.Put("keys", "同名", "一", "").Ok);
                DbResult dup = db.Put("keys", "同名", "二", "");
                Assert.False(dup.Ok);
                Assert.Equal("DB_TITLE_EXISTS", dup.Code);
                DbResult upd = db.Update("keys", "没有这条", "正文", "");
                Assert.Equal("DB_TITLE_NOT_FOUND", upd.Code);
                DbResult del = db.Delete("keys", "没有这条", "");
                Assert.Equal("DB_TITLE_NOT_FOUND", del.Code);
            }
            finally
            {
                SafeDelete(root);
            }
        }

        /// <summary>
        /// 硬限拒写——标题 51 字符 / 正文 5001 字符具名出声；50 与 5000 边界放行。
        /// </summary>
        [Fact]
        public void TooLong_Rejected()
        {
            string root = NewRoot();
            try
            {
                DbService db = new DbService(root);
                Assert.True(db.Create("limits", "").Ok);
                DbResult longTitle = db.Put("limits", new string('标', 51), "正文", "");
                Assert.False(longTitle.Ok);
                Assert.Equal("DB_TITLE_TOO_LONG", longTitle.Code);
                DbResult longBody = db.Put("limits", "标题", new string('文', 5001), "");
                Assert.False(longBody.Ok);
                Assert.Equal("DB_BODY_TOO_LONG", longBody.Code);
                Assert.True(db.Put("limits", new string('标', 50), new string('文', 5000), "").Ok);
            }
            finally
            {
                SafeDelete(root);
            }
        }

        /// <summary>
        /// 坏行出声跳过——手工插入非 JSON 行后查询仍可用，坏行计数进结果（不静默丢弃、不中断全库）。
        /// </summary>
        [Fact]
        public void BadLine_SkippedAndCounted()
        {
            string root = NewRoot();
            try
            {
                DbService db = new DbService(root);
                Assert.True(db.Create("bad", "").Ok);
                Assert.True(db.Put("bad", "好条目", "正文", "").Ok);
                File.AppendAllText(EntriesPath(root, "bad"), "这不是 JSON\n");
                DbResult query = db.Query("bad", "", 10, "");
                Assert.True(query.Ok);
                Assert.Equal(1, query.BadLines);
                Assert.Equal(1, ReadInt(query.Data, "total"));
            }
            finally
            {
                SafeDelete(root);
            }
        }

        /// <summary>
        /// 格式不符整库拒绝——头行 fmt 非实现版本 → DB_FORMAT_UNSUPPORTED（不回落空库）。
        /// </summary>
        [Fact]
        public void FormatUnsupported_Rejected()
        {
            string root = NewRoot();
            try
            {
                DbService db = new DbService(root);
                Assert.True(db.Create("fmtlib", "").Ok);
                string path = EntriesPath(root, "fmtlib");
                string[] lines = File.ReadAllLines(path);
                lines[0] = "{\"_\":\"db.entries\",\"fmt\":9,\"lib\":\"fmtlib\"}";
                File.WriteAllLines(path, lines);
                DbResult query = db.Query("fmtlib", "", 10, "");
                Assert.False(query.Ok);
                Assert.Equal("DB_FORMAT_UNSUPPORTED", query.Code);
            }
            finally
            {
                SafeDelete(root);
            }
        }

        /// <summary>
        /// 库存在性——未登记库拒绝（不因目录存在而可用）；库名非法拒绝；重复建库出声。
        /// </summary>
        [Fact]
        public void LibGuards()
        {
            string root = NewRoot();
            try
            {
                DbService db = new DbService(root);
                Assert.Equal("DB_LIB_NOT_FOUND", db.Query("nope", "", 10, "").Code);
                Assert.Equal("BAD_ARGS", db.Create("A", "").Code);
                Assert.True(db.Create("ok-lib", "").Ok);
                Assert.Equal("DB_LIB_EXISTS", db.Create("ok-lib", "").Code);
                Directory.CreateDirectory(Path.Combine(root, "ghost"));
                Assert.Equal("DB_LIB_NOT_FOUND", db.Query("ghost", "", 10, "").Code);
            }
            finally
            {
                SafeDelete(root);
            }
        }

        /// <summary>
        /// 标题释放——删除后同标题可再建，得到新 ID（墓碑占位不复用）。
        /// </summary>
        [Fact]
        public void TitleReleasedAfterDelete_NewId()
        {
            string root = NewRoot();
            try
            {
                DbService db = new DbService(root);
                Assert.True(db.Create("release", "").Ok);
                DbResult first = db.Put("release", "可复用标题", "一", "");
                Assert.True(first.Ok);
                Assert.True(db.Delete("release", "可复用标题", "").Ok);
                DbResult second = db.Put("release", "可复用标题", "二", "");
                Assert.True(second.Ok);
                Assert.Equal(2, ReadInt(second.Data, "id"));
                Assert.Equal(1, ReadInt(db.Query("release", "", 10, "").Data, "total"));
            }
            finally
            {
                SafeDelete(root);
            }
        }

        /// <summary>
        /// 临时库根——每次测试独立目录。
        /// </summary>
        /// <returns>临时目录绝对路径</returns>
        private static string NewRoot()
        {
            string root = Path.Combine(Path.GetTempPath(), "mau_db_test_" + Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(root);
            return root;
        }

        /// <summary>
        /// 条目文件路径——库根下 lib/entries.jsonl。
        /// </summary>
        /// <param name="root">库根</param>
        /// <param name="lib">库名</param>
        /// <returns>条目文件绝对路径</returns>
        private static string EntriesPath(string root, string lib)
        {
            return Path.Combine(Path.Combine(root, lib), "entries.jsonl");
        }

        /// <summary>
        /// 读载荷整型字段。
        /// </summary>
        /// <param name="payload">载荷 JSON</param>
        /// <param name="name">字段名</param>
        /// <returns>整型值（缺失 0）</returns>
        private static int ReadInt(string payload, string name)
        {
            using (JsonDocument doc = JsonUtil.ParseStrict(payload, "test.db"))
            {
                return JsonUtil.GetInt(doc.RootElement, name, 0);
            }
        }

        /// <summary>
        /// 读载荷字符串字段。
        /// </summary>
        /// <param name="payload">载荷 JSON</param>
        /// <param name="name">字段名</param>
        /// <returns>字符串值（缺失空串）</returns>
        private static string ReadStr(string payload, string name)
        {
            using (JsonDocument doc = JsonUtil.ParseStrict(payload, "test.db"))
            {
                return JsonUtil.GetStr(doc.RootElement, name, "");
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
        /// <summary>
        /// 口径边界——标题归一（Trim 后比较 + 忽略大小写）与标题控制字符拒绝。
        /// </summary>
        [Fact]
        public void Contract_TitleNormalization()
        {
            string root = NewRoot();
            try
            {
                DbService db = new DbService(root);
                Assert.True(db.Create("norm", "").Ok);
                Assert.True(db.Put("norm", "  标题甲  ", "正文一", "").Ok);
                DbResult padded = db.Put("norm", "标题甲", "正文二", "");
                Assert.False(padded.Ok);
                Assert.Equal("DB_TITLE_EXISTS", padded.Code);
                DbResult getPadded = db.Get("norm", "title", "  标题甲 ", "");
                Assert.True(getPadded.Ok);
                Assert.True(db.Put("norm", "Chapter", "正文三", "").Ok);
                DbResult caseDup = db.Put("norm", "chapter", "正文四", "");
                Assert.Equal("DB_TITLE_EXISTS", caseDup.Code);
                DbResult getCase = db.Get("norm", "title", "CHAPTER", "");
                Assert.True(getCase.Ok);
                DbResult ctrl = db.Put("norm", "标题\n乙", "正文五", "");
                Assert.False(ctrl.Ok);
                Assert.Equal("BAD_ARGS", ctrl.Code);
            }
            finally
            {
                SafeDelete(root);
            }
        }
        /// <summary>
        /// 口径边界——正文纯空白拒、keyword 超长拒、id 非正整数拒、limit 上界拒而下界回落默认。
        /// </summary>
        [Fact]
        public void Contract_ArgumentBounds()
        {
            string root = NewRoot();
            try
            {
                DbService db = new DbService(root);
                Assert.True(db.Create("bounds", "").Ok);
                Assert.True(db.Put("bounds", "条目一", "正文", "").Ok);
                DbResult blank = db.Put("bounds", "条目二", "   ", "");
                Assert.False(blank.Ok);
                Assert.Equal("BAD_ARGS", blank.Code);
                string longKeyword = new string('k', 201);
                DbResult kw = db.Query("bounds", longKeyword, 10, "");
                Assert.False(kw.Ok);
                Assert.Equal("BAD_ARGS", kw.Code);
                DbResult idText = db.Get("bounds", "id", "abc", "");
                Assert.Equal("BAD_ARGS", idText.Code);
                DbResult idZero = db.Get("bounds", "id", "0", "");
                Assert.Equal("BAD_ARGS", idZero.Code);
                DbResult limitOver = db.Query("bounds", "", 201, "");
                Assert.Equal("BAD_ARGS", limitOver.Code);
                DbResult limitZero = db.Query("bounds", "", 0, "");
                Assert.True(limitZero.Ok);
                Assert.Equal(1, ReadInt(limitZero.Data, "shown"));
            }
            finally
            {
                SafeDelete(root);
            }
        }
        /// <summary>
        /// 分页与排序——offset 翻页三页自洽（nextOffset 衔接）；超界返回空页；order 非枚举 / offset 负数拒。
        /// </summary>
        [Fact]
        public void QueryPaging_OffsetAndOrder()
        {
            string root = NewRoot();
            try
            {
                DbService db = new DbService(root);
                Assert.True(db.Create("page", "").Ok);
                for (int i = 1; i <= 5; i = i + 1)
                {
                    Assert.True(db.Put("page", "分页" + i.ToString(), "正文" + i.ToString(), "").Ok);
                }
                DbResult p1 = db.Query("page", "", 2, 0, "id", "");
                Assert.Equal(2, ReadInt(p1.Data, "shown"));
                Assert.Equal(0, ReadInt(p1.Data, "offset"));
                Assert.Equal(2, ReadInt(p1.Data, "nextOffset"));
                DbResult p2 = db.Query("page", "", 2, 2, "id", "");
                Assert.Equal(2, ReadInt(p2.Data, "shown"));
                Assert.Equal(4, ReadInt(p2.Data, "nextOffset"));
                DbResult p3 = db.Query("page", "", 2, 4, "id", "");
                Assert.Equal(1, ReadInt(p3.Data, "shown"));
                Assert.Equal(5, ReadInt(p3.Data, "nextOffset"));
                DbResult over = db.Query("page", "", 2, 99, "id", "");
                Assert.True(over.Ok);
                Assert.Equal(0, ReadInt(over.Data, "shown"));
                DbResult badOrder = db.Query("page", "", 2, 0, "kkk", "");
                Assert.Equal("BAD_ARGS", badOrder.Code);
                DbResult negOffset = db.Query("page", "", 2, -1, "updated", "");
                Assert.Equal("BAD_ARGS", negOffset.Code);
            }
            finally
            {
                SafeDelete(root);
            }
        }
        /// <summary>
        /// 管理面——库级明细列出活跃 / 墓碑；删库 confirm 不符拒；未登记拒；删后查询报 NOT_FOUND。
        /// </summary>
        [Fact]
        public void Admin_ListDetailAndDrop()
        {
            string root = NewRoot();
            try
            {
                DbService db = new DbService(root);
                Assert.True(db.Create("admina", "").Ok);
                Assert.True(db.Put("admina", "甲", "正文", "").Ok);
                Assert.True(db.Put("admina", "乙", "正文", "").Ok);
                Assert.True(db.Delete("admina", "乙", "").Ok);
                DbResult libs = db.ListLibsDetail("");
                Assert.True(libs.Ok);
                Assert.Contains("admina", libs.Data);
                DbResult wrongConfirm = db.DropLib("admina", "other", "");
                Assert.Equal("BAD_ARGS", wrongConfirm.Code);
                DbResult missing = db.DropLib("nolibx", "nolibx", "");
                Assert.Equal("DB_LIB_NOT_FOUND", missing.Code);
                DbResult drop = db.DropLib("admina", "admina", "");
                Assert.True(drop.Ok);
                DbResult after = db.ListLibsDetail("");
                Assert.DoesNotContain("admina", after.Data);
                DbResult gone = db.Query("admina", "", 10, "");
                Assert.Equal("DB_LIB_NOT_FOUND", gone.Code);
            }
            finally
            {
                SafeDelete(root);
            }
        }
    }
}
