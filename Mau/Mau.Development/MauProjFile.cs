using System;
using System.Collections.Generic;
using System.IO;
using System.Text;

namespace Mau.Development
{
    /// <summary>
    /// mauproj 组工程文件（v1.0 收紧版）——行指令式：组/版本/引用/文件。
    /// 对应 design-ch4-deploy.md §四：一组一个 mauproj，翻译+编译统一链的输入声明。
    /// 移除 v2 的 依赖:/bricks: 字段——v3 积木是文本资产，依赖面由翻译器自动扫描。
    /// B3 下沉：自 Mau.Cli 移入 Mau.Development 共享库（Mau.Cli 与宿主 CatHome4 共用——消灭双轨；宿主原名 CH4.Entry）。
    /// </summary>
    public sealed class MauProjFile
    {
        /// <summary>
        /// 组名——输出 DLL 名 FL_&lt;组名&gt;.dll；mauproj 文件名必须 = 组名（public/src/&lt;组名&gt;/ 目录约定）
        /// </summary>
        public string Name;

        /// <summary>
        /// 组版本
        /// </summary>
        public string Version;

        /// <summary>
        /// 额外引用——程序集名（默认基座 Mau.Runtime/Mau.Contracts 自动带；引用: 声明映射 Mau-public/ dll）
        /// </summary>
        public List<string> References;

        /// <summary>
        /// 成员 .mau 文件——相对组目录；空 = 递归收集组目录全部子目录 *.mau
        /// </summary>
        public List<string> Files;

        /// <summary>
        /// 组目录——mauproj 所在目录（文件收集基准）
        /// </summary>
        public string DirectoryPath;

        /// <summary>
        /// 构造空 mauproj
        /// </summary>
        public MauProjFile()
        {
            Name = "";
            Version = "";
            References = new List<string>();
            Files = new List<string>();
            DirectoryPath = "";
        }

        /// <summary>
        /// 从文件加载并解析 mauproj
        /// </summary>
        /// <param name="path">mauproj 文件路径</param>
        /// <returns>解析结果（File 非空 = 成功）</returns>
        public static MauProjParseResult Load(string path)
        {
            MauProjParseResult result = new MauProjParseResult();
            if (!File.Exists(path))
            {
                result.Error = "文件不存在: " + path;
                return result;
            }
            string content = File.ReadAllText(path).Replace("\r\n", "\n");

            // [段1] 行指令解析
            MauProjFile file = new MauProjFile();
            file.DirectoryPath = Path.GetDirectoryName(Path.GetFullPath(path)) ?? ".";
            bool nameSeen = false;
            string[] bodyLines = content.Split('\n');
            for (int i = 0; i < bodyLines.Length; i++)
            {
                string trimmed = bodyLines[i].Trim();
                int lineNumber = i + 1;
                if (trimmed.Length == 0 || trimmed.StartsWith("//"))
                {
                    continue;
                }
                int colon = trimmed.IndexOf(':');
                if (colon <= 0)
                {
                    result.Error = "第 " + lineNumber + " 行格式错误——缺少 '关键字: 值' 分隔";
                    return result;
                }
                string key = trimmed.Substring(0, colon).Trim();
                string value = trimmed.Substring(colon + 1).Trim();
                if (key == "组")
                {
                    if (nameSeen)
                    {
                        result.Error = "第 " + lineNumber + " 行——组名重复声明";
                        return result;
                    }
                    nameSeen = true;
                    file.Name = value;
                }
                else if (key == "版本")
                {
                    if (file.Version.Length > 0)
                    {
                        result.Error = "第 " + lineNumber + " 行——版本重复声明";
                        return result;
                    }
                    file.Version = value;
                }
                else if (key == "引用")
                {
                    AddList(file.References, value);
                }
                else if (key == "文件")
                {
                    AddList(file.Files, value);
                }
                else
                {
                    result.Error = "第 " + lineNumber + " 行——未知指令: " + key;
                    return result;
                }
            }
            if (!nameSeen)
            {
                result.Error = "缺少必填指令: 组";
                return result;
            }
            if (file.Name.Length == 0)
            {
                result.Error = "组名为空——组: 指令值不能为空";
                return result;
            }
            if (!IsSafeName(file.Name))
            {
                result.Error = "组名非法——仅允许字母/数字/下划线（防路径逃逸）: " + file.Name;
                return result;
            }

            // [段3] 转义解码——所有值统一 Unescape
            file.Name = Unescape(file.Name);
            file.Version = Unescape(file.Version);
            for (int i = 0; i < file.References.Count; i++)
            {
                file.References[i] = Unescape(file.References[i]);
            }
            for (int i = 0; i < file.Files.Count; i++)
            {
                file.Files[i] = Unescape(file.Files[i]);
            }

            result.File = file;
            return result;
        }

