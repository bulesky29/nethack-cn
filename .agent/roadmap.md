# nh-helper roadmap

按"性价比 × 用户体感"排，从高到低。需要的时候再做，不必都做。

## 高价值，未做

### 1. 快速 AI 接入 popup 分类回退
基础设施已经齐了 (`Config.FastModel/FastBaseURL/FastAPIKey`、默认 `google/gemini-2.5-flash-lite`)，但 `classifyPopup` 当前只用纯正则。正则对目前观察到的所有 popup（库存 / take-off / drop / name / Book of Issek / tutorial）都判得对，零延迟、零费用。

何时该做：用户报错说某个 popup 分错窗了。那时把 fast model 接进 `classifyPopup`，只对正则判 0-1 行 letter-pattern 的"灰色地带"内容调一次 AI。预计成本忽略不计、延迟 +100-300ms 但只发生在边缘案例。

### 2. 单字符职业称号 glossary 种子
NetHack 每个职业都有 ~9 个 title （`Troglodyte` → `Aborigine` → `Barbarian` → …）。手动塞全 13 职业 × 9 title ≈ 120 条 glossary 太累。

办法：写一个 `scripts/seed-titles.sh` 一次性灌入，或在 `manual.go` 里加个 `-seed-titles` flag 让 nh-helper 自己生成 SQL 喂给 SQLite。

### 3. 跨平台 CJK 字体处理
当前 `manual.go` 的 `cjkFontCandidates` 列了 macOS / Linux / Windows 路径，但 Linux/Windows 上典型字体常是 `.ttc`（TrueType Collection），maroto / gofpdf 的 `Bytes:` 只接 `.ttf`，TTC 会解析失败。

办法：用 `golang.org/x/image/font/sfnt` 解析 TTC，抽出一个 face 重新序列化成 TTF 字节流再喂给 maroto。或者直接 `go:embed` 一个开源 CJK TTF（思源黑体 subset ~5MB）兜底，所有平台都能跑。

### 4. 终端 resize 时刷状态面板
现在 `signals_unix.go` 把 `SIGWINCH` 收了但只在 host 端用来改 SSH 窗口大小。client 端的 `ui.go` 算 pane 宽度是启动时 `term.GetSize()` 算一次后就缓存。窗口拖大拖小后 `┌── ... ───┐` 的右边界会错位。

办法：client 也 listen `SIGWINCH`，触发时重算 `cols`/`rows`，重画 frame。Windows 没这个信号，跳过即可。

### 5. 标准 OS 配置位置
当前 `config.json` 和 `nh-helper.db` 都放在二进制旁。Windows 下二进制可能在 `Program Files` 这种不可写位置；Linux 下用户可能希望走 `~/.config/nh-helper/`。

办法：path 解析顺序：`$NH_HELPER_HOME` env → `os.UserConfigDir()` → 二进制旁。binaryDir 留作 fallback。

## 中价值，未做

### 6. 翻译成本 / token 追踪
SQLite 里 `translations` 表已经有 `hits` 列。再加 `input_tokens`、`output_tokens`、`cost_cents` 列，每次 LLM 返回时记账（OpenRouter 响应 header 里有 `x-ratelimit-*`）。`nh-helper -stats` 输出本月花了多少钱、命中率多少。

### 7. Glossary 导入 / 导出工具
`nh-helper -glossary-export terms.json` / `-glossary-import terms.json`。方便用户之间分享 NetHack 中文词条库。

### 8. 词条质量评分
让 fast model 定期审视 `source='auto'` 的词条，给质量分。低分的标记 `quality<0.5` 在下次匹配时跳过。

### 9. Wiki 链接
长 popup 翻译时，如果检测到怪物 / 物品名（在 glossary 里），可以追加一行 `[wiki: https://nethackwiki.com/wiki/Kobold]` 供玩家深查。

## 低价值 / 实验性

### 10. 单窗口 split-pane 替代方案
现在是 host + menu + text 三个窗口，桌面会很乱。DECSTBM 只支持一个 scroll region，单终端实现两个独立滚动区很难。替代方案：

- 集成 tmux：检测到 tmux 环境时，host 在 tmux 里 `split-window` 出两个 pane 跑 client，省去 osascript 弹窗。
- 自写 ncurses 风格管理（很复杂，不推荐）。
- 在 host 终端里画一个状态条（host 终端本来就显示 NetHack 屏，再叠状态有可能干扰游戏渲染）。

### 11. 机翻兜底
之前讨论过的：极短消息（远观名词）走免费 MT (MyMemory / LibreTranslate)，省 LLM 调用。但前提是这些 MT 对 NetHack 黑话翻得不离谱，需要先评估。

### 12. 自训 NetHack 翻译模型
长期来看，针对 NetHack 微调一个小模型（< 1B），本地跑，零延迟零费用。需要构造 NetHack 中英对照数据集，量级 10k-100k 对。是个独立的小项目。

## 维护性

### 13. Linux / Windows 终端弹窗 UX 打磨
当前 `terminal_linux.go` 试 `gnome-terminal/konsole/xterm/x-terminal-emulator`，`terminal_windows.go` 试 `wt.exe` 然后 fallback `cmd /c start`。各家终端的参数差异、tmux/screen 兼容性、SSH 远程下的行为都需要实测打磨。

### 14. 集成测试
现在测试只覆盖 ui 渲染和 manual 生成。理想是有一个 fake SSH server + canned screen events 跑整个事件管道，验证 host→router→client 链路。
