using System;
using System.IO;
using System.Text;
using Mau.Development;
using Mau.Runtime;
using Xunit;

namespace Mau.Development.Tests
{
    /// <summary>
    /// cs.format 空白 / 缩进规整测试——干跑探针（零写入） / 写盘保真（BOM / 换行） / 参数面零容忍 / 排除面。
    /// </summary>
    public class FormatTests : IDisposable
    {
        /// <summary>
        /// 临时工程根（受控根）
        /// </summary>
        private readonly string _root;

        /// <summary>
        /// 整块缺一级缩进的探针文件（带 BOM）
        /// </summary>
        private readonly string _badPath;

        /// <summary>
        /// 整块缺一级缩进的探针文件（无 BOM）
        /// </summary>
        private readonly string _badNoBomPath;

        /// <summary>
        /// 整块缺一级缩进的探针文件（LF 行尾）
        /// </summary>
        private readonly string _badLfPath;

        /// <summary>
        /// 行尾混合探针文件（多数 CRLF + 少量 LF）
        /// </summary>
        private readonly string _mixedPath;

        /// <summary>
        /// 已规范文件（零差异基线）
        /// </summary>
        private readonly string _cleanPath;

        /// <summary>
        /// 生成物命名文件（排除面）
        /// </summary>
        private readonly string _generatedPath;

        /// <summary>
        /// 编码桥实例（受控根 = 临时根）
        /// </summary>
        private readonly MauRoslynBridge _bridge;

