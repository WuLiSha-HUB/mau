'use strict';

// biliwatch —— B 站直播状态监控 → CH4 通道播报
// 服务端 TS（Node 原生类型擦除直跑 · 无构建步骤）；前端纯 JS 静态页。
// 数据面：轮询 api.live.bilibili.com 公开房间信息（带 UA + Referer）。
// 出口面：场次跃迁时 POST CH4 /api/v1/channels/publish（通道 Key · 广播语义）。
// 状态面：自带配置页与状态页（HTTP + web/index.html），端口 7901（外围工具下行段）。
// 落盘：data/config.json（配置）/ data/state.json（场次基线）/ data/events.jsonl（事件流水）。

import fsp from 'node:fs/promises';
import http from 'node:http';
import https from 'node:https';
import path from 'node:path';
import os from 'node:os';

// [段1] 类型定义——配置 / 运行态 / 外部回执

/** 单个房间配置。 */
interface RoomConfig {
    /** B 站房间号（数字串）。 */
    roomId: string;
    /** 备注——正文 {name} 的首选来源。 */
    note: string;
    /** 是否纳入轮询。 */
    enabled: boolean;
}

/** 轮询参数。 */
interface PollConfig {
    intervalSec: number;
    timeoutMs: number;
    backoffMaxSec: number;
}

/** 出口参数。 */
interface PublishConfig {
    enabled: boolean;
    endpoint: string;
    key: string;
}

/** 通知开关。 */
interface NotifyConfig {
    onLive: boolean;
    onOffline: boolean;
}

/** 消息模板（占位符 {name} {roomId} {title} {uid} {url} {duration}）。 */
interface TemplateConfig {
    live: string;
    offline: string;
}

/** 服务配置——落盘形态与内存形态同构。 */
interface BiliwatchConfig {
    rooms: RoomConfig[];
    poll: PollConfig;
    publish: PublishConfig;
    notify: NotifyConfig;
    templates: TemplateConfig;
}

/** 事件类型——字面量联合，写错即编译期报错。 */
type EventType =
    | 'service'
    | 'config'
    | 'baseline'
    | 'live'
    | 'offline'
    | 'live-skip'
    | 'offline-skip'
    | 'check-fail'
    | 'send-skip'
    | 'send-fail'
    | 'sent'
    | 'sent-partial';

/** 场次跃迁方向。 */
type TransitionKind = 'live' | 'offline';

/** 事件流水条目。 */
interface BiliEvent {
    ts: string;
    type: EventType;
    roomId: string;
    text: string;
}

/** 单房间运行态。 */
interface RoomState {
    roomId: string;
    /** 场次键（live_time 日期串）；undefined = 尚未建立基线，'' = 非开播。 */
    sessionKey: string | undefined;
    /** 本场次起点（毫秒时间戳）。 */
    liveSince: number;
    title: string;
    uid: string;
    liveStatus: number | null;
    liveTimeText: string;
    lastCheckAt: number;
    lastOkAt: number;
    failCount: number;
    error: string | null;
    nextPollAt: number;
    inFlight: boolean;
}

/** 进程运行态。 */
interface Runtime {
    config: BiliwatchConfig | null;
    rooms: Record<string, RoomState>;
    events: BiliEvent[];
    errors: string[];
    timer: ReturnType<typeof setTimeout> | null;
}

/** B 站房间信息接口返回体（只声明用到的字段——其余字段不取不用）。 */
interface BiliRoomInfo {
    live_status?: number;
    live_time?: string;
    title?: string;
    uid?: number;
}

/** B 站接口响应壳。 */
interface BiliResponse {
    code: number;
    message?: string;
    data?: BiliRoomInfo;
}

/** HTTP 结果（取数 / 播报共用）。 */
interface HttpResult {
    status: number;
    body: string;
}

/** CH4 播报回执体（只声明消费到的字段）。 */
interface PublishResult {
    ok?: boolean;
    delivered?: number;
    failed?: number;
    error?: string;
}

/** 配置清洗结果。 */
interface NormalizeResult {
    config: BiliwatchConfig;
    warnings: string[];
}

/** 状态页单房间摘要。 */
interface RoomSummary {
    roomId: string;
    note: string;
    enabled: boolean;
    liveStatus: number | null;
    liveTimeText: string;
    live: boolean;
    title: string;
    lastCheckAt: number;
    lastOkAt: number;
    failCount: number;
    error: string | null;
    nextPollAt: number;
}

/** 状态页整块载荷。 */
interface StatusPayload {
    ok: boolean;
    tool: string;
    version: string;
    now: string;
    host: string;
    uptimeSec: number;
    config: {
        rooms: RoomConfig[];
        poll: PollConfig;
        publish: { enabled: boolean; endpoint: string; keySet: boolean; keyMasked: string };
        notify: NotifyConfig;
        templates: TemplateConfig;
    };
    rooms: RoomSummary[];
    events: BiliEvent[];
    errors: string[];
}

// [段2] 常量与默认值

const VERSION = '1.0.0';
const PORT: number = Number(process.env.BW_PORT || 7901);
const BIND: string = process.env.BW_BIND || '127.0.0.1';
const WEB_ROOT: string = path.join(import.meta.dirname, 'web');
const DATA_DIR: string = path.join(import.meta.dirname, 'data');
const CONFIG_FILE: string = path.join(DATA_DIR, 'config.json');
const STATE_FILE: string = path.join(DATA_DIR, 'state.json');
const EVENTS_FILE: string = path.join(DATA_DIR, 'events.jsonl');

