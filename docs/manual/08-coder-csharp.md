# 08 · 上手改造 CatHome4

> 本章给**接手改造者**——读完即可用 C# 工具组（cs-*）动手改 CatHome4 的宿主代码。

## 改造前的准备

1. **确认入口**：仓库根 = 受控根 `mau`。宿主入口 `CatHome4.sln`，基座入口 `Mau.sln`。
2. **确认环境**：.NET 10 SDK（`dotnet --version` ≥ 10.0）；Linux 侧另需运行时。
3. **确认改动分类**（见 06）——宿主程序集改动走「部署 + 重启」，语料 / 积木改动走「热重载」。分类错了，就会得到「改了没生效」。

## Coder 方法论（动手前的思维）

**主循环：**

```
拆需求 → 前提核验 → 防撞车 + 定归属 → 选工具 + 定权威源（磁盘 > 缓存 > 索引 > 记忆）
  → 写（.cs 走 cs-*：cs-read 先读 → cs-patch / cs-member / cs-comment 改）
  → 验证两层 → 异常则红灯（停下 → 归因 → 实锤 → 修复 → 再验证）→ 影响面同步 → 下一步
```

**关键决策锚点：**

| 锚点 | 行为 |
|:--|:--|
| **合约优先** | 跨程序集边界先定义接口 → 编译通过 → 再写实现 |
| **闸门** | ≥2 个合理方案 / 需求不明 / 事实不清 → 列选项 + 利弊，等拍板；信息在对方手里先问 |
| **前提核验** | 方案引用的每个既有物（机制 / 字段 / API 参数 / 约定名）在实现面核实一遍——存在吗？语义如假设吗？ |
| **验证与证据** | 动作与验证成对；证据有资格——直读 > 元数据，真机行为 > 静态搜索 |
| **异常红灯** | 任何预期未兑现（报错 / 超时 / 静默成功）→ 停下归因，不带着错误假设往下做 |
| **影响面** | 结构 / 命名 / 接口变更前先列全部承担者（定义 / 消费 / 断言 / 文档 / 注释），每处一句处置 |

## cs-* 工具组工作流

```
进入：修改 .cs 项目代码

1. [扫结构]  cs-list（class 空 = 全项目）
2. [读现状]  cs-read（🔴 改前必读——不凭记忆改；签名后缀区分重载；.ctor / 类名 = 构造函数）
3. [选择改法]
     改方法体        → cs-patch（body 完整含大括号——首选）
     加 / 删 / 改名成员 → cs-member（insert / delete / rename，rename 全项目同步）
     改注释          → cs-comment（summary / param / returns）
     行级 / 结构级   → text-replace 单点锚点（签名变更 / 字段↔属性 / 删 using）
4. [每步裁决]  cs-check（语法层）→ cs-build（🔴 唯一权威）
5. [写侧规整]  cs-format check 作格式哨兵
6. [交付自检]  cs-check 0 error + cs-build 0/0 + cs-comment_check + cs-dead + cs-format check 零差异
```

**入口三态统一：** `path` 接 csproj / .sln / 目录——聚合面（`cs-list` / `cs-check` / `cs-build` / `cs-find` / `cs-dead` / `cs-comment_check`）用 .sln 或目录；成员级四件（`cs-read` / `cs-patch` / `cs-member` / `cs-comment`）用 csproj，或「多项目入口 + class 定位」。

**cs-* 铁律：** 修改前先读（`cs-read`）· 每步改完立即 `cs-check` + `cs-build` · 裁决以 `cs-build` 为准 · 写操作串行单发 · 改方法体优先 `cs-patch`（只支持方法 / 构造函数体）· 行级与结构级走 `text-*` · 项目有残留编译错先修错误再改 · 被拒参数是保护（改参数不绕行）。

**TypeScript 同构：** 同样的读 / 改 / 验节奏换 `ts-*`（`ts-read` → `ts-patch` → `ts-check`），工程口径以 `tsconfig` 为准（`ts-info` 可查生效档位）。

## 编码规范要点

