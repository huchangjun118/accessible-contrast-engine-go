# Accessible Contrast Engine（颜色可访问性引擎）

一个完全在本地进程内运行、无任何外部服务或真实设计资产依赖的 Go 库。输入“语义角色 → 颜色”的映射与关键角色对，引擎产出：

- 两种对比度模型下**相互独立、互不覆盖**的判定结论；
- 带透明度颜色在**显式底色**上按确定顺序的合成结果与可解释轨迹；
- 超出色域输入的**确定性回映**结果（不改变输入令牌的语义）；
- 在保持品牌色相与角色层级前提下自动派生的高对比主题，失败时精确定位到角色对；
- 版本化、原子替换、增量重算、读不阻塞写的并发主题存储。

```go
import c "github.com/huchangjun118/accessible-contrast-engine-go"
```

## 1. 数据模型与处理管线

| 类型 | 含义 |
| --- | --- |
| `RGB` | gamma 编码的 sRGB 三元组，通道**不限制**在 `[0,1]`，允许表达超色域输入 |
| `Color` | `RGB` + 直通（非预乘）alpha，`Alpha=1` 为不透明 |
| `RoleMapping` | `map[string]Color`，语义角色名到颜色的映射 |
| `RolePair` | 关键组合：`Foreground` 角色叠在 `Background` 角色上，`Usage` 决定阈值 |
| `Report`/`PairResult`/`Score` | 总报告 / 单角色对结果 / 单模型判定 |

每个角色对的求值管线固定为：

1. **合成**：前景、背景在显式底色上按 source-over 顺序合成为不透明显示色；
2. **色域回映**：把显示色确定性地映射回 sRGB 可显示范围；
3. **逐模型评分**：每个模型独立产出一个 `Score`，结论分开存放，绝不相互覆盖。

## 2. 对比度模型与阈值语义

两种模型同时求值（`ModelWCAG21`、`ModelAPCA`），`ContrastScores` / `Evaluate` 返回的 `[]Score` 顺序固定为 WCAG 在前、APCA 在后，保证可复现。

### 2.1 WCAG 2.x 相对亮度（`wcag21`）

- 先做 sRGB 反伽马得到线性光，再按 `Y = 0.2126R + 0.7152G + 0.0722B` 计算相对亮度；
- 对比度 `(L_lighter + 0.05) / (L_darker + 0.05)`，范围 `1..21`；
- **无极性**：交换前后景结果相同；`Score.Value == Score.Signed`。

### 2.2 感知对比度 APCA（`apca0.0.98G`）

- 实现 APCA **0.0.98G-W3**：分别对文字/背景亮度做不同指数的软钳位与伽马，输出有符号的 `Lc`（约 `[-108,106]`）；
- **有极性**：`Lc > 0` 表示深字浅底，`Lc < 0` 表示浅字深底；
- `Score.Signed` 保留有符号 `Lc`，`Score.Value = |Lc|` 用于与阈值比较；极性单独放在 `Score.Polarity`，符号信息永不丢失。

### 2.3 `Usage` 与默认阈值（`DefaultThresholds`）

阈值比较的是模型的**无符号幅度**：WCAG 为比值，APCA 为 `|Lc|`。

| Usage | 语义 | WCAG 阈值 | APCA `|Lc|` 阈值 |
| --- | --- | --- | --- |
| `text` | 正文文本 | 4.5 | 75 |
| `large` | 大号/加粗文本 | 3.0 | 60 |
| `enhanced` | AAA / 增强对比 | 7.0 | 90 |
| `ui` | 非文本 UI 图形 | 3.0 | 60 |

- 两个模型的适用范围不同：WCAG 是法规/规范常用的亮度比；APCA 更贴近感知且对极性敏感。同一组颜色在两个模型下分别判定，`PairResult.Pass` 为“**所有模型均通过**”。
- 可通过 `EvalConfig.Thresholds` 传入自定义的 `map[Model]ModelThresholds` 覆盖阈值；只提供部分模型时，未提供的模型不参与评分。
- 派生目标还可通过 `DeriveConfig.Margin` 在阈值之上再加余量。

