# nh-helper

NetHack 实时中文翻译助手 —— 一个针对 macOS 的单文件 Go 程序，让你在 `nethack@alt.org` 公共服务器上玩 NetHack 时，自动把屏幕顶部的英文消息翻译成中文，显示在另一个独立的终端窗口里。

## 它在做什么

游戏过程中，NetHack 会在终端最上方的两行不断打印各种消息：战斗结果、物品提示、陷阱警告、菜单内容等等。对英文不熟的玩家来说，最致命的"诅咒物品""毒气陷阱""石化攻击"提示往往一闪而过就被错过了。

`nh-helper` 把整件事拆成两个进程：

- **Host（宿主）模式**：你在这个终端里正常玩 NetHack。程序在背后通过 SSH 连接到 `alt.org`，并在内存里维护一个虚拟终端，持续监测屏幕顶部两行的变化。
- **Client（翻译）模式**：自动弹出的另一个 Terminal 窗口，负责接收宿主推送过来的英文消息，过滤掉无意义的内容（移动、撞墙、状态栏等），再把真正重要的文本送到 OpenRouter 让大模型翻译成中文，实时打印出来。对涉及诅咒、陷阱、致命威胁的内容会自动加 `[警告]` 标记。

两个进程通过本地 TCP（`127.0.0.1:9999`）通信，互不影响游戏体验。

## 怎么用

1. 编译：

   ```bash
   go build -o nh-helper .
   ```

2. 首次运行（必须在原生 Terminal.app 里执行，不能在 VSCode 内置终端里跑，因为要用 AppleScript 弹新窗口）：

   ```bash
   ./nh-helper
   ```

3. 第一次启动会交互式询问以下配置，写入同目录下的 `config.json`：
   - SSH 用户名（**默认 `nethack`，直接回车即可**；这是 alt.org 的公共网关账号，不是你的游戏账号）
   - SSH 密码（**alt.org 留空，直接回车即可**）
   - OpenRouter API Key
   - 模型 slug（可直接回车使用默认 `google/gemini-2.5-flash`）

   > **关于 alt.org 的两层登录**：alt.org 的 SSH 接入用的是公用账号 `nethack`（密码为空），所有玩家都用它"敲门"。SSH 连上之后服务器会跑一个叫 `dgamelaunch` 的菜单程序，**你自己的 alt.org 游戏账号是在那个菜单里输入的**，nh-helper 不需要也不应该保存它。如果你之前把游戏账号填进了 SSH 字段，删掉 `config.json` 重跑即可。

4. 之后程序会自动：
   - 弹出一个新的 Terminal 窗口运行翻译端
   - 通过 SSH 登录 `nethack@alt.org`
   - 你直接在原窗口开玩，翻译会持续出现在新窗口里

按 `Ctrl+C` 或正常退出 NetHack 后，终端的原始状态会自动恢复。

## 配置说明

`config.json` 与可执行文件放在同一目录，权限是 `0600`：

```json
{
  "ssh_user": "nethack",
  "ssh_password": "",
  "openrouter_api_key": "sk-or-...",
  "model": "google/gemini-2.5-flash",
  "base_url": "https://openrouter.ai/api/v1/chat/completions"
}
```

- `model`：OpenRouter / OpenAI 兼容接口的模型 slug。留空或不写则使用默认 `google/gemini-2.5-flash`；想换可填 `anthropic/claude-haiku-4.5`、`openai/gpt-4o-mini` 等。
- `base_url`：聊天补全接口的完整 URL，默认指向 OpenRouter。如需走自建/代理的 OpenAI 兼容端点，填入对应的 `/chat/completions` 路径即可。
- 老版本的 `config.json` 不需要迁移，缺失的字段会自动回落到默认值。

## 依赖

- Go 1.22+
- `golang.org/x/crypto/ssh` —— SSH 客户端
- `golang.org/x/term` —— 终端原始模式与密码读取
- `github.com/hinshun/vt10x` —— 解析 ANSI 序列、还原虚拟屏幕

## 限制

- 仅支持 macOS（自动开窗依赖 `osascript` 与 Terminal.app）
- 主机密钥校验当前为信任全部（`ssh.InsecureIgnoreHostKey`），如有更高安全要求请改用固定指纹
- 翻译有 LLM 调用的延迟与费用，过滤策略已经尽量保守，但仍可能漏掉某些不常见的关键消息
