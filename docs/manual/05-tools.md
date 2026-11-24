# 05 · 工具面与使用规范

> 猫「能动手」靠的是工具。本章讲工具怎么组织、怎么选、有哪些铁律。

## 工具怎么组织

- **工具组**：每工具组一个独立 Flow（**19 组现行**，工具池约 102 件）——文本 / 文件 / 代码 / 终端 / 网页 / 图像 / 数据 / 群组 / 配置 / 管理等。可独立热重载 / 退役。
- **每猫工具面**：`toolNames` 逐猫裁剪——同一宿主里，不同猫可见的工具集合不同。
- **双轨执行**：内置工具（会话内 / 宿主直执——`Note` / `pack` / `time` / `random` / `info` / `sleep` / `timer` / `timeback-*` 等）vs OA 工具（走工单由工具组 Flow 认领——`text-*` / `file-*` / `cs-*` / `mau-*` / `web-*` / `image-*` / `config-*` / `powershell` 等）。
- **OA 走单**：工具调用 → 工单（officeType=TOOL）→ 工具组 Flow 认领 → 结果落盒回执。链路失败即诚实报错，无静默直执保底。

## 工具分组速查

| 组 | 代表工具 | 能做什么 |
|:--|:--|:--|
| 会话直执（内置） | `info` `time` `random` `Note` `pack` `sleep` `timer` `timeback-*` `host-reload` `host-flows` | 状态自省 / 任务追踪 / 定时 / 作用域 / 热重载 |
| 文本 IO | `text-read` `text-write` `text-replace` `text-grep` `text-read_between` `text-read_lines` `text-append` | 读写 / 定位 / 区间替换 |
| 文件管理 | `file-tree` `file-find` `file-move` `file-delete` `file-copy` `file-version` | 结构浏览 / 移动 / 软删除 / 整棵复制 |
| C# 语法树 | `cs-read` `cs-patch` `cs-member` `cs-comment` `cs-find*` `cs-check` `cs-build` `cs-test` ……（13 件） | 语义级读写 / 编译裁决 / 跑测 |
| TypeScript 工程 | `ts-check` `ts-list` `ts-read` `ts-find-ref` `ts-patch` `ts-info` `ts-lint` `ts-test` `ts-format`（9 件） | 类型诊断 / 结构 / 改写 / 跑测 |
| 自举链 | `mau-verify` `mau-gen` `mau-proj` `mau-setup` | 语料检查 / 组翻译 / 部署 |
| 外部通道 | `powershell` `powershell7`（Windows）· `sh-exec`（Linux） | 单行指令——进程 / 端口 / 网络等 |
| 联网 | `web-search` `web-ds-search` `web-fetch` `web-fetch-jobs` | 搜索 / 结构化检索 / 下载 |
| 浏览器 | `browser-open/read/eval/shot/tabs` + `browser-headful` | 网页自动化 / 截图 / 有头登录（作用域内） |
| 视觉 | `image-analyze` `image-inject` | 识别取字 / 直读原图 |
| Python | `py-run` `py-list` `py-service` | 脚本执行 / 常驻服务 |
| 数据库 | `db-put/query/get/update/delete` · `dbAdmin-libs/create/drop` | 条目库读写 / 建库删库 |
| 群组 | `group-info/inbox/post/delegate/clear` · `group-newsession/catinfo/catcfg-get/catcfg-set` | 多猫协作 / 派单 / 组内配置 |
| QQ | `qqbot-send` `qqbot-info` `qqbot-botinfo` `qqbot-groups` | 主动发言 / 通道自述 / 群目录 |
| 配置 | `config-get/set` `config-cat-get/set` `config-list` `config-reset` | 全局 / 每猫配置 |
| 临时 | `temp-info` `temp-exec` | 万能执行 / 自举试验 |
| 管理（特权） | `restart-*` `majordomo-*` | 仅默认会话可见可调 |

## 怎么选工具（决策树）

```
启动 → info（必须第一步：受控根表 / 版本 / 状态）

读：知道锚点 → text-read_between · 知道行号 → text-read_lines · 要全文 → text-read
    · 图片识别要文字 → image-analyze · 要亲眼看图 → image-inject（作用域内）
写：.cs → cs-*（cs-read 先行）· .ts → ts-*（ts-read 先行）· 新建 → text-write
    · 改某处 → text-replace（锚点唯一）· 追加 → text-append
查：结构 → file-tree · 文件名 → file-find · 内容 → text-grep · 网页 → browser-*（域内）
清理：软删除 → file-delete · 移动 / 重命名 → file-move · 复制 → file-copy
执行：单命令 → 外部通道（Windows: powershell · Linux: sh-exec·最小面）
协作：派活给别的猫 → group-delegate · 只留信息 → group-post
杂项：时间 → time · 加载包 → pack · 定时唤醒 → sleep · 排程 / 按天闹钟 → timer
```

## 高频铁律

| # | 铁律 |
|:--:|:--|
| 1 | **每对话第一步 `info`**——受控根表 / 版本 / 状态以返回值为唯一真相 |
| 2 | **锚点唯一**——`text-replace` / `text-read_between` 的锚点须全文唯一；锚点取完整原文行，不取搜索结果片段 |
| 3 | **同文件读与写不并发**——写操作串行单发 |
| 4 | **`text-write` 覆写不可逆**——改某处优先 `text-replace`；确需覆写先读全文 |
| 5 | **删除用显式标记**——删除匹配文本传删除标记；不靠空串 |
| 6 | **外部通道 = 单行指令**——命令 + 字面量参数；禁代码形态 / 多段 / 管道 / 块；禁列目录（走 `file-tree`）；文件读写走 `text-*` |
| 7 | **`cs-*` / `ts-*` 修改前先读**；编译裁决唯一权威 = `cs-build` / `ts-check` |
| 8 | **失败必须可见**——工具报错具名（`ERR\|CODE\|现象`），不静默回落 |
| 9 | **动作有固定通道 → 走通道**——部署走 `mau-setup`、重启走特权链；不手拼命令链、不绕拦截 |
| 10 | **阻断 ≥3 次即停**——同一环节连阻三次，停下汇总（已试 / 现状 / 卡点 / 建议）等决策 |
| 11 | **大过程用作用域（timeback）**——预期串接 ≥3 次调用才收口的环节开域，收域后按回读规范读回原文 |

## 受控根与寻址

- 路径优先 `<根id>:<相对路径>`（如 `mau:docs/manual/index.md`）；裸根名 = 该根根目录；绝对路径可用；越界拒绝。
- 受控根表以 `info` 返回为准（动态注册）——不凭记忆假设路径。
- 只读根拒绝写；根表本身只读（防改根绕边界）。

> 改造实操 → 08；自举链工具细节 → 06。