const LIVE_HOST = 'api.live.bilibili.com';
const LIVE_UA = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) biliwatch/' + VERSION;
const LIVE_REFERER = 'https://live.bilibili.com/';
const ROOM_URL = 'https://live.bilibili.com/';
const MAX_EVENTS = 100;
const MAX_ERRORS = 20;
const SCHED_TICK_MS = 1000;
const SEND_TIMEOUT_MS = 15000;
const BODY_LIMIT_BYTES = 262144;

/** B 站未开播时 live_time 的占位值。 */
const NO_LIVE_TIME = '0000-00-00 00:00:00';

/** 默认配置——用户值深合并覆盖；rooms 为整组替换。 */
const DEFAULT_CONFIG: BiliwatchConfig = {
    rooms: [],
    poll: { intervalSec: 30, timeoutMs: 10000, backoffMaxSec: 300 },
    publish: { enabled: true, endpoint: 'http://127.0.0.1:8080/api/v1/channels/publish', key: '' },
    notify: { onLive: true, onOffline: true },
    templates: {
        live: '# 🔴 {name} 开播了\n\n> 📺 {title}\n\n[▶ 点击进入直播间]({url})',
        offline: '# ⚫ {name} 下播了\n\n⏱ 时长：**{duration}**\n\n[▶ 点击进入直播间]({url})'
    }
};

const startedAt: number = Date.now();

/** 进程运行态。 */
const runtime: Runtime = {
    config: null,
    rooms: {},
    events: [],
    errors: [],
    timer: null
};

// [段3] 基础工具——读写 / 合并 / 渲染 / 格式 / 记账

/**
 * 未知异常 → 文本（strict 下 catch 变量是 unknown）。
 */
function errText(err: unknown): string {
    if (err instanceof Error) {
        return err.message;
    }
    return String(err);
}

/**
 * 值 → 字符串键记录——仅用于「字段遍历」这类结构性读取（形状收敛，非类型转换）。
 */
function asRecord(v: unknown): Record<string, unknown> {
    if (v !== null && typeof v === 'object') {
        return v as Record<string, unknown>;
    }
    return {};
}

/**
 * 读 JSON 文件；不存在返回 null（静默），损坏记 errors 后返回 null。
 */
async function readJson(file: string, errors: string[]): Promise<unknown> {
    let text = '';
    try {
        text = await fsp.readFile(file, 'utf8');
    } catch {
        return null;
    }
    try {
        return JSON.parse(text) as unknown;
    } catch (err) {
        errors.push(path.basename(file) + ' 解析失败：' + errText(err));
        return null;
    }
}

/**
 * 原子写 JSON：同目录临时文件 + rename。
 */
async function writeJsonAtomic(file: string, obj: unknown): Promise<void> {
    const tmp = file + '.tmp';
    await fsp.writeFile(tmp, JSON.stringify(obj, null, 2), 'utf8');
    await fsp.rename(tmp, file);
}

/**
 * 深拷贝 JSON 可序列化值（数组 / 对象 / 原语）。
 * 用途：合并时「用户未提供」的段必须拷贝，否则会与默认值共享引用（后续清洗会污染默认值）。
 */
function cloneValue(v: unknown): unknown {
    if (Array.isArray(v)) {
        const arr: unknown[] = [];
        for (let i = 0; i < v.length; i = i + 1) {
            arr.push(cloneValue(v[i]));
        }
        return arr;
    }
    if (v !== null && typeof v === 'object') {
        const src = asRecord(v);
        const copy: Record<string, unknown> = {};
        const keys = Object.keys(src);
        for (let i = 0; i < keys.length; i = i + 1) {
            copy[keys[i]] = cloneValue(src[keys[i]]);
        }
        return copy;
    }
    return v;
}

/**
 * 合并器出口——记录形态 → 配置形态。
 * 断言成立的理由：键集恒取自 base（BiliwatchConfig）；值形态由递归合并收口，
 * 值域由 normalizeIncoming 逐字段清洗——此处只做「形状认定」，不做类型转换。
 */
function asConfig(rec: Record<string, unknown>): BiliwatchConfig {
    return rec as unknown as BiliwatchConfig;
}

/**
 * 深合并：base 打底、user 覆盖；嵌套对象递归，数组与原始值深拷贝。
 * 键集恒取自 base——合并是「形状收敛」，值域仍由 normalizeIncoming 逐字段清洗。
 */
function mergeConfig(base: BiliwatchConfig, user: unknown): BiliwatchConfig {
    const baseRec = asRecord(base);
    const src = asRecord(user);
    const out: Record<string, unknown> = {};
    const keys = Object.keys(baseRec);
    for (let i = 0; i < keys.length; i = i + 1) {
        const k = keys[i];
        const b = baseRec[k];
        const u = src[k];
        const bIsObj = b !== null && typeof b === 'object' && !Array.isArray(b);
        const uIsObj = u !== null && typeof u === 'object' && !Array.isArray(u);
        if (bIsObj && uIsObj) {
            out[k] = mergeConfig(b as unknown as BiliwatchConfig, u);
        } else if (u === undefined) {
            out[k] = cloneValue(b);
        } else {
            out[k] = cloneValue(u);
        }
    }
    return asConfig(out);
}

