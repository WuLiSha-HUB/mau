using System;
using System.Collections.Concurrent;
using System.Collections.Generic;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;

namespace Mau.Development
{
    /// <summary>
    /// 文件快照——磁盘签名（mtime + size，NTFS 微秒级元数据调用，无 IO 读）。
    /// 仅作判变快链：元数据全等时仍以内容比较为准（同秒同大小的外部改写必须被看见）。
    /// </summary>
    internal struct ProjectSnapshot
    {
        /// <summary>
        /// 最后写入时间（UTC）
        /// </summary>
        public DateTime Utc;

        /// <summary>
        /// 文件长度
        /// </summary>
        public long Length;
    }

    /// <summary>
    /// 项目缓存条目——项目键隔离单元（design-ch4-cs.md §3.1）
    /// 树/编译/语义三态在一个对象图内；快照签名监管磁盘权威。
    /// </summary>
    internal sealed class ProjectCache
    {
        /// <summary>
        /// csproj 绝对路径（池键）
        /// </summary>
        public string CsprojPath;

        /// <summary>
        /// csproj 所在目录
        /// </summary>
        public string ProjectDir;

        /// <summary>
        /// 程序集名（csproj AssemblyName 或文件名）
        /// </summary>
        public string AssemblyName;

        /// <summary>
        /// 目标框架（net10.0 等）
        /// </summary>
        public string Tfm;

        /// <summary>
        /// csproj Nullable 使能
        /// </summary>
        public bool NullableEnable;

        /// <summary>
        /// 是否可执行程序（OutputType=Exe/WinExe）
        /// </summary>
        public bool IsExe;

        /// <summary>
        /// 源排除清单——csproj DefaultItemExcludes 解析（子项目目录如 CatHome4.Core.Tests\**）
        /// </summary>
        public List<string> DefaultExcludes;

        /// <summary>
        /// 全量源文件（绝对路径；排除 obj/bin + DefaultExcludes）
        /// </summary>
        public string[] SourceFiles;

        /// <summary>
        /// 树态——全量常驻（不可变 SyntaxTree）
        /// </summary>
        public ConcurrentDictionary<string, SyntaxTree> Trees;

        /// <summary>
        /// 磁盘签名表
        /// </summary>
        public ConcurrentDictionary<string, ProjectSnapshot> Stamps;

        /// <summary>
        /// 语义态——惰性 + 缓存
        /// </summary>
        public ConcurrentDictionary<string, SemanticModel> Semantics;

        /// <summary>
        /// 编译态——不可变引用（volatile 交换）
        /// </summary>
        private volatile CSharpCompilation _compilation;

        /// <summary>
        /// 当前编译
        /// </summary>
        public CSharpCompilation Compilation
        {
            get
            {
                return _compilation;
            }
            set
            {
                _compilation = value;
            }
        }

        /// <summary>
        /// 引用集（锁内重建——cs.build 后置 dirty）
        /// </summary>
        public List<MetadataReference> References;

        /// <summary>
        /// 引用集脏标记——build 成功后置真，下次语义操作前重建（bin 产物替换）
        /// </summary>
        public bool ReferencesDirty;

        /// <summary>
        /// per-project 锁——写/刷新串行
        /// </summary>
        public object Gate;

        /// <summary>
        /// LRU 时间戳
        /// </summary>
        public long LastAccess;

        /// <summary>
        /// 创建空缓存条目
        /// </summary>
        public ProjectCache()
        {
            CsprojPath = "";
            ProjectDir = "";
            AssemblyName = "";
            Tfm = "net10.0";
            NullableEnable = false;
            IsExe = false;
            DefaultExcludes = new List<string>();
            SourceFiles = Array.Empty<string>();
            Trees = new ConcurrentDictionary<string, SyntaxTree>(StringComparer.OrdinalIgnoreCase);
            Stamps = new ConcurrentDictionary<string, ProjectSnapshot>(StringComparer.OrdinalIgnoreCase);
            Semantics = new ConcurrentDictionary<string, SemanticModel>(StringComparer.OrdinalIgnoreCase);
            _compilation = null!;
            References = new List<MetadataReference>();
            ReferencesDirty = false;
            Gate = new object();
            LastAccess = 0;
        }
    }
}
