# ts 服务压测载具（A292）

服务层压测与实工程验证的载具与驱动。**生成物不进 git**（`.gitignore`）。

## 组成

| 文件 | 用途 |
|:--|:--|
| `gen_bench.js` | 合成工程生成器——`--out <dir> --files N [--with-error]`；块内链深 ≤20（深链会让 TS 类型展开爆炸，掩盖被测成本） |
| `bench.js` | 压测驱动——直连 `stdio_server.js`（绕 C# 宿主，测服务层真相） |
| `bench_tsc.js` | 参照基线——同工程 `tsc --noEmit` 耗时（区分「TS 本身成本」与「服务实现放大」） |
| `probe.js` | 单请求探针——打印原样回执（验语义正确性） |
| `cocos/` | 真实形态载具——Cocos 3.8.8 引擎声明 + 手写工程骨架（播放内核 / 剧情树 / 玩法容器 / UI / 平台桥） |

## 用法

```
node gen_bench.js --out bench/b300 --files 300
node bench.js --project bench/b300 --scenario check --count 5
node bench.js --project bench/b300 --scenario check --conc 8 --count 64
node bench.js --project bench/b50 --project bench/b300 --project bench/b1500 --scenario multi --count 2
node bench.js --project bench/b1500 --scenario patch --count 5
node bench_tsc.js --project bench/b1500 --repeat 3
node probe.js --op check --project bench/fixtures-err
```

场景：`ping` / `check` / `list` / `read` / `findref` / `patch` / `mix` / `multi` / `mem`。

## Cocos 载具的引擎声明

`cocos/engine/cc.d.ts` 不进 git，从本地编辑器取：

```
<Cocos 安装目录>\resources\resources\3d\engine\bin\.declarations\cc.d.ts
  → bench/cocos/engine/cc.d.ts
```

`cc.editor.d.ts` **不要拷**——编辑器内专用，与 `cc.d.ts` 合并后产生重复标识符。

工程口径：`lib` 含 DOM / ES2015、`skipLibCheck: true`（与 Cocos 模板同口径）——引擎声明自身的历史噪音不参与诊断，引擎类型仍参与本工程代码的类型检查。