| 项 | 要求 |
|:--|:--|
| **大括号** | 一律换行——`void Foo()` 后换行接 `{`；禁 `{}` 同行 / `} else {` / `} catch {`；单行代码块展开（属性 getter 可单行） |
| **语法糖禁用** | `=>` → `{ return; }` · `x++` → `x = x + 1` · `var` → 显式类型 · `?.` → if 判空 · 三元 → if / else · `??` → if 判 null |
| **命名** | 同类实体同前缀（前缀即层级标记）；词缀三禁（学术后缀 / 无效后缀 / 奇怪缩写） |
| **注释** | 所有类 / 函数 / 参数 / 返回值 / 字段（含 private）必带 `/// <summary>`——改前先读、改后同步、覆写不丢；超 10 行的函数插 `// [段N] 动词开头 ≤20 字` |
| **Nullable** | 框架注入 `= null!` / API 返回值文件级 `#nullable disable warnings` / 实际可空 `?` + 判空；修复优先级 CS8602 → CS8618 → CS8625 / CS8600 → CS0414 |
| **LOG** | 格式 `动词 \| 参数1 \| 参数2`；等级 INFO（默认）/ WARN（,2）/ ERROR（,3） |

## 交付自检（两层，缺一不可）

| 层 | 内容 |
|:--|:--|
| **编译层** | `cs-check` 语法 0 error + `cs-build` 0/0 + `cs-comment_check` + `cs-dead` + `cs-format check` 零差异 |
| **生效层** | 消费链核实 + 跑测可见 + 运行形态自证——「改即生效」类承诺实测运行实例行为，不读代码推断 |

汇报格式：`✅ 完成。{文件}.{方法}：{变更摘要 ≤30字}。{验证面}通过。`

> **总纲：一切过程信号不构成验收**——编译 0/0 / 测试全绿 / findings 完整都只是「没有反证」；验收判据只能是**语义目标的正面陈述**。

## 场景速查（做某事用哪些工具）

| 你在做什么 | 工具序列（按顺序） |
|:--|:--|
| 理解 / 修改代码 | `cs-list` → `cs-read` → `cs-patch` / `cs-member` / `cs-comment` → `cs-check` + `cs-build` |
| 排查编译错 | `cs-check(full=true)` → `cs-read` → 修复 → `cs-build` |
| 查引用 / 重构前 | `cs-find_ref`（含重载全匹配，跨程序集标 `[跨程序集]`） |
| 查定义位置 | `cs-find`（名字反查定义） |
| 交付自检 | 编译层五件 + 生效层实证 |
| 实机跑测 | `cs-test`（path 三态；可选 `filter` / `noBuild`） |
| 格式规整 | `cs-format mode=check`（干跑）→ 需要时 `apply` |
| 跨程序集改签名 | `text-*` 写入 → `cs-build` 全量裁决（`cs-patch` / `cs-member` 预检会误报） |

## 常用命令（工作目录 = 仓库根）

| 命令 | 用途 |
|:--|:--|
| `dotnet build CatHome4.sln` · `dotnet build Mau.sln` | 宿主 / 基座全量构建（0 error 0 warning） |
| `dotnet test CatHome4.sln` · `dotnet test Mau.sln` | 宿主 / 基座测试 |
| `mau test` / `mau check` | 三段门禁 / 语法谱 + 负例谱 |
| `mau verify <file.mau>` | 语料全链检查 |
| `mau proj <组>.mauproj -o <dir> --build` | 组翻译 + 出 dll |
| `mau bricks index --update` | 积木索引重建（唯一写入通道） |
| `SetUp.exe prepare` · `SetUp.exe deploy <目录>` | 仓库内重建 / 发布正式实例 |
| `CatHome4.exe --run "<指令>"` | CLI 全链（脚本化跑测） |

## 仓库约定

| 约定 | 内容 |
|:--|:--|
| 提交信息 | `vX.Y: 变更摘要`（≤30 字，动词开头） |
| 发布标签 | 重要版本打 `v<版本号>` 标签 |
| 版本单点 | 仓库根 `Directory.Build.props` 的 `<Version>`——宿主与基座同一条版本线 |
| 双远程 | Gitee `origin`（日常推送）· GitHub 镜像（自动同步，不直接推） |

## 进阶：减少上下文膨胀（timeback）

改码 / 编译 / 跑测的过程输出（编译日志、报错栈、测试流水）体量大且用完即弃——**预期串接 ≥3 次工具调用才收口**的环节，开一个 `code_write` 作用域在其中完成读 / 改 / 编译 / 跑测，收回时只带回「变更位置 + 可复跑跑测」，过程输出留在域内销毁。审查取证（读码 / 查引用）走 `code_review` 域，网页 / 看图走 `browser_vision` / `image_vision` 域。

> 收域后**必按变更位置回读**（未回读 = 本域未闭合）。
