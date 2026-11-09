// tests/unit/mock-env.js —— 测试环境公共件（P20-P3-17——setup.js 与 smoke.test.mjs 的原样重复样板去重）
// 定位：JSDOM 全局装配 + 浏览器 API mock（EventSource/fetch）；两个测试环境（chat.html / index.html）共用
import { JSDOM } from 'jsdom';

/**
 * 安装 DOM 全局——window 指向 globalThis（前端 var/function 声明经 vm.runInThisContext 挂 globalThis，window 别名可访问）
 * @param {string} html 页面骨架 HTML
 * @param {string} [url] JSDOM 基址（默认与生产同源 http://127.0.0.1:8080）
 * @returns {JSDOM} JSDOM 实例（调用方需要时可用）
 */
export function installDom(html, url) {
  const dom = new JSDOM(html, { url: url || 'http://127.0.0.1:8080', runScripts: 'outside-only' });
  globalThis.window = globalThis;
  globalThis.document = dom.window.document;
  // navigator 在 Node 22+ 只读 getter——defineProperty 覆盖
  Object.defineProperty(globalThis, 'navigator', { value: dom.window.navigator, configurable: true });
  globalThis.Node = dom.window.Node;
  globalThis.Event = dom.window.Event;
  globalThis.HTMLElement = dom.window.HTMLElement;
  // window 事件面——window 别名指向 globalThis（Node 全局无该 API）；页面脚本挂 resize 等监听需可调用
  if (typeof globalThis.addEventListener !== 'function') {
    globalThis.addEventListener = function () {};
    globalThis.removeEventListener = function () {};
  }
  return dom;
}

/**
 * mock EventSource 类——SSE 在测试环境无真实连接；事件由测试手动 fire
 * @returns {Function} EventSource 构造类
 */
export function createMockEventSource() {
  return class MockEventSource {
    constructor(url) { this.url = url; this._listeners = {}; }
    addEventListener(type, fn) { this._listeners[type] = fn; }
    close() {}
    // 测试辅助——手动触发事件
    fire(type, data) {
      const fn = this._listeners[type];
      if (fn) { fn({ data: JSON.stringify(data) }); }
    }
  };
}

/** 安装 EventSource mock（全局） */
export function installMockEventSource() {
  globalThis.EventSource = createMockEventSource();
}

/** 安装 fetch mock——避免真实 HTTP 请求；默认空响应，测试按需覆盖 globalThis.fetch */
export function installMockFetch() {
  globalThis.fetch = async () => ({ json: async () => ({}), ok: true });
}

/**
 * 安装剪贴板 mock（jsdom 无 navigator.clipboard——A81 复制原文消费面）
 * 写入记录挂 globalThis.__clipboardWrites 供断言；用例前清空：`window.__clipboardWrites.length = 0`
 * @returns {string[]} 写入记录数组（同 globalThis.__clipboardWrites）
 */
export function installMockClipboard() {
  const writes = [];
  Object.defineProperty(globalThis, 'navigator', {
    value: { clipboard: { writeText: async (text) => { writes.push(text); } } },
    configurable: true
  });
  globalThis.__clipboardWrites = writes;
  return writes;
}