/**
 * 模板渲染——把 {key} 替成上下文值；未提供（undefined / 空串）的键原样保留。
 */
function renderTemplate(tpl: string, ctx: Record<string, string | undefined>): string {
    return String(tpl).replace(/\{(\w+)\}/g, function (m: string, k: string): string {
        const v = ctx[k];
        if (v === undefined || v === '') {
            return m;
        }
        return String(v);
    });
}

/**
 * Markdown 控制字符剥除——B 站标题里的 [* _ ` # [ ] ~] 会把后续正文吃成斜体 / 链接 / 代码，
 * 渲染出来是残句；发送前统一剥掉（只作用于人写字段，URL 不过本函数）。
 */
function stripMd(s: string): string {
    return String(s).replace(/[*_`#\[\]~]/g, '');
}

/**
 * B 站时间串（"YYYY-MM-DD HH:MM:SS" · UTC+8）→ 毫秒时间戳；无效返回 0。
 * 手工按 +8 解析——不依赖本机时区。
 */
function parseBiliTime(s: string): number {
    const m = /^(\d{4})-(\d{2})-(\d{2}) (\d{2}):(\d{2}):(\d{2})$/.exec(String(s || ''));
    if (!m) {
        return 0;
    }
    return Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3]), Number(m[4]) - 8, Number(m[5]), Number(m[6]));
}

/**
 * 秒数 → 中文时长。
 */
function formatDuration(sec: number): string {
    if (!sec || sec < 0 || !isFinite(sec)) {
        return '未知';
    }
    const h = Math.floor(sec / 3600);
    const m = Math.floor((sec % 3600) / 60);
    const s = Math.floor(sec % 60);
    if (h > 0) {
        return h + ' 小时 ' + m + ' 分';
    }
    if (m > 0) {
        return m + ' 分 ' + s + ' 秒';
    }
    return s + ' 秒';
}

/**
 * 数值夹紧——非法值回落 fallback。
 */
function clampNumber(v: unknown, min: number, max: number, fallback: number): number {
    const n = Number(v);
    if (!isFinite(n)) {
        return fallback;
    }
    if (n < min) {
        return min;
    }
    if (n > max) {
        return max;
    }
    return n;
}

/**
 * 文本首行截断——事件摘要用。
 */
function firstLine(text: string): string {
    const line = String(text).split('\n')[0];
    return line.length > 80 ? line.slice(0, 80) + '…' : line;
}

/**
 * Key 掩码——只用于回显，绝不回明文。
 */
function maskKey(key: string): string {
    if (!key) {
        return '';
    }
    if (key.length <= 8) {
        return '********';
    }
    return key.slice(0, 4) + '********' + key.slice(key.length - 4);
}

/**
 * 错误入台账（最近 MAX_ERRORS 条）。
 */
function pushError(msg: string): void {
    runtime.errors.push(String(msg));
    if (runtime.errors.length > MAX_ERRORS) {
        runtime.errors.splice(0, runtime.errors.length - MAX_ERRORS);
    }
    console.error('[error] ' + msg);
}

/**
 * 事件入内存（最近 MAX_EVENTS 条）并追加落盘（写失败出声，不阻断主链路）。
 */
function pushEvent(type: EventType, roomId: string, text: string): void {
    const ev: BiliEvent = { ts: new Date().toISOString(), type: type, roomId: roomId || '', text: text };
    runtime.events.push(ev);
    if (runtime.events.length > MAX_EVENTS) {
        runtime.events.splice(0, runtime.events.length - MAX_EVENTS);
    }
    console.log('[' + ev.ts + '] ' + type + (roomId ? ' room=' + roomId : '') + ' | ' + text);
    fsp.appendFile(EVENTS_FILE, JSON.stringify(ev) + '\n', 'utf8').catch(function (err: unknown): void {
        pushError('events.jsonl 写入失败：' + errText(err));
    });
}

// [段4] 数据面——B 站房间信息

/**
 * 拉房间信息（公开接口）——返回 data 对象；code 非 0 与网络异常一律抛错。
 */
function fetchRoomInfo(roomId: string, timeoutMs: number): Promise<BiliRoomInfo> {
    return new Promise(function (resolve, reject) {
        const req = https.get({
            host: LIVE_HOST,
            path: '/room/v1/Room/get_info?room_id=' + encodeURIComponent(roomId),
            headers: { 'User-Agent': LIVE_UA, 'Referer': LIVE_REFERER },
            timeout: timeoutMs
        }, function (res: http.IncomingMessage) {
            let buf = '';
            res.setEncoding('utf8');
            res.on('data', function (chunk: string) { buf = buf + chunk; });
            res.on('end', function () {
                let payload: BiliResponse | null = null;
                try {
                    payload = JSON.parse(buf) as BiliResponse;
                } catch {
                    reject(new Error('响应非 JSON（HTTP ' + String(res.statusCode) + '）'));
                    return;
                }
                if (payload === null || payload.code !== 0) {
                    const codeText = payload === null ? '空响应' : String(payload.code);
                    const msgText = payload && payload.message ? '：' + payload.message : '';
                    reject(new Error('接口码 ' + codeText + msgText));
                    return;
                }
                resolve(payload.data === undefined ? {} : payload.data);
            });
        });
        req.on('timeout', function () { req.destroy(new Error('请求超时 ' + timeoutMs + 'ms')); });
        req.on('error', function (err: Error) { reject(err); });
    });
}

// [段5] 出口面——CH4 通道播报

/**
 * POST 通道播报端点——返回 { status, body }。
 */
function postPublish(cfg: BiliwatchConfig, text: string): Promise<HttpResult> {
    return new Promise(function (resolve, reject) {
        const body = JSON.stringify({ text: text });
        let u: URL;
        try {
            u = new URL(cfg.publish.endpoint);
        } catch {
            reject(new Error('端点不是合法 URL：' + cfg.publish.endpoint));
            return;
        }
        const lib = u.protocol === 'https:' ? https : http;
        const req = lib.request({
            protocol: u.protocol,
            hostname: u.hostname,
            port: u.port || (u.protocol === 'https:' ? 443 : 80),
            path: u.pathname + u.search,
            method: 'POST',
            headers: {
                'Content-Type': 'application/json; charset=utf-8',
                'Content-Length': String(Buffer.byteLength(body)),
                'Authorization': 'Bearer ' + cfg.publish.key
            },
            timeout: SEND_TIMEOUT_MS
        }, function (res: http.IncomingMessage) {
            let buf = '';
            res.setEncoding('utf8');
            res.on('data', function (chunk: string) { buf = buf + chunk; });
            res.on('end', function () {
                resolve({ status: res.statusCode === undefined ? 0 : res.statusCode, body: buf });
            });
        });
        req.on('timeout', function () { req.destroy(new Error('发送超时 ' + SEND_TIMEOUT_MS + 'ms')); });
        req.on('error', function (err: Error) { reject(err); });
        req.write(body);
        req.end();
    });
}

/**
 * 发送一条正文——成功 / 部分成功 / 失败都落事件（不静默）。
 */
async function sendText(cfg: BiliwatchConfig, text: string, roomId: string): Promise<void> {
    if (cfg.publish.enabled === false) {
        pushEvent('send-skip', roomId, '出口已禁用，未发送');
        return;
    }
    if (!cfg.publish.key) {
        pushEvent('send-fail', roomId, '未配置通道 Key，未发送');
        return;
    }
    const res = await postPublish(cfg, text).catch(function (err: unknown): null {
        pushEvent('send-fail', roomId, '发送异常：' + errText(err));
        return null;
    });
    if (res === null) {
        return;
    }
    let payload: PublishResult | null = null;
    try {
        payload = JSON.parse(res.body) as PublishResult;
    } catch {
        payload = null;
    }
    if (res.status === 200) {
        const n = payload && payload.delivered !== undefined ? payload.delivered : '?';
        pushEvent('sent', roomId, '已发送（delivered=' + n + '）');
        return;
    }
    if (res.status === 207) {
        const d = payload ? payload.delivered : '?';
        const f = payload ? payload.failed : '?';
        pushEvent('sent-partial', roomId, '部分送达（delivered=' + d + ' / failed=' + f + '）——逐群明细见 CH4 留档');
        return;
    }
    const detail = payload && payload.error ? payload.error : firstLine(res.body ? res.body : '');
    pushEvent('send-fail', roomId, 'HTTP ' + res.status + '：' + detail);
}

// [段6] 状态机——场次跃迁判定

/**
 * 取房间运行态（不存在则新建）。
 */
function ensureRoom(roomId: string): RoomState {
    const exist = runtime.rooms[roomId];
    if (exist) {
        return exist;
    }
    const st: RoomState = {
        roomId: roomId,
        sessionKey: undefined,
        liveSince: 0,
        title: '',
        uid: '',
        liveStatus: null,
        liveTimeText: '',
        lastCheckAt: 0,
        lastOkAt: 0,
        failCount: 0,
        error: null,
        nextPollAt: 0,
        inFlight: false
    };
    runtime.rooms[roomId] = st;
    return st;
}

/**
 * 载入场次基线——跨重启保留，重启不重复报也不漏报。
 * 只有「state 里完全没有这个房间」才是 undefined（未建基线）；存过 '' 的房间按「非开播基线」还原。
 */
async function loadState(): Promise<void> {
    const state = await readJson(STATE_FILE, runtime.errors);
    const rooms = asRecord(asRecord(state)['rooms']);
    const keys = Object.keys(rooms);
    for (let i = 0; i < keys.length; i = i + 1) {
        const k = keys[i];
        const saved = asRecord(rooms[k]);
        const st = ensureRoom(k);
        const savedKey = saved['sessionKey'];
        st.sessionKey = typeof savedKey === 'string' ? savedKey : undefined;
        st.liveSince = Number(saved['liveSince']) || 0;
    }
}

/**
 * 落盘场次基线（失败出声，不阻断）。
 */
async function saveState(): Promise<void> {
    const rooms: Record<string, { sessionKey: string; liveSince: number }> = {};
    const keys = Object.keys(runtime.rooms);
    for (let i = 0; i < keys.length; i = i + 1) {
        const st = runtime.rooms[keys[i]];
        rooms[keys[i]] = {
            sessionKey: st.sessionKey === undefined ? '' : st.sessionKey,
            liveSince: st.liveSince || 0
        };
    }
    try {
        await writeJsonAtomic(STATE_FILE, { version: VERSION, savedAt: new Date().toISOString(), rooms: rooms });
    } catch (err) {
        pushError('state.json 写入失败：' + errText(err));
    }
}

/**
 * 轮询单个房间——取数失败退避并出声；成功则判场次跃迁。
 * 🔴 场次键 = live_time（开播日期串）——同场次内不变，天然去重；
 *    live_status 非 1（未开播 / 轮播）一律视为非开播，离开即场次结束。
 */
async function pollRoom(roomCfg: RoomConfig): Promise<void> {
    const cfg = runtime.config;
    if (cfg === null) {
        return;
    }
    const roomId = String(roomCfg.roomId);
    const st = ensureRoom(roomId);
    if (st.inFlight) {
        return;
    }
    st.inFlight = true;
    try {
        st.lastCheckAt = Date.now();
        const info = await fetchRoomInfo(roomId, cfg.poll.timeoutMs).catch(function (err: unknown): null {
            st.failCount = st.failCount + 1;
            const reason = errText(err);
            st.error = reason;
            const backoff = Math.min(cfg.poll.backoffMaxSec, cfg.poll.intervalSec * Math.pow(2, st.failCount - 1));
            st.nextPollAt = Date.now() + backoff * 1000;
            pushEvent('check-fail', roomId, '取数失败（第 ' + st.failCount + ' 次）：' + reason + ' · ' + backoff + ' 秒后重试');
            return null;
        });
        if (info === null) {
            return;
        }
        st.failCount = 0;
        st.error = null;
        st.lastOkAt = Date.now();
        st.nextPollAt = Date.now() + cfg.poll.intervalSec * 1000;
        st.liveStatus = Number(info.live_status);
        // live_time 是日期串（UTC+8）；未开播时为 "0000-00-00 00:00:00"。
        st.liveTimeText = info.live_time ? String(info.live_time) : '';
        st.title = info.title ? String(info.title) : '';
        st.uid = info.uid ? String(info.uid) : '';

        const timeValid = !!st.liveTimeText && st.liveTimeText !== NO_LIVE_TIME;
        const sessionKey = st.liveStatus === 1 && timeValid ? st.liveTimeText : '';
        const prev = st.sessionKey;
        st.sessionKey = sessionKey;

        if (prev === undefined) {
            pushEvent('baseline', roomId, '建立基线：' + (sessionKey ? '直播中（场次 ' + sessionKey + '）' : '未开播'));
            await saveState();
            return;
        }
        if (sessionKey === prev) {
            return;
        }
        if (sessionKey) {
            st.liveSince = parseBiliTime(sessionKey);
            await notifyTransition(roomCfg, st, 'live', 0);
        } else {
            const since = st.liveSince || parseBiliTime(prev);
            await notifyTransition(roomCfg, st, 'offline', since);
            st.liveSince = 0;
        }
        await saveState();
    } finally {
        st.inFlight = false;
    }
}

/**
 * 跃迁处理——按模板组装正文并发送。
 */
async function notifyTransition(roomCfg: RoomConfig, st: RoomState, kind: TransitionKind, sinceMs: number): Promise<void> {
    const cfg = runtime.config;
    if (cfg === null) {
        return;
    }
    const ctx: Record<string, string | undefined> = {
        name: stripMd(roomCfg.note || st.title || ('房间 ' + st.roomId)),
        roomId: st.roomId,
        title: stripMd(st.title),
        uid: st.uid,
        url: ROOM_URL + st.roomId,
        duration: kind === 'offline' && sinceMs ? formatDuration(Math.round((Date.now() - sinceMs) / 1000)) : ''
    };
    const tpl = kind === 'live' ? cfg.templates.live : cfg.templates.offline;
    const text = renderTemplate(tpl, ctx);
    const allowed = kind === 'live' ? cfg.notify.onLive !== false : cfg.notify.onOffline !== false;
    if (!allowed) {
        pushEvent(kind === 'live' ? 'live-skip' : 'offline-skip', st.roomId, '已关闭该类通知，未发送：' + firstLine(text));
        return;
    }
    pushEvent(kind, st.roomId, '状态跃迁 → ' + firstLine(text));
    await sendText(cfg, text, st.roomId);
}

// [段7] 调度

/**
 * 每秒扫一次——到期房间取数（inFlight 保护重入）。
 */
function onTick(): void {
    try {
        const cfg = runtime.config;
        if (cfg !== null) {
            const now = Date.now();
            for (let i = 0; i < cfg.rooms.length; i = i + 1) {
                const roomCfg = cfg.rooms[i];
                if (!roomCfg || roomCfg.enabled === false) {
                    continue;
                }
                const st = ensureRoom(String(roomCfg.roomId));
                if (st.inFlight) {
                    continue;
                }
                if (st.nextPollAt && st.nextPollAt > now) {
                    continue;
                }
                pollRoom(roomCfg).catch(function (err: unknown): void {
                    pushError('轮询异常 room=' + roomCfg.roomId + '：' + errText(err));
                });
            }
        }
    } catch (err) {
        pushError('调度异常：' + errText(err));
    }
    runtime.timer = setTimeout(onTick, SCHED_TICK_MS);
}

/**
 * 立即检查全部启用房间（人工触发）。
 */
async function checkAllNow(): Promise<void> {
    const cfg = runtime.config;
    if (cfg === null) {
        return;
    }
    const tasks: Array<Promise<void>> = [];
    for (let i = 0; i < cfg.rooms.length; i = i + 1) {
        const roomCfg = cfg.rooms[i];
        if (roomCfg && roomCfg.enabled !== false) {
            tasks.push(pollRoom(roomCfg));
        }
    }
    await Promise.all(tasks);
}

// [段8] HTTP 面

/**
 * 来源白名单——localhost 与 tailnet（100.64.0.0/10）放行，其余 403。
 * （默认只绑 127.0.0.1；置 BW_BIND=0.0.0.0 时本闸是唯一门禁。）
 */
function isAllowedSource(remote: string | undefined): boolean {
    if (!remote) {
        return false;
    }
    let addr = remote;
    if (addr.indexOf('::ffff:') === 0) {
        addr = addr.slice(7);
    }
    if (addr === '::1' || addr === '127.0.0.1') {
        return true;
    }
    const parts = addr.split('.');
    if (parts.length !== 4) {
        return false;
    }
    if (Number(parts[0]) !== 100) {
        return false;
    }
    const b = Number(parts[1]);
    return b >= 64 && b <= 127;
}

/**
 * 读请求体（上限 BODY_LIMIT_BYTES，超限报错并断开）。
 */
function readBody(req: http.IncomingMessage): Promise<string> {
    return new Promise(function (resolve, reject) {
        let buf = '';
        let size = 0;
        req.setEncoding('utf8');
        req.on('data', function (chunk: string) {
            size = size + Buffer.byteLength(chunk);
            if (size > BODY_LIMIT_BYTES) {
                reject(new Error('body 过大（>' + BODY_LIMIT_BYTES + ' 字节）'));
                req.destroy();
                return;
            }
            buf = buf + chunk;
        });
        req.on('end', function () { resolve(buf); });
        req.on('error', function (err: Error) { reject(err); });
    });
}

/**
 * 输出 JSON。
 */
function sendJson(res: http.ServerResponse, status: number, obj: unknown): void {
    res.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store' });
    res.end(JSON.stringify(obj));
}

/**
 * 配置清洗——非法项跳过并出声，不静默丢弃；掩码 Key 回显与空串都视为「未修改」。
 */
function normalizeIncoming(user: unknown, current: BiliwatchConfig | null): NormalizeResult {
    const warnings: string[] = [];
    const cfg = mergeConfig(DEFAULT_CONFIG, current);
    if (user === null || typeof user !== 'object') {
        warnings.push('提交内容不是对象，已忽略');
        return { config: cfg, warnings: warnings };
    }
    const src = asRecord(user);

    const rawRooms = src['rooms'];
    if (Array.isArray(rawRooms)) {
        const seen: Record<string, boolean> = {};
        const rooms: RoomConfig[] = [];
        for (let i = 0; i < rawRooms.length; i = i + 1) {
            const item = asRecord(rawRooms[i]);
            const rawId = item['roomId'];
            const rid = (rawId === undefined || rawId === null ? '' : String(rawId)).trim();
            if (!/^\d{1,12}$/.test(rid)) {
                warnings.push('房间号非法，已跳过：' + (rid || '(空)'));
                continue;
            }
            if (seen[rid]) {
                warnings.push('房间号重复，已合并：' + rid);
                continue;
            }
            seen[rid] = true;
            const noteRaw = item['note'];
            rooms.push({
                roomId: rid,
                note: noteRaw === undefined || noteRaw === null ? '' : String(noteRaw).slice(0, 40),
                enabled: item['enabled'] !== false
            });
        }
        cfg.rooms = rooms;
    } else if (rawRooms !== undefined) {
        warnings.push('rooms 不是数组，已忽略');
    }

    const pollRaw = src['poll'];
    if (pollRaw !== null && typeof pollRaw === 'object') {
        const poll = asRecord(pollRaw);
        cfg.poll.intervalSec = clampNumber(poll['intervalSec'], 5, 3600, cfg.poll.intervalSec);
        cfg.poll.timeoutMs = clampNumber(poll['timeoutMs'], 1000, 30000, cfg.poll.timeoutMs);
        cfg.poll.backoffMaxSec = clampNumber(poll['backoffMaxSec'], 30, 3600, cfg.poll.backoffMaxSec);
    }

    const pubRaw = src['publish'];
    if (pubRaw !== null && typeof pubRaw === 'object') {
        const pub = asRecord(pubRaw);
        cfg.publish.enabled = pub['enabled'] !== false;
        const epRaw = pub['endpoint'];
        if (typeof epRaw === 'string' && epRaw.trim()) {
            const ep = epRaw.trim();
            if (/^https?:\/\//.test(ep)) {
                cfg.publish.endpoint = ep;
            } else {
                warnings.push('端点须以 http(s):// 开头，已保留原值');
            }
        }
        const keyRaw = pub['key'];
        if (typeof keyRaw === 'string') {
            const k = keyRaw.trim();
            // 空串与掩码回显都视为「未修改」——避免保存其他字段时误清 Key。
            if (k !== '' && k.indexOf('*') < 0) {
                cfg.publish.key = k;
            }
        }
    }

    const notifyRaw = src['notify'];
    if (notifyRaw !== null && typeof notifyRaw === 'object') {
        const notify = asRecord(notifyRaw);
        cfg.notify.onLive = notify['onLive'] !== false;
        cfg.notify.onOffline = notify['onOffline'] !== false;
    }

    const tplRaw = src['templates'];
    if (tplRaw !== null && typeof tplRaw === 'object') {
        const tpl = asRecord(tplRaw);
        const liveTpl = tpl['live'];
        const offTpl = tpl['offline'];
        if (typeof liveTpl === 'string' && liveTpl.trim()) {
            cfg.templates.live = liveTpl.slice(0, 500);
        }
        if (typeof offTpl === 'string' && offTpl.trim()) {
            cfg.templates.offline = offTpl.slice(0, 500);
        }
    }
    return { config: cfg, warnings: warnings };
}

/**
 * 状态载荷——配置（Key 只回掩码）+ 房间运行态 + 事件 + 错误。
 */
function statusPayload(): StatusPayload {
    const cfg = runtime.config === null ? mergeConfig(DEFAULT_CONFIG, null) : runtime.config;
    const rooms: RoomSummary[] = [];
    for (let i = 0; i < cfg.rooms.length; i = i + 1) {
        const roomCfg = cfg.rooms[i];
        const st = runtime.rooms[String(roomCfg.roomId)];
        rooms.push({
            roomId: String(roomCfg.roomId),
            note: roomCfg.note || '',
            enabled: roomCfg.enabled !== false,
            liveStatus: st ? st.liveStatus : null,
            liveTimeText: st ? st.liveTimeText : '',
            live: !!st && st.sessionKey !== undefined && st.sessionKey !== '',
            title: st ? st.title : '',
            lastCheckAt: st ? st.lastCheckAt : 0,
            lastOkAt: st ? st.lastOkAt : 0,
            failCount: st ? st.failCount : 0,
            error: st ? st.error : null,
            nextPollAt: st ? st.nextPollAt : 0
        });
    }
    return {
        ok: true,
        tool: 'biliwatch',
        version: VERSION,
        now: new Date().toISOString(),
        host: os.hostname(),
        uptimeSec: Math.round((Date.now() - startedAt) / 1000),
        config: {
            rooms: cfg.rooms,
            poll: cfg.poll,
            publish: {
                enabled: cfg.publish.enabled !== false,
                endpoint: cfg.publish.endpoint,
                keySet: !!cfg.publish.key,
                keyMasked: maskKey(cfg.publish.key)
            },
            notify: cfg.notify,
            templates: cfg.templates
        },
        rooms: rooms,
        events: runtime.events.slice(-40).reverse(),
        errors: runtime.errors.slice(-MAX_ERRORS)
    };
}

/**
 * 为新配置里的房间补运行态。
 */
function ensureRooms(): void {
    const cfg = runtime.config;
    if (cfg === null) {
        return;
    }
    for (let i = 0; i < cfg.rooms.length; i = i + 1) {
        ensureRoom(String(cfg.rooms[i].roomId));
    }
}

/**
 * 读前端静态文件（限定 web 目录，禁目录穿越）。
 */
async function serveStatic(res: http.ServerResponse, urlPath: string): Promise<void> {
    const rel = urlPath === '/' ? 'index.html' : urlPath.replace(/^\/+/, '');
    if (rel.indexOf('..') >= 0) {
        res.writeHead(403, { 'Content-Type': 'text/plain; charset=utf-8' });
        res.end('forbidden');
        return;
    }
    const file = path.join(WEB_ROOT, rel);
    const ext = path.extname(file).toLowerCase();
    const types: Record<string, string> = {
        '.html': 'text/html; charset=utf-8',
        '.js': 'text/javascript; charset=utf-8',
        '.css': 'text/css; charset=utf-8'
    };
    try {
        const buf = await fsp.readFile(file);
        res.writeHead(200, { 'Content-Type': types[ext] || 'application/octet-stream', 'Cache-Control': 'no-store' });
        res.end(buf);
    } catch {
        res.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' });
        res.end('not found');
    }
}

const server = http.createServer(async function (req: http.IncomingMessage, res: http.ServerResponse): Promise<void> {
    const remote = req.socket.remoteAddress;
    if (!isAllowedSource(remote)) {
        res.writeHead(403, { 'Content-Type': 'text/plain; charset=utf-8' });
        res.end('forbidden: ' + String(remote));
        return;
    }
    const url = (req.url || '/').split('?')[0];
    try {
        if (url === '/api/ping') {
            res.writeHead(200, { 'Content-Type': 'text/plain; charset=utf-8', 'Cache-Control': 'no-store' });
            res.end('pong');
            return;
        }
        if (url === '/api/status') {
            sendJson(res, 200, statusPayload());
            return;
        }
        if (url === '/api/config' && req.method === 'POST') {
            const raw = await readBody(req);
            let user: unknown = null;
            try {
                user = JSON.parse(raw) as unknown;
            } catch {
                sendJson(res, 400, { ok: false, error: 'body 非 JSON' });
                return;
            }
            const r = normalizeIncoming(user, runtime.config);
            try {
                await writeJsonAtomic(CONFIG_FILE, r.config);
            } catch (err) {
                sendJson(res, 500, { ok: false, error: '配置写入失败：' + errText(err) });
                return;
            }
            runtime.config = r.config;
            ensureRooms();
            const warnText = r.warnings.length ? ' · ' + r.warnings.length + ' 项告警' : '';
            pushEvent('config', '', '配置已更新（' + r.config.rooms.length + ' 房间' + warnText + '）');
            sendJson(res, 200, { ok: true, warnings: r.warnings, config: statusPayload().config });
            return;
        }
        if (url === '/api/check' && req.method === 'POST') {
            await checkAllNow();
            sendJson(res, 200, { ok: true, rooms: statusPayload().rooms });
            return;
        }
        if (url === '/api/test' && req.method === 'POST') {
            const raw = await readBody(req);
            let user: unknown = null;
            try {
                user = JSON.parse(raw || '{}') as unknown;
            } catch {
                user = null;
            }
            const body = asRecord(user);
            const textRaw = body['text'];
            const text = typeof textRaw === 'string' && textRaw ? textRaw.slice(0, 500) : 'biliwatch 测试消息——通道链路正常。';
            const cfg = runtime.config;
            if (cfg === null) {
                sendJson(res, 503, { ok: false, error: '服务尚未载入配置' });
                return;
            }
            await sendText(cfg, text, '(test)');
            sendJson(res, 200, { ok: true, text: text, events: runtime.events.slice(-5).reverse() });
            return;
        }
        await serveStatic(res, url);
    } catch (err) {
        pushError('请求处理异常 ' + url + '：' + errText(err));
        sendJson(res, 500, { ok: false, error: errText(err) });
    }
});

// [段9] 启动、退出与自检

/**
 * 启动——建数据目录、载配置与基线、起 HTTP、开调度。
 */
async function main(): Promise<void> {
    await fsp.mkdir(DATA_DIR, { recursive: true });
    const loaded = await readJson(CONFIG_FILE, runtime.errors);
    const cfg = mergeConfig(DEFAULT_CONFIG, loaded);
    runtime.config = cfg;
    await loadState();
    ensureRooms();
    server.on('error', function (err: Error) {
        console.error('HTTP 服务启动失败：' + err.message);
        process.exit(2);
    });
    server.listen(PORT, BIND, function () {
        console.log('biliwatch v' + VERSION + ' listening on ' + BIND + ':' + PORT);
        console.log('data: ' + DATA_DIR);
    });
    pushEvent('service', '', '服务启动 v' + VERSION + '（' + BIND + ':' + PORT + '，' + cfg.rooms.length + ' 房间）');
    runtime.timer = setTimeout(onTick, SCHED_TICK_MS);
}

/**
 * 优雅退出——清定时器、关 HTTP、兜底 3 秒强退。
 */
function shutdown(signal: string): void {
    console.log('biliwatch: ' + signal + ' received, closing');
    if (runtime.timer !== null) {
        clearTimeout(runtime.timer);
        runtime.timer = null;
    }
    server.close(function () {
        process.exit(0);
    });
    setTimeout(function () {
        process.exit(0);
    }, 3000);
}

process.on('SIGTERM', function () { shutdown('SIGTERM'); });
process.on('SIGINT', function () { shutdown('SIGINT'); });

/**
 * 自检用 GET——只打本机回环面。
 */
function httpGet(port: number, urlPath: string): Promise<HttpResult> {
    return new Promise(function (resolve, reject) {
        const req = http.get({ hostname: '127.0.0.1', port: port, path: urlPath, timeout: 5000 }, function (res: http.IncomingMessage) {
            let b = '';
            res.setEncoding('utf8');
            res.on('data', function (c: string) { b = b + c; });
            res.on('end', function () {
                resolve({ status: res.statusCode === undefined ? 0 : res.statusCode, body: b });
            });
        });
        req.on('timeout', function () { req.destroy(new Error('自检请求超时')); });
        req.on('error', function (err: Error) { reject(err); });
    });
}

/**
 * 自检模式（--selfcheck）——起服务 → 打自己的 HTTP 面 → 打印结论 → 退出。
 * 端口占用（已有实例在跑）即失败退出——这就是单实例语义。
 */
async function runSelfCheck(): Promise<void> {
    await main();
    try {
        const ping = await httpGet(PORT, '/api/ping');
        console.log('selfcheck /api/ping → HTTP ' + ping.status + ' · ' + ping.body);
        if (ping.status !== 200 || ping.body !== 'pong') {
            throw new Error('/api/ping 未按预期返回');
        }
        const st = await httpGet(PORT, '/api/status');
        const payload = JSON.parse(st.body) as StatusPayload;
        console.log('selfcheck /api/status → HTTP ' + st.status + ' · rooms=' + payload.rooms.length +
            ' · keySet=' + payload.config.publish.keySet + ' · events=' + payload.events.length);
        // 可选探针：--room <roomId> 打一次真实取数（只读——不写配置、不发消息）。
        const ri = process.argv.indexOf('--room');
        if (ri >= 0 && process.argv[ri + 1]) {
            const probeId = process.argv[ri + 1];
            const info = await fetchRoomInfo(probeId, 10000);
            console.log('probe room ' + probeId + ' → live_status=' + String(info.live_status) +
                ' · live_time=' + String(info.live_time) + ' · title=' + String(info.title || '') +
                ' · uid=' + String(info.uid || ''));
        }
        console.log('SELFCHECK_OK');
        process.exit(0);
    } catch (err) {
        console.error('SELFCHECK_FAIL：' + errText(err));
        process.exit(1);
    }
}

if (process.argv.indexOf('--selfcheck') >= 0) {
    await runSelfCheck();
} else {
    main().catch(function (err: unknown): void {
        console.error('启动失败：' + errText(err));
        process.exit(1);
    });
}