## 3. 透明合成：顺序确定、底色显式

`CompositeOver(fg, bg, base)` 严格按两步 source-over 合成：

1. `background over base`（背景叠在底色上）；
2. `foreground over background`（前景叠在步骤 1 的结果上）。

source-over 公式（直通 alpha）：

```
outA = srcA + dstA·(1-srcA)
outC = (srcC·srcA + dstC·dstA·(1-srcA)) / outA
```

- 返回的 `CompositedColor.Steps` 逐步记录来源、目标与输出，合成结果**可解释、可复现**。
- **缺少底色的输入被明确拒绝**：只要前景或背景半透明（含全透明）而 `base == nil`，立即返回哨兵错误 `ErrMissingBase`（`errors.Is` 可匹配，`Evaluate` 中会被包装并带上角色对名）。引擎永远不会静默按黑底处理。
- 前后景均不透明时无需底色。

## 4. 色域映射：确定性、可复现、不改语义

`RGB` 通道允许超出 `[0,1]`（例如宽色域来源）。映射方法由 `GamutMappingMethod` 选择：

- `oklch-chroma-reduction`（默认）：转换到 **OKLCh**，保持色相角 h 与明度 L 不变，用二分搜索（64 轮、容差 `1e-7`）求仍落在 sRGB 内的最大色度 C。结果保持品牌色相，色度只减不增。
- `channel-clip`：逐通道钳制到 `[0,1]`，确定但会产生色相偏移，主要用于对照。

规则与保证：

- 纯函数、无随机量：同一输入永远得到同一输出（测试中重复调用逐字节相等）。
- 已在色域内的颜色原样返回，`GamutMapResult.InGamut=true`、`Iterations=0`。
- 非有限值有固定的清洗规则（`NaN→0`、`+Inf/-Inf` 先映射为有限越界值再走正常回映），不会 panic。
- 映射只用于**可访问性判定/显示**，绝不回写输入令牌：`RoleMapping` 中保存的颜色语义在判定之外保持不变（`PairResult.GamutMapped` 仅做标记）。

## 5. 高对比主题派生

`DeriveHighContrast(ctx, roles, pairs, cfg)` 在 OKLCh 空间迭代调整，直到所有关键角色对在目标模型下达到 `阈值 + Margin`：

- **品牌色相保持**：每个角色的色相角 h 全程不变，输出 `Adjustment` 以 OKLCh 表达原始/最终明度与色度，色相以度数给出；
- **角色层级与极性不破坏**：按原始明度排序角色，每轮后做层级投影，原始明度有明确高低关系的角色不允许反转；每个角色对的明暗极性（浅底深字 / 深底浅字）保持；
- **调整顺序**：优先以明度拉开端点距离（步长随缺口自适应），明度进展停滞或饱和时再单调递减色度（`C *= 0.8`）；色度只减不增；
- 目标模型可选 `ModelWCAG21`（默认）或 `ModelAPCA`，`Usage` 可覆盖各角色对自带用法；
- 尊重 `ctx` 取消；含透明度时同样要求显式 `Base`，否则返回 `ErrMissingBase`。

### 失败定位

当迭代达到 `MaxIterations`（默认 200）仍无法满足全部角色对时，返回的 `DeriveResult.OK=false`，`Failures []PairFailure` 中**逐个列出**仍失败的角色对：

- `Pair`：可定位的角色对（名称 + 前/背景角色名）；
- `Achieved` / `Required`：实际达到与要求的对比度幅度；
- `Reason`：原因（达到明度边界且层级受限导致阈值不可行等）。

`Tokens` 仍会返回当前最佳（不透明、在色域内）的派生结果。

## 6. 版本化、原子替换与增量重算

`Engine` 提供并发安全的主题存储：