        /// <summary>
        /// 收集成员文件——Files 非空按列表（相对组目录，跳过缺失/越界），空则递归收集全部 *.mau
        /// </summary>
        /// <returns>成员文件绝对路径列表</returns>
        public List<string> CollectFiles()
        {
            List<string> collected = new List<string>();
            if (Files.Count > 0)
            {
                for (int i = 0; i < Files.Count; i++)
                {
                    string abs = Path.Combine(DirectoryPath, Files[i]);
                    string full = Path.GetFullPath(abs);
                    string rootFull = Path.GetFullPath(DirectoryPath);
                    if (!full.StartsWith(rootFull + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase))
                    {
                        continue;
                    }
                    if (File.Exists(full))
                    {
                        collected.Add(full);
                    }
                }
            }
            else
            {
                string[] all = Directory.GetFiles(DirectoryPath, "*.mau", SearchOption.AllDirectories);
                Array.Sort(all, StringComparer.OrdinalIgnoreCase);
                for (int i = 0; i < all.Length; i++)
                {
                    collected.Add(all[i]);
                }
            }
            return collected;
        }

        /// <summary>
        /// 解析引用清单——引用: 声明 → Mau-public/ 部署区 dll 绝对路径（引用源铁律：编译/运行时同源）
        /// </summary>
        /// <param name="mauPublicDir">Mau-public/ 部署区目录</param>
        /// <returns>解析后 dll 绝对路径列表；缺失引用写入 errors</returns>
        public List<string> ResolveReferences(string mauPublicDir, out List<string> errors)
        {
            errors = new List<string>();
            List<string> resolved = new List<string>();
            for (int i = 0; i < References.Count; i++)
            {
                string raw = References[i];
                if (raw.Length == 0)
                {
                    continue;
                }
                string full = Path.Combine(mauPublicDir, raw + ".dll");
                if (!File.Exists(full))
                {
                    errors.Add("引用 " + raw + " 未找到（Mau-public/ 缺失: " + full + "）——先 dotnet publish Mau-public");
                    continue;
                }
                if (!resolved.Contains(full))
                {
                    resolved.Add(full);
                }
            }
            return resolved;
        }

        /// <summary>
        /// 值转义解码——\, 转义字面逗号；\\ 转义字面反斜杠
        /// </summary>
        /// <param name="value">原始值</param>
        /// <returns>解码后值</returns>
        private static string Unescape(string value)
        {
            if (value.IndexOf('\\') < 0)
            {
                return value;
            }
            StringBuilder sb = new StringBuilder();
            for (int i = 0; i < value.Length; i++)
            {
                char c = value[i];
                if (c == '\\' && i + 1 < value.Length)
                {
                    char next = value[i + 1];
                    if (next == ',' || next == '\\')
                    {
                        sb.Append(next);
                        i = i + 1;
                        continue;
                    }
                }
                sb.Append(c);
            }
            return sb.ToString();
        }

        /// <summary>
        /// 追加列表值——按分隔符拆分（\, 转义字面逗号），空段跳过
        /// </summary>
        /// <param name="list">目标列表</param>
        /// <param name="value">分隔文本</param>
        /// <param name="separator">分隔符（默认逗号）</param>
        private static void AddList(List<string> list, string value, char separator = ',')
        {
            StringBuilder current = new StringBuilder();
            for (int i = 0; i < value.Length; i++)
            {
                char c = value[i];
                if (c == '\\' && i + 1 < value.Length && (value[i + 1] == ',' || value[i + 1] == '\\'))
                {
                    current.Append(c);
                    current.Append(value[i + 1]);
                    i = i + 1;
                    continue;
                }
                if (c == separator)
                {
                    string part = current.ToString().Trim();
                    if (part.Length > 0)
                    {
                        list.Add(part);
                    }
                    current.Clear();
                    continue;
                }
                current.Append(c);
            }
            string lastPart = current.ToString().Trim();
            if (lastPart.Length > 0)
            {
                list.Add(lastPart);
            }
        }

        /// <summary>
        /// 安全组名判定——仅字母/数字/下划线（防路径逃逸）
        /// </summary>
        /// <param name="name">组名</param>
        /// <returns>安全为真</returns>
        internal static bool IsSafeName(string name)
        {
            if (name.Length == 0)
            {
                return false;
            }
            for (int i = 0; i < name.Length; i++)
            {
                char c = name[i];
                if (!((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'))
                {
                    return false;
                }
            }
            return true;
        }
    }

    /// <summary>
    /// mauproj 解析结果
    /// </summary>
    public sealed class MauProjParseResult
    {
        /// <summary>
        /// 解析出的工程文件（成功时非空）
        /// </summary>
        public MauProjFile? File;

        /// <summary>
        /// 错误信息（非空 = 失败）
        /// </summary>
        public string Error = "";
    }
}