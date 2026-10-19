using System;
using System.Collections.Generic;
using System.Text;
using System.Text.Json;
using Mau.Contracts;
using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// 宿主会话实体——会话元数据持久化段（A201 · design-ch4-protocol §十三）。
    /// 会话自身参数（实例 ID / displayName / 时间戳 / 前文三项 / 两级 token 累计）独立落盘，
    /// 与 cat.cfg（配置面）· jsonl（真实前文）· view.jsonl（视图前文）并列——重启可恢复。
    /// </summary>
    internal sealed partial class ChatSession
    {
        /// <summary>会话元数据落点解析——猫 key → 元数据文件绝对路径（入口壳注入；空=不持久化——测试环境）</summary>
        public static Func<string, string> SessionMetaPathProvider;

        /// <summary>显示名解析——猫 key → cat.cfg 现值（入口壳注入；空=session.new 不刷新显示名）</summary>
        public static Func<string, string> DisplayNameProvider;

        /// <summary>会话实例 ID——session.new 生成（显示用；不参与 LLM 请求身份与网关缓存命名空间）</summary>
        private string _sessionInstanceId = "";

        /// <summary>会话创建时刻——Unix 毫秒（session.new 重置；启动无文件时按首建生成）</summary>
        private long _sessionCreatedAt;

        /// <summary>最近一轮 token 三项快照——每次请求结算时捕获（读面空闲回落源 + 元数据落盘数据源；A202）</summary>
        private SessionTokens _lastRoundTokens;

        /// <summary>最近一次请求的前文长度（token）——落盘快照；未发起请求时读面回落此值（A202）</summary>
        private long _persistedContextTokens;

        /// <summary>最后活跃时刻落盘快照（session.json LastActiveAt）——内存前文变动时刻为 0 时读面回落源（A225b）</summary>
        private long _persistedLastActiveAt;

        /// <summary>前文总字符数——轮末 / 恢复时刷新（state 段读此缓存，不逐帧重算）</summary>
        private long _contextChars;

        /// <summary>元数据落盘器——按 Provider 路径构造（空 Provider = 不持久化）</summary>
        private SessionMetaStore _metaStore;

        /// <summary>元数据落盘器解析标志——区分「未解析」与「解析后为空」</summary>
        private bool _metaStoreResolved;

        /// <summary>会话实例 ID——显示用（不参与 LLM 请求身份）</summary>
        public string SessionInstanceId
        {
            get
            {
                return _sessionInstanceId;
            }
        }

        /// <summary>会话创建时刻——Unix 毫秒</summary>
        public long SessionCreatedAt
        {
            get
            {
                return _sessionCreatedAt;
            }
        }

        /// <summary>
        /// 设置会话显示名——session.new 时按当前 cat.cfg 刷新（A201：displayName 随新会话取最新）。
        /// </summary>
        /// <param name="displayName">显示名（空 = 保持原值）</param>
        public void SetDisplayName(string displayName)
        {
            if (displayName == null || displayName.Length == 0)
            {
                return;
            }
            _displayName = displayName;
        }

        /// <summary>
        /// 载入会话元数据——启动恢复（装配层构造后调用）。
        /// 文件缺失 = 首建（生成实例 ID + 创建时刻）；损坏 = 按首建处理（落盘器已出声）。
        /// displayName 不取文件值——构造注入的 cat.cfg 现值更权威。
        /// </summary>
        public void LoadMeta()
        {
            SessionMetaStore store = ResolveMetaStore();
            SessionMeta meta = store == null ? null : store.Load();
            long now = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
            if (meta == null)
            {
                _sessionInstanceId = SessionStore.NewSessionId();
                _sessionCreatedAt = now;
                // A221 首建——把当前注入面冻结进快照（无档会话以本次启动面为起点）
                SaveMeta();
                return;
            }
            _sessionInstanceId = meta.SessionId == null ? "" : meta.SessionId;
            if (_sessionInstanceId.Length == 0)
            {
                _sessionInstanceId = SessionStore.NewSessionId();
            }
            _sessionCreatedAt = meta.CreatedAt;
            _persistedLastActiveAt = meta.LastActiveAt;
            _sessionPrompt = meta.SessionTokens.Prompt;
            _sessionCacheHit = meta.SessionTokens.CacheHit;
            _sessionCompletion = meta.SessionTokens.Completion;
            // A202 请求边界态恢复——轮级三项 + 六态累计 + 计数 + 耗时 + Note（落什么恢复什么；相位不恢复）
            _lastRoundTokens = meta.RoundTokens;
            _persistedContextTokens = meta.ContextTokens;
            _requestCount = (int)meta.RoundRequests;
            _toolCallCount = (int)meta.RoundTools;
            lock (_phaseLock)
            {
                _phaseAccumMs[PhaseWait] = meta.RoundPhases.Wait;
                _phaseAccumMs[PhaseLink] = meta.RoundPhases.Link;
                _phaseAccumMs[PhaseThink] = meta.RoundPhases.Think;
                _phaseAccumMs[PhaseTool] = meta.RoundPhases.Tool;
                _phaseAccumMs[PhaseRun] = meta.RoundPhases.Run;
                _phaseAccumMs[PhaseReply] = meta.RoundPhases.Reply;
            }
            ApplyPersistedNote(meta.Note);
            _contextChars = meta.ContextChars;
            // A221 旧档补写——快照缺失（升级前落的档）时以本次注入面为起点冻结
            if (meta.Tools == null || meta.Tools.Length == 0)
            {
                SaveMeta();
            }
        }

        /// <summary>
        /// 落盘会话元数据——A202 唯一常规写点 = 每次 API 请求结算后（usage 处置点），
        /// 另加 session.new 初始态（与真实前文面 / 视图面同批即时落盘）。覆盖式原子写；失败出声不阻断。
        /// </summary>
        private void SaveMeta()
        {
            SessionMetaStore store = ResolveMetaStore();
            if (store == null)
            {
                return;
            }
            _contextChars = ComputeContextChars();
            SessionMeta meta = new SessionMeta();
            meta.SessionId = _sessionInstanceId;
            meta.CatId = _id;
            meta.DisplayName = _displayName;
            meta.CreatedAt = _sessionCreatedAt;
            long active = _context.LastChangeAt;
            meta.LastActiveAt = active > 0 ? active : _sessionCreatedAt;
            meta.ContextCount = _context.GetMessageCount();
            meta.ContextChars = _contextChars;
            meta.ContextTokens = ContextTokensKnown;
            SessionTokens st = new SessionTokens();
            st.Prompt = _sessionPrompt;
            st.CacheHit = _sessionCacheHit;
            st.Completion = _sessionCompletion;
            meta.SessionTokens = st;
            meta.RoundTokens = _lastRoundTokens;
            // A202 请求边界态——六态累计（含活跃态实时增量）+ 计数 + 耗时 + Note
            string stateName;
            int requests;
            var ms = GetRunState(out stateName, out requests);
            SessionPhases phases = new SessionPhases();
            phases.Wait = ms["wait"];
            phases.Link = ms["link"];
            phases.Think = ms["think"];
            phases.Tool = ms["tool"];
            phases.Run = ms["run"];
            phases.Reply = ms["reply"];
            meta.RoundPhases = phases;
            meta.RoundRequests = requests;
            meta.RoundTools = _toolCallCount;
            meta.RoundElapsedMs = ComputeRoundElapsedMs();
            meta.Note = BuildPersistedNote();
            // A221 工具面快照——与 system prompt 同命：随会话落盘，重启原样恢复
            meta.Tools = BuildPersistedTools();
            store.Save(meta);
        }

        /// <summary>
        /// 捕获本轮 token 三项快照——结算时调用（此后内存值即清零；读面空闲回落此快照）。
        /// </summary>
        /// <returns>本轮三项快照</returns>
        private SessionTokens CaptureRoundTokens()
        {
            SessionTokens t = new SessionTokens();
            t.Prompt = _usagePrompt;
            t.CacheHit = _usageCacheHit;
            t.Completion = _usageCompletion;
            return t;
        }

        /// <summary>
        /// 前文总字符数——全消息 Content 长度之和（轮末 / 恢复时按需计算，不逐帧）。
        /// </summary>
        /// <returns>字符总数</returns>
        private long ComputeContextChars()
        {
            LlmMessage[] all = _context.GetMessages();
            long total = 0;
            for (int i = 0; i < all.Length; i = i + 1)
            {
                string content = all[i].Content;
                if (content != null)
                {
                    total = total + content.Length;
                }
            }
            return total;
        }

        /// <summary>
        /// 元数据落盘器解析——按 Provider 路径构造（一次解析缓存；Provider 空 = 不持久化）。
        /// </summary>
        /// <returns>落盘器；未接线 = null</returns>
        private SessionMetaStore ResolveMetaStore()
        {
            if (_metaStoreResolved)
            {
                return _metaStore;
            }
            _metaStoreResolved = true;
            if (SessionMetaPathProvider == null || _id == null || _id.Length == 0)
            {
                return null;
            }
            string path = SessionMetaPathProvider(_id);
            if (path == null || path.Length == 0)
            {
                return null;
            }
            _metaStore = new SessionMetaStore(path);
            return _metaStore;
        }

        /// <summary>
        /// 本轮起算 → 当前的耗时毫秒——roundsum 载荷与元数据落盘共用单点（A202）。
        /// </summary>
        /// <returns>耗时毫秒（轮未起表 = 0）</returns>
        private long ComputeRoundElapsedMs()
        {
            if (_roundStartTick <= 0)
            {
                return 0;
            }
            long ms = (System.Diagnostics.Stopwatch.GetTimestamp() - _roundStartTick) * 1000 / System.Diagnostics.Stopwatch.Frequency;
            if (ms < 0)
            {
                return 0;
            }
            return ms;
        }

        /// <summary>
        /// 构建 Note 持久化形态——任务列表 + 索引 + 完成数（A202；空计划 = 空数组）。
        /// </summary>
        /// <returns>Note 落盘形态</returns>
        private SessionNote BuildPersistedNote()
        {
            SessionNote note = new SessionNote();
            if (_noteTasks == null || _noteTasks.Length == 0)
            {
                note.Tasks = new string[0];
            }
            else
            {
                note.Tasks = _noteTasks;
            }
            note.Current = _noteCurrent;
            note.Done = _noteDone;
            return note;
        }

        /// <summary>
        /// 应用落盘 Note——启动恢复（A202；未完成计划在下一轮轮末按现有机制继续自动拉起——莎 2026-10-06 批准）。
        /// </summary>
        /// <param name="note">落盘形态（Tasks 空 = 无计划，不改内存态）</param>
        private void ApplyPersistedNote(SessionNote note)
        {
            if (note.Tasks == null || note.Tasks.Length == 0)
            {
                return;
            }
            _noteTasks = note.Tasks;
            _noteCurrent = (int)note.Current;
            _noteDone = (int)note.Done;
            if (_noteCurrent < 0)
            {
                _noteCurrent = 0;
            }
            if (_noteCurrent >= _noteTasks.Length)
            {
                _noteCurrent = _noteTasks.Length - 1;
            }
        }

        /// <summary>
        /// 构建工具面持久化形态——当前注入面快照（A221；逐字节冻结，重启恢复原样沿用）。
        /// </summary>
        /// <returns>工具面快照（注入面为空 = null）</returns>
        private SessionToolSpec[] BuildPersistedTools()
        {
            if (_tools == null || _tools.Length == 0)
            {
                return null;
            }
            SessionToolSpec[] tools = new SessionToolSpec[_tools.Length];
            for (int i = 0; i < _tools.Length; i = i + 1)
            {
                SessionToolSpec item = new SessionToolSpec();
                item.Name = _tools[i].Name;
                item.Description = _tools[i].Description;
                item.ParametersJson = _tools[i].ParametersJson;
                tools[i] = item;
            }
            return tools;
        }

        /// <summary>
        /// 解析会话工具面——**构造前**调用（A221）：持久化快照优先，无快照（旧档 / 首建）回落池面。
        /// 🔴 语义 = 会话工具头与 system prompt 同命——仅 session.new 时更换，重启不改面。
        /// 快照中池已不存在的工具名剔除并出声（工具退役——失败可见，不静默）。
        /// </summary>
        /// <param name="catId">猫 key（元数据路径寻址）</param>
        /// <param name="poolSpecs">当前池面（回落源）</param>
        /// <returns>本次会话使用的工具面</returns>
        public static ToolSpec[] ResolveSessionTools(string catId, ToolSpec[] poolSpecs)
        {
            SessionToolSpec[] persisted = LoadPersistedTools(catId);
            if (persisted == null || persisted.Length == 0)
            {
                return poolSpecs;
            }
            List<ToolSpec> list = new List<ToolSpec>();
            int dropped = 0;
            for (int i = 0; i < persisted.Length; i = i + 1)
            {
                if (PoolHasName(poolSpecs, persisted[i].Name))
                {
                    list.Add(new ToolSpec(persisted[i].Name, persisted[i].Description, persisted[i].ParametersJson));
                }
                else
                {
                    dropped = dropped + 1;
                }
            }
            if (dropped > 0)
            {
                LogStore.Add("CatHome4", 2, "会话工具面快照剔除非本猫当前工具面项 " + dropped.ToString() + " 个（快照 ∩ 当前授权）: " + catId, "TOOL");
            }
            if (list.Count == 0)
            {
                return poolSpecs;
            }
            return list.ToArray();
        }

        /// <summary>
        /// 读取持久化工具面快照——路径经 SessionMetaPathProvider 解析（未接线 / 无文件 = null）。
        /// </summary>
        /// <param name="catId">猫 key</param>
        /// <returns>快照；缺失 = null</returns>
        private static SessionToolSpec[] LoadPersistedTools(string catId)
        {
            if (SessionMetaPathProvider == null || catId == null || catId.Length == 0)
            {
                return null;
            }
            string path = SessionMetaPathProvider(catId);
            if (path == null || path.Length == 0)
            {
                return null;
            }
            SessionMeta meta = new SessionMetaStore(path).Load();
            if (meta == null)
            {
                return null;
            }
            return meta.Tools;
        }

        /// <summary>
        /// 池面名字命中——快照条目是否仍在当前池中（退役剔除判据）。
        /// </summary>
        /// <param name="poolSpecs">池面</param>
        /// <param name="name">工具名</param>
        /// <returns>true = 仍在池中</returns>
        private static bool PoolHasName(ToolSpec[] poolSpecs, string name)
        {
            if (poolSpecs == null || name == null)
            {
                return false;
            }
            for (int i = 0; i < poolSpecs.Length; i = i + 1)
            {
                if (string.Equals(poolSpecs[i].Name, name, StringComparison.Ordinal))
                {
                    return true;
                }
            }
            return false;
        }
        /// <summary>
        /// 总结播报执行体（后台线程）——先播播报名（全局默认音色），再播正文（该猫外观音色）；两段共用同一个播放串行键。
        /// </summary>
        /// <param name="catKey">猫 key（日志定位用）</param>
        /// <param name="displayName">猫显示名（播报名 = displayName + 说）</param>
        /// <param name="voice">正文音色（外观池 ttsStyle；空=服务默认）</param>
        /// <param name="text">总结正文</param>
        private static void RunSummarySpeak(string catKey, string displayName, string voice, string text)
        {
            IPyService py = null;
            if (!DataBox.TryResolve<IPyService>(out py) || py == null)
            {
                LogStore.Add("CatHome4", 2, "轮末总结播报跳过——IPyService 未注入", "CHAT");
                return;
            }
            IAudioService audio = null;
            if (!DataBox.TryResolve<IAudioService>(out audio) || audio == null)
            {
                LogStore.Add("CatHome4", 2, "轮末总结播报跳过——IAudioService 未注入", "CHAT");
                return;
            }
            // [段0] 提示音——固定资产（钟声）；缺失由 ResolveChimePath 记 WARN 后跳过
            string chimePath = ResolveChimePath();
            if (chimePath.Length > 0)
            {
                string chimeResult = audio.Enqueue(RoundSummaryAudioKey, chimePath, -1f);
                if (chimeResult != null && chimeResult.Length > 0)
                {
                    LogStore.Add("CatHome4", 2, "轮末总结提示音入队失败: " + chimeResult, "CHAT");
                }
            }
            // [段1] 播报名——全局默认音色（先告诉听众是谁在说）
            string intro = displayName == null || displayName.Length == 0 ? "" : displayName + "说";
            if (intro.Length > 0)
            {
                SpeakOne(py, audio, intro, ResolveGlobalVoice(), catKey);
            }
            // [段2] 正文——该猫外观音色
            SpeakOne(py, audio, text, voice, catKey);
        }
        /// <summary>
        /// 单段合成并入队——失败记 Log 即返回（不影响后续段）；播放键固定 RoundSummaryAudioKey（跨猫全串行）。
        /// </summary>
        /// <param name="py">py 服务</param>
        /// <param name="audio">音频服务</param>
        /// <param name="text">待合成文本</param>
        /// <param name="voice">音色（空=服务默认）</param>
        /// <param name="catKey">猫 key（日志定位用）</param>
        private static void SpeakOne(IPyService py, IAudioService audio, string text, string voice, string catKey)
        {
            if (text == null || text.Length == 0)
            {
                return;
            }
            string outPath = BuildSummaryAudioPath();
            if (outPath.Length == 0)
            {
                LogStore.Add("CatHome4", 2, "轮末总结播报跳过——产物目录未配置（py.service.kokoro.out_dir）", "CHAT");
                return;
            }
            Dictionary<string, object> payload = new Dictionary<string, object>();
            payload["id"] = "summary";
            payload["op"] = "speak";
            payload["text"] = NormalizeForSpeech(text);
            if (voice != null && voice.Length > 0)
            {
                payload["voice"] = voice;
            }
            payload["speed"] = 1.0;
            payload["out"] = outPath;
            string invokeArgs = JsonUtil.Object(("name", "kokoro"), ("payload", JsonUtil.Serialize(payload)));
            string reply = py.Invoke(invokeArgs);
            if (reply == null || reply.Length == 0 || reply.StartsWith("ERR|", StringComparison.Ordinal))
            {
                LogStore.Add("CatHome4", 2, "轮末总结合成失败: " + (reply == null ? "(无回执)" : reply) + " | cat=" + catKey, "CHAT");
                return;
            }
            string produced = outPath;
            JsonDocument doc = JsonUtil.Parse(reply, "summary-speak");
            if (doc == null)
            {
                LogStore.Add("CatHome4", 2, "轮末总结合成回执不可解析: " + reply, "CHAT");
                return;
            }
            try
            {
                JsonElement root = doc.RootElement;
                if (!JsonUtil.GetBool(root, "ok", false))
                {
                    LogStore.Add("CatHome4", 2, "轮末总结合成失败: " + reply, "CHAT");
                    return;
                }
                produced = JsonUtil.GetStr(root, "out", outPath);
            }
            finally
            {
                doc.Dispose();
            }
            string enqueue = audio.Enqueue(RoundSummaryAudioKey, produced, -1f);
            if (enqueue != null && enqueue.Length > 0)
            {
                LogStore.Add("CatHome4", 2, "轮末总结播放入队失败: " + enqueue, "CHAT");
            }
        }
        /// <summary>
        /// 全局默认音色——audio.voice 落盘值优先，未落盘回落 schema 默认；空=服务默认（zf_001）。
        /// </summary>
        /// <returns>音色名</returns>
        private static string ResolveGlobalVoice()
        {
            ConfigStore cfg = null;
            if (!DataBox.TryResolve<ConfigStore>(out cfg) || cfg == null)
            {
                return "";
            }
            string voice = cfg.Get("audio.voice", "");
            if (voice != null && voice.Length > 0)
            {
                return voice;
            }
            ConfigSchema schema = null;
            if (DataBox.TryResolve<ConfigSchema>(out schema) && schema != null)
            {
                ConfigSchema.Item? item = schema.Find("audio.voice");
                if (item != null)
                {
                    return item.Default;
                }
            }
            return "";
        }
        /// <summary>
        /// 播报名解析——固定 key 走中文转义（majordomo 的 displayName 不可改，espeak 读英文不自然）；
        /// 其余猫用 displayName（可改名调整读音），空则回落 catKey。
        /// </summary>
        /// <param name="catKey">猫 key</param>
        /// <returns>播报名（不含「说」）</returns>
        private string ResolveSpeakName(string catKey)
        {
            if (string.Equals(catKey, "majordomo", StringComparison.Ordinal))
            {
                return "大管家";
            }
            return DisplayName.Length > 0 ? DisplayName : catKey;
        }
        /// <summary>
        /// 播报文本归一——点分版本号逐位转汉字（cn2an 会把 1.8.152 的尾段按数值读成「一百五十二」）。
        /// 只处理点分数字（版本号形态）；数量类数字保持原样由 G2P 按量词读。
        /// </summary>
        /// <param name="text">原始文本</param>
        /// <returns>播报用文本（无版本号则原样返回）</returns>
        private static string NormalizeForSpeech(string text)
        {
            if (text == null || text.Length == 0)
            {
                return text;
            }
            // 同音替换——只换词表内的明确错读（如「重来 → 虫来」），词表外原样
            string replaced = text;
            for (int r = 0; r < SpeechReplacements.Length; r = r + 1)
            {
                replaced = replaced.Replace(SpeechReplacements[r][0], SpeechReplacements[r][1]);
            }
            return System.Text.RegularExpressions.Regex.Replace(replaced, @"\d+(?:\.\d+)+", delegate (System.Text.RegularExpressions.Match match)
            {
                string value = match.Value;
                StringBuilder sb = new StringBuilder(value.Length * 2);
                for (int i = 0; i < value.Length; i = i + 1)
                {
                    char c = value[i];
                    if (c == '.')
                    {
                        sb.Append('点');
                    }
                    else if (c >= '0' && c <= '9')
                    {
                        sb.Append("零一二三四五六七八九"[c - '0']);
                    }
                    else
                    {
                        sb.Append(c);
                    }
                }
                return sb.ToString();
            });
        }
        /// <summary>
        /// 播报同音替换表——只收明确读错的词（Kokoro 的 ZHG2P 按分词选音，独词形态下「重」读成 zhòng）。
        /// 词表外一律不动；新增词目须先实测确认读错（判例：「重来/重做/重跑」读 zhòng，「重启/重新/重量/重要」正确）。
        /// </summary>
        private static readonly string[][] SpeechReplacements = new string[][]
        {
            new string[] { "重来", "虫来" },
            new string[] { "重做", "虫做" },
            new string[] { "重跑", "虫跑" }
        };
        /// <summary>
        /// 轮末总结提示音路径——部署包 assets/audio/roundsummary_chime.wav；缺失记 WARN 并跳过（不阻断播报）。
        /// </summary>
        /// <returns>提示音绝对路径；缺失空串</returns>
        private static string ResolveChimePath()
        {
            string path = System.IO.Path.Combine(AppContext.BaseDirectory, "assets", "audio", "roundsummary_chime.wav");
            if (System.IO.File.Exists(path))
            {
                return path;
            }
            LogStore.Add("CatHome4", 2, "轮末总结提示音缺失（assets/audio/roundsummary_chime.wav）——跳过提示音", "CHAT");
            return "";
        }
    }
}