- `NewEngine(roles, pairs, cfg)` 构造并提交 **版本 1**；`pairs`/`cfg` 此后只读。
- `Current() *Snapshot` **无锁读取**：内部用 `atomic.Pointer[Snapshot]`，读不阻塞写、写也不阻塞读。
- `Replace(ctx, roles)`：整表替换并重算全部角色对，版本号 +1。
- `UpdateTokens(ctx, changes)`：局部令牌变更，只重算 `ChangedPairs` 判定为受影响的角色对（前景或背景角色发生变化），未受影响的结果从旧版本**原样复用**后合并；内容无变化时不产生新版本。

### 一致性与失败隔离

- 每个 `Snapshot` 都是不可变整体。写入在**私有副本**上重算，完成后用**一次原子指针 Store** 发布；进行中的查询要么看到完整旧版本，要么看到完整新版本，**不会出现新旧混合**。
- 写操作通过单令牌通道串行化；求值失败或 `ctx` 取消时直接丢弃结果，**绝不触碰已提交快照**，版本号不变。
- 被取消的替换/增量重算返回错误与（增量场景下）未改动的旧 `Report`。

## 7. 配置项汇总

| 位置 | 字段 | 默认 | 说明 |
| --- | --- | --- | --- |
| `EvalConfig` | `Base *RGB` | 无 | 存在任何半透明颜色时**必填**；底色按不透明表面处理 |
| `EvalConfig` | `Thresholds` | `DefaultThresholds()` | 每模型、每用法的通过阈值 |
| `EvalConfig` | `GamutMethod` | `oklch-chroma-reduction` | 色域回映方法（或 `channel-clip`） |
| `DeriveConfig` | `Model` | `wcag21` | 派生目标模型（`wcag21` / `apca0.0.98G`） |
| `DeriveConfig` | `Usage` | 空 | 非空时覆盖所有角色对自带的用法 |
| `DeriveConfig` | `Margin` | 0 | 阈值之上的额外通过余量 |
| `DeriveConfig` | `MaxIterations` | 200 | 调整搜索的最大迭代轮数 |
| `DeriveConfig` | `Base` / `GamutMethod` | 同上 | 合成底色与色域映射方法 |

## 8. 测试

```bash
go test ./...          # 功能测试
go test -race ./...    # 并发安全（原子替换/读不阻塞写）
```

覆盖范围：

- **多模型判定**：WCAG 已知比值（黑/白=21、`#767676`≈4.54）、APCA 极性/对称性/零值、同一颜色两模型独立通过与失败、自定义阈值；
- **透明叠加**：缺底色拒绝（前景与背景两种情形、`Evaluate` 链路）、固定两步顺序与可解释轨迹、黑底 vs 白底结果不同、确定性与幂等；
- **色域映射**：超界/非有限输入回映、色相保持与色度只减、在色域恒等、clip 对照、可复现、输入令牌不被改写；
- **派生**：成功达标且保持色相/极性/表面层级、不可行高阈值返回精确定位的 `PairFailure`、取消传播、透明缺底色拒绝、APCA 模型派生；
- **并发替换**：初始版本、整表替换版本递增、失败/取消不污染、增量重算正确性与未受影响结果复用、无变化不升版本、高并发下读者只见一致快照、写进行中读不被阻塞。

## 9. 包与文件

| 文件 | 职责 |
| --- | --- |
| `color.go` | `RGB`/`Color`、hex 解析（`#rgb`/`#rgba`/`#rrggbb`/`#rrggbbaa`） |
| `gamut.go` | sRGB 线性化、OKLab/OKLCh 转换、色域映射 |
| `composite.go` | source-over 顺序合成、`ErrMissingBase`、合成轨迹 |
| `contrast.go` | WCAG 2.x 与 APCA 模型、阈值、极性、逐模型评分 |
| `eval.go` | 角色对求值管线（合成→回映→评分） |
| `derive.go` | OKLCh 高对比派生、层级投影、失败定位 |
| `theme.go` | 原子快照版本存储、整表/增量重算、并发控制 |