        /// <summary>
        /// 创建夹具——写探针文件 + 建桥
        /// </summary>
        public FormatTests()
        {
            _root = Path.Combine(Path.GetTempPath(), "mau_cs_format_" + Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(_root);
            _badPath = Path.Combine(_root, "Bad.cs");
            _badNoBomPath = Path.Combine(_root, "BadNoBom.cs");
            _badLfPath = Path.Combine(_root, "BadLf.cs");
            _mixedPath = Path.Combine(_root, "Mixed.cs");
            _cleanPath = Path.Combine(_root, "Clean.cs");
            _generatedPath = Path.Combine(_root, "FL_Generated.cs");
            File.WriteAllText(_badPath, BadText(), new UTF8Encoding(true));
            File.WriteAllText(_badNoBomPath, BadText(), new UTF8Encoding(false));
            File.WriteAllText(_badLfPath, BadTextLf(), new UTF8Encoding(true));
            File.WriteAllText(_mixedPath, MixedText(), new UTF8Encoding(true));
            File.WriteAllText(_cleanPath, CleanText(), new UTF8Encoding(true));
            File.WriteAllText(_generatedPath, BadText(), new UTF8Encoding(true));
            WorkspaceConfig.RootEntry entry = new WorkspaceConfig.RootEntry();
            entry.Id = "tmp";
            entry.Path = _root;
            entry.Writable = true;
            _bridge = new MauRoslynBridge(new WorkspaceConfig.RootEntry[] { entry });
        }

        /// <summary>
        /// 清理——删临时根
        /// </summary>
        public void Dispose()
        {
            try
            {
                if (Directory.Exists(_root))
                {
                    Directory.Delete(_root, true);
                }
            }
            catch (IOException ex)
            {
                // 测试夹具清理——尽力删除（目录被占用等不影响用例结论）
                Console.WriteLine("[测试清理] 工作区目录删除失败: " + ex.Message);
            }
        }

        /// <summary>
        /// check——报需要规整的文件与行数，且零写入（文件内容与字节数不变）
        /// </summary>
        [Fact]
        public void CheckReportsWithoutWriting()
        {
            string before = File.ReadAllText(_badPath);
            long beforeLength = new FileInfo(_badPath).Length;
            string result = InvokeFormat(_root, "check");
            Assert.StartsWith("{\"ok\":true,\"tool\":\"cs-format\"", result);
            Assert.Contains("Bad.cs", result);
            Assert.Equal(before, File.ReadAllText(_badPath));
            Assert.Equal(beforeLength, new FileInfo(_badPath).Length);
        }

        /// <summary>
        /// apply——整块缺一级缩进被补齐（成员 4 空格 / 语句 8 空格），BOM 与 CRLF 全保真
        /// </summary>
        [Fact]
        public void ApplyFixesIndentAndKeepsBom()
        {
            string result = InvokeFormat(_badPath, "apply");
            Assert.StartsWith("{\"ok\":true,\"tool\":\"cs-format\"", result);
            string text = File.ReadAllText(_badPath);
            Assert.Contains("    public int Run()\r\n", text);
            Assert.Contains("        {\r\n            int value = 1;\r\n            return value;\r\n        }\r\n", text);
            Assert.True(HasBom(_badPath), "BOM 丢失");
            Assert.True(AllCrlf(text), "出现非 CRLF 行尾（混行）");
        }

        /// <summary>
        /// apply——无 BOM 文件写盘后仍无 BOM（保真原文状态，不擅自添加）
        /// </summary>
        [Fact]
        public void ApplyKeepsMissingBom()
        {
            string result = InvokeFormat(_badNoBomPath, "apply");
            Assert.StartsWith("{\"ok\":true,\"tool\":\"cs-format\"", result);
            Assert.False(HasBom(_badNoBomPath), "无 BOM 文件被写入 BOM");
        }
        /// <summary>
        /// apply——LF 文件保持 LF（行尾保真：lf → lf，不引入 CRLF 混行）
        /// </summary>
        [Fact]
        public void ApplyKeepsLfNewline()
        {
            string result = InvokeFormat(_badLfPath, "apply");
            Assert.StartsWith("{\"ok\":true,\"tool\":\"cs-format\"", result);
            string text = File.ReadAllText(_badLfPath);
            Assert.Contains("    public int Run()\n", text);
            Assert.True(AllLf(text), "出现 CRLF 行尾（LF 文件被写成混行）");
        }
        /// <summary>
        /// apply——行尾混合文件按多数归一（落盘后不再是 mixed）并在正文报出混合提示
        /// </summary>
        [Fact]
        public void ApplyNormalizesMixedToMajority()
        {
            string result = InvokeFormat(_mixedPath, "apply");
            Assert.StartsWith("{\"ok\":true,\"tool\":\"cs-format\"", result);
            Assert.Contains("MIXED|", result);
            string text = File.ReadAllText(_mixedPath);
            Assert.True(AllCrlf(text), "混合行尾未被归一");
        }
        /// <summary>
        /// apply——LF 文件二次 check 零差异（幂等；行尾归一后不再报「非幂等」）
        /// </summary>
        [Fact]
        public void ApplyKeepsLfIdempotent()
        {
            string first = InvokeFormat(_badLfPath, "apply");
            Assert.StartsWith("{\"ok\":true,\"tool\":\"cs-format\"", first);
            string second = InvokeFormat(_badLfPath, "check");
            Assert.StartsWith("{\"ok\":true,\"tool\":\"cs-format\"", second);
            Assert.DoesNotContain("| lines=", second);
        }

        /// <summary>
        /// check——已规范文件零差异（幂等基线），生成物命名文件被排除面跳过
        /// </summary>
        [Fact]
        public void CleanAndGeneratedFilesUntouched()
        {
            string result = InvokeFormat(_root, "check");
            Assert.DoesNotContain("Clean.cs", result);
            Assert.DoesNotContain("FL_Generated.cs", result);
        }

        /// <summary>
        /// apply——目录入口规整后，二次 check 零差异（幂等）
        /// </summary>
        [Fact]
        public void ApplyIsIdempotent()
        {
            string first = InvokeFormat(_root, "apply");
            Assert.StartsWith("{\"ok\":true,\"tool\":\"cs-format\"", first);
            string second = InvokeFormat(_root, "check");
            Assert.StartsWith("{\"ok\":true,\"tool\":\"cs-format\"", second);
            Assert.DoesNotContain("Bad.cs", second);
        }

        /// <summary>
        /// 参数面零容忍——缺 path / 未知参数 / 非法 mode 一律 ERR|BAD_ARGS（不静默回落）
        /// </summary>
        [Fact]
        public void BadArgsAreRejected()
        {
            string missing = Invoke("format", "{\"mode\":\"check\"}");
            Assert.StartsWith("ERR|BAD_ARGS", missing);
            string unknown = Invoke("format", "{\"path\":\"" + Escape(_root) + "\",\"foo\":\"1\"}");
            Assert.StartsWith("ERR|BAD_ARGS", unknown);
            string badMode = Invoke("format", "{\"path\":\"" + Escape(_root) + "\",\"mode\":\"fix\"}");
            Assert.StartsWith("ERR|BAD_ARGS", badMode);
        }

        /// <summary>
        /// 宿主注入保留键放行——catId（P9.4 猫级路由，宿主每次调用注入，不进工具声明面）不触发 BAD_ARGS，功能面照常
        /// </summary>
        [Fact]
        public void HostInjectedArgsAccepted()
        {
            string args = "{\"path\":\"" + Escape(_root) + "\",\"mode\":\"check\",\"catId\":\"majordomo\"}";
            string result = Invoke("format", args);
            Assert.StartsWith("{\"ok\":true,\"tool\":\"cs-format\"", result);
            Assert.Contains("Bad.cs", result);
        }

        /// <summary>
        /// 越界路径拒绝——受控根外路径 ERR|BAD_PATH
        /// </summary>
        [Fact]
        public void OutOfRootPathRejected()
        {
            string outside = Path.Combine(Path.GetTempPath(), "outside_" + Guid.NewGuid().ToString("N") + ".cs");
            string result = InvokeFormat(outside, "check");
            Assert.StartsWith("ERR|BAD_PATH", result);
        }
        /// <summary>
        /// csproj 入口按**项目源文件集**收集——DefaultItemExcludes 子项目目录排除生效（同级子项目不被吞）；
        /// 与扫描面同一实现（CollectProjectSources），项目边界不再被「取所在目录递归」放大。
        /// </summary>
        [Fact]
        public void ProjectEntryRespectsSubProjectExcludes()
        {
            string projDir = Path.Combine(_root, "Domain");
            string subDir = Path.Combine(projDir, "Domain.Sub");
            Directory.CreateDirectory(subDir);
            File.WriteAllText(Path.Combine(projDir, "Main.cs"), BadText(), new UTF8Encoding(true));
            File.WriteAllText(Path.Combine(subDir, "Sub.cs"), BadText(), new UTF8Encoding(true));
            // 排除式用平台分隔符——MSBuild glob 在 Windows 吃反斜杠、在 Linux 只认正斜杠
            string csproj = "<Project Sdk=\"Microsoft.NET.Sdk\">" + Environment.NewLine +
                "  <PropertyGroup>" + Environment.NewLine +
                "    <TargetFramework>net10.0</TargetFramework>" + Environment.NewLine +
                "    <DefaultItemExcludes>$(DefaultItemExcludes);Domain.Sub" + Path.DirectorySeparatorChar + "**</DefaultItemExcludes>" + Environment.NewLine +
                "  </PropertyGroup>" + Environment.NewLine +
                "</Project>" + Environment.NewLine;
            File.WriteAllText(Path.Combine(projDir, "Domain.csproj"), csproj, new UTF8Encoding(true));
            string result = InvokeFormat(Path.Combine(projDir, "Domain.csproj"), "check");
            Assert.StartsWith("{\"ok\":true,\"tool\":\"cs-format\"", result);
            Assert.Contains("\"items\":1", result);
            Assert.Contains("Main.cs", result);
            Assert.DoesNotContain("Sub.cs", result);
        }

        /// <summary>
        /// 调用 cs.format——mode 缺省即 check
        /// </summary>
        /// <param name="path">路径参数</param>
        /// <param name="mode">模式</param>
        /// <returns>结果文本</returns>
        private string InvokeFormat(string path, string mode)
        {
            string args = "{\"path\":\"" + Escape(path) + "\",\"mode\":\"" + mode + "\"}";
            return Invoke("format", args);
        }

        /// <summary>
        /// 调用桥——异常不外泄断言
        /// </summary>
        /// <param name="method">方法名</param>
        /// <param name="argsJson">参数 JSON</param>
        /// <returns>结果文本</returns>
        private string Invoke(string method, string argsJson)
        {
            string result;
            _bridge.Invoke(method, argsJson, out result);
            return result;
        }

        /// <summary>
        /// JSON 路径转义（Windows 反斜杠）
        /// </summary>
        /// <param name="path">路径</param>
        /// <returns>转义文本</returns>
        private static string Escape(string path)
        {
            return path.Replace("\\", "\\\\");
        }

        /// <summary>
        /// BOM 探测
        /// </summary>
        /// <param name="path">文件路径</param>
        /// <returns>有 BOM</returns>
        private static bool HasBom(string path)
        {
            byte[] bytes = File.ReadAllBytes(path);
            return bytes.Length >= 3 && bytes[0] == 0xEF && bytes[1] == 0xBB && bytes[2] == 0xBF;
        }

        /// <summary>
        /// 全 CRLF 判定
        /// </summary>
        /// <param name="text">文本</param>
        /// <returns>全 CRLF</returns>
        private static bool AllCrlf(string text)
        {
            return text.Replace("\r\n", "").IndexOf('\n') < 0;
        }

        /// <summary>
        /// 缺一级缩进的探针源码——成员顶格 / 语句 4 空格（模拟 F5 结转形态）
        /// </summary>
        /// <returns>源码文本</returns>
        private static string BadText()
        {
            return "using System;\r\n" +
                "\r\n" +
                "namespace Probe\r\n" +
                "{\r\n" +
                "public sealed class Bad\r\n" +
                "{\r\n" +
                "/// <summary>\r\n" +
                "/// 探针\r\n" +
                "/// </summary>\r\n" +
                "/// <returns>常量</returns>\r\n" +
                "public int Run()\r\n" +
                "{\r\n" +
                "int value = 1;\r\n" +
                "return value;\r\n" +
                "}\r\n" +
                "}\r\n" +
                "}\r\n";
        }

        /// <summary>
        /// 已规范探针源码——零差异基线（Formatter 不动）
        /// </summary>
        /// <returns>源码文本</returns>
        private static string CleanText()
        {
            return "using System;\r\n" +
                "\r\n" +
                "namespace Probe\r\n" +
                "{\r\n" +
                "    public sealed class Clean\r\n" +
                "    {\r\n" +
                "        /// <summary>\r\n" +
                "        /// 探针\r\n" +
                "        /// </summary>\r\n" +
                "        /// <returns>常量</returns>\r\n" +
                "        public int Run()\r\n" +
                "        {\r\n" +
                "            return 1;\r\n" +
                "        }\r\n" +
                "    }\r\n" +
                "}\r\n";
        }
        /// <summary>
        /// 全 LF 判定——不含 CR
        /// </summary>
        /// <param name="text">文本</param>
        /// <returns>全 LF</returns>
        private static bool AllLf(string text)
        {
            return text.IndexOf('\r') < 0;
        }
        /// <summary>
        /// 缺一级缩进的探针源码（LF 行尾）
        /// </summary>
        /// <returns>源码文本</returns>
        private static string BadTextLf()
        {
            return BadText().Replace("\r\n", "\n");
        }
        /// <summary>
        /// 行尾混合探针源码——多数 CRLF + 第 2/5 行后为 LF
        /// </summary>
        /// <returns>源码文本</returns>
        private static string MixedText()
        {
            string text = BadText();
            string[] lines = text.Split(new string[] { "\r\n" }, StringSplitOptions.None);
            StringBuilder sb = new StringBuilder();
            for (int i = 0; i < lines.Length; i = i + 1)
            {
                sb.Append(lines[i]);
                if (i < lines.Length - 1)
                {
                    if (i == 2 || i == 5)
                    {
                        sb.Append("\n");
                    }
                    else
                    {
                        sb.Append("\r\n");
                    }
                }
            }
            return sb.ToString();
        }
    }
}
