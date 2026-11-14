using System;
using CatHome4.Admin;
using CatHome4.Contracts;
using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// 宿主状态读面实现——外壳需要但由内核持有的量（design-ch4-platform §一：差异只在出口，内核提供读面）。
    /// 数据源全部取内核既有单点（版本元数据 / HTTP 宿主端口 / 会话宿主端口 / 配置存储），不做第二份缓存。
    /// </summary>
    internal sealed class HostView : IHostView
    {
        /// <summary>数据根——构造时一次性解析（与内核 ResolveDataRoot 同源）。</summary>
        private readonly string _dataRoot;

        /// <summary>
        /// 构造读面——解析数据根（外壳「打开数据目录」与提示窗使用同一值）。
        /// </summary>
        public HostView()
        {
            _dataRoot = Program.ResolveDataRoot();
        }

        /// <summary>版本号——exe 元数据 InformationalVersion 主段（空 → "?"）。</summary>
        public string VersionInfo
        {
            get
            {
                string version = global::Mau.Runtime.VersionInfo.GetEntryVersion();
                if (version.Length == 0)
                {
                    version = "?";
                }
                return version;
            }
        }

        /// <summary>构建时刻——exe 元数据（装配时刻）。</summary>
        public string BuildTime
        {
            get { return global::Mau.Runtime.VersionInfo.GetEntryBuildTime(); }
        }

        /// <summary>管理面板端口——主 HTTP 宿主端口（未监听为 0）。</summary>
        public int PanelPort
        {
            get { return Program.PanelPortValue; }
        }

        /// <summary>Majordomo 独立端口（未拉起为 0）。</summary>
        public int MajorPort
        {
            get { return CatHostService.MajorPort; }
        }

        /// <summary>最近一次启动报告路径（空 = 未落盘）。</summary>
        public string StartupReportPath
        {
            get { return Program.StartupReportPath; }
        }

        /// <summary>数据根绝对路径。</summary>
        public string DataRoot
        {
            get { return _dataRoot; }
        }

        /// <summary>端口段区标识（dev / deploy）——托盘提示区分两实例。</summary>
        public string AreaLabel
        {
            get { return Program.PortAreaLabel; }
        }

        /// <summary>
        /// 启动降级条目快照——与启动提示窗 / 面板读同一数据源。
        /// </summary>
        /// <returns>条目数组（按登记顺序）</returns>
        public StartupDegradeEntry[] StartupDegrades()
        {
            return StartupDegradation.Snapshot();
        }

        /// <summary>
        /// 自启开关现值——读配置 app.autostart（配置为真相，注册表为物化）。
        /// </summary>
        /// <returns>true = 开机自启开启</returns>
        public bool IsAutoStartOn()
        {
            ConfigStore cfg = null;
            if (DataBox.TryResolve<ConfigStore>(out cfg) && cfg != null)
            {
                string value = cfg.Get(Program.AutoStartConfigKey, "false");
                return value == "true" || value == "1";
            }
            return false;
        }

        /// <summary>
        /// 写自启开关——schema 白名单校验后落配置（物化由外壳在同一次动作后同步）。
        /// </summary>
        /// <param name="on">目标状态</param>
        /// <returns>空串 = 成功；否则为失败原因</returns>
        public string SetAutoStart(bool on)
        {
            string newValue = "false";
            if (on)
            {
                newValue = "true";
            }
            ConfigStore cfg = null;
            ConfigSchema schema = null;
            DataBox.TryResolve<ConfigStore>(out cfg);
            DataBox.TryResolve<ConfigSchema>(out schema);
            string error = "";
            if (cfg == null || !cfg.SetChecked(Program.AutoStartConfigKey, newValue, schema, out error))
            {
                return error;
            }
            return "";
        }
    }
}
