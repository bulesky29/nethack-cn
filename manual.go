package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/signintech/gopdf"
)

// manualFileName is the PDF written next to the binary on first startup.
// Delete it and re-launch nh-helper to regenerate.
const manualFileName = "nh-helper-手册.pdf"

// cjkFontCandidates lists TTF files on macOS that ship with full CJK
// coverage. We probe them in order at runtime. Arial Unicode.ttf is the
// safest bet on any post-10.5 install.
var cjkFontCandidates = []string{
	"/Library/Fonts/Arial Unicode.ttf",
	"/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
}

type manualItem struct {
	key  string
	desc string
}

type manualSection struct {
	title string
	intro string       // optional one-liner under the heading
	items []manualItem // optional key/description pairs
	notes []string     // optional free-form paragraphs after the items
}

var manualContent = []manualSection{
	{
		title: "关于本手册",
		intro: "这份手册由 nh-helper 在首次启动时自动生成。它收录了 NetHack 玩家最常用的按键、状态条解读、地图符号、和新手最容易踩坑的几条生存建议。",
		notes: []string{
			"想要重新生成（比如改了源代码里的内容），删掉本 PDF 文件再启动 nh-helper 即可。",
			"完整命令请在游戏里按 ? 查阅帮助菜单，或访问 https://nethackwiki.com/ 查阅 wiki。",
		},
	},
	{
		title: "1. 移动",
		items: []manualItem{
			{"h  j  k  l", "西 / 南 / 北 / 东（vi 风格方向键，单步）"},
			{"y  u  b  n", "西北 / 东北 / 西南 / 东南（对角线，单步）"},
			{"H J K L 等", "大写方向键：一直走，直到撞墙或遇到东西"},
			{"g + 方向", "走到当前房间的边缘"},
			{"G + 方向", "一路走到下一个有趣的位置（楼梯、岔路、物品）"},
			{"_", "Travel：在地图上挑一个目标位置自动寻路"},
			{"<", "上楼梯（站在 < 上）"},
			{">", "下楼梯（站在 > 上）"},
			{"数字 + 方向", "走 N 步（先按 n、键入数字、再按方向键）"},
		},
	},
	{
		title: "2. 战斗",
		items: []manualItem{
			{"F + 方向", "强制攻击该方向（即使没看到怪也挥武器，可打隐形怪）"},
			{"t", "投掷物品（先选物品，再选方向）"},
			{"z", "使用法杖（先选法杖，再选方向）"},
			{"Z", "施法（需要先在 + 菜单里学会咒语）"},
			{"#force", "用武器砸开锁着的箱子（武器可能损坏）"},
		},
	},
	{
		title: "3. 物品 / 背包",
		items: []manualItem{
			{"i", "查看背包"},
			{",", "捡起脚下物品"},
			{"d", "丢弃单个物品"},
			{"D", "按类型多选丢弃"},
			{"W", "穿盔甲"},
			{"T", "脱盔甲"},
			{"w", "装备武器（按 - 切换为徒手）"},
			{"P", "戴戒指 / 护符"},
			{"R", "摘戒指 / 护符"},
			{"r", "读卷轴"},
			{"q", "喝药水"},
			{"e", "吃东西"},
			{"a", "使用工具（绳钩、口哨、信号灯等）"},
		},
	},
	{
		title: "4. 探索",
		items: []manualItem{
			{"s", "搜索周围（找隐藏门 / 陷阱），多按几次能提高发现率"},
			{"o", "开门"},
			{"c", "关门"},
			{"^d", "踢"},
			{";", "远观（farlook）：指地图任意位置，查看那里是什么"},
			{":", "看脚下（同 . 但语义更明确）"},
			{"\\", "已发现物品 / 怪物的列表"},
			{"?", "帮助菜单"},
			{"^x", "查看自己角色的属性面板"},
		},
	},
	{
		title: "5. 进阶 # 命令",
		items: []manualItem{
			{"#pray", "向神祈祷（HP 极低 / 中毒 / 濒死时救命）"},
			{"#offer", "在祭坛 _ 上献祭怪物尸体（提升与神关系）"},
			{"#enhance", "提升武器熟练度（杀够数后用此命令确认升级）"},
			{"#chat", "与 NPC 交谈"},
			{"#loot", "翻开脚下的箱子 / 容器"},
			{"#dip", "蘸药水 / 蘸水"},
			{"#invoke", "唤起神器的特殊能力"},
			{"#ride", "骑乘（如果踩在被驯服的座骑上）"},
			{"#force", "用武器砸开锁"},
			{"#name", "给怪物 / 物品起名（标记用）"},
			{"S", "存档（下次启动可继续）"},
			{"#quit", "放弃当前角色（不可挽回）"},
		},
	},
	{
		title: "6. 状态条解读",
		intro: "nh-helper 客户端窗口顶部 4 行就是翻译后的状态条，下面是原始字段的含义。",
		items: []manualItem{
			{"St / Dx / Co", "力量 / 敏捷 / 体格"},
			{"In / Wi / Ch", "智力 / 感知 / 魅力"},
			{"HP : N(M)", "当前生命值 / 最大生命值"},
			{"Pw : N(M)", "当前法力值 / 最大法力值"},
			{"AC : N", "护甲值，越低越好；负数最佳"},
			{"Dlvl : N", "当前楼层"},
			{"Xp : N", "经验等级"},
			{"$ : N", "身上的金币"},
			{"T : N", "总回合数"},
			{"Lawful / Neutral / Chaotic", "阵营：守序 / 中立 / 混乱"},
			{"Hungry → Weak → Fainting → Starving", "饥饿 → 虚弱 → 晕厥 → 濒死"},
			{"Confused / Stunned / Hallu / Blind", "困惑 / 眩晕 / 幻觉 / 失明"},
			{"Burdened / Stressed / Strained", "背负 / 负担重 / 极限（移动速度降低）"},
		},
	},
	{
		title: "7. 地图符号速查",
		items: []manualItem{
			{"@", "你自己（或人类 NPC）"},
			{"a-z  A-Z", "各种怪物（k 狗头人, F 真菌, d 狗, h 矮人, …）"},
			{".", "地面"},
			{"#", "走廊"},
			{"|  -  +", "墙壁 / 门"},
			{"<  >", "上 / 下楼梯"},
			{"_", "祭坛"},
			{"{", "喷泉"},
			{"}", "池塘 / 水域"},
			{"$", "金币"},
			{") (", "武器 / 武器堆"},
			{"[", "盔甲"},
			{"=", "戒指"},
			{"\"", "护符"},
			{"!", "药水"},
			{"?", "卷轴"},
			{"/", "法杖"},
			{"+", "法术书 / 关着的门"},
			{"*", "宝石 / 矿石"},
			{"%", "食物 / 尸体"},
			{"^", "陷阱标记（已发现）"},
			{"`", "巨石 / 雕像"},
		},
	},
	{
		title: "8. 新手存活建议",
		intro: "按重要程度从高到低排列，前 3 条几乎能让你存活率翻倍。",
		items: []manualItem{
			{"① 救命法术", "HP 低于最大 1/7 时立刻 #pray，新角色至少能救一次"},
			{"② 不徒手鉴别", "永远不要直接戴上未鉴定的戒指 / 护符；扔到祭坛 _ 上、或等鉴定卷轴"},
			{"③ 绕开陷阱", "看到 ^ 标记永远绕开走；反复经过陷阱迟早出事"},
			{"④ 别白拿商店货", "想买什么放回原位 → 走到门口 → 老板自动结账。直接拿了跑老板会变 angry shopkeeper（极强）"},
			{"⑤ 拐角先远观", "跨过门 / 走廊拐角前先用 ; 看一眼对面有没有怪"},
			{"⑥ 永远带食物", "背包里常备 2-3 份食物；饥饿状态下战斗能力大幅下降，濒死 (Starving) 直接 #pray 救命"},
			{"⑦ 每升级看 enhance", "每次 Xp 升级后用 #enhance 检查武器技能能不能 unlock"},
			{"⑧ 献祭看阵营", "祭坛阵营要和你一致；献祭异己阵营的尸体会激怒你的神"},
			{"⑨ 喷泉 / 王座 / 水槽慎用", "新手期风险远大于收益，能不互动就别互动"},
			{"⑩ 进新楼前存档", "S 存档后下楼，万一秒杀也能从这个档继续"},
		},
	},
	{
		title: "9. nh-helper 使用说明",
		items: []manualItem{
			{"顶部状态面板", "4 行固定面板：标题（玩家 · 职业 · 阵营 · 回合）/ HP·Pw 进度条 / 六维属性 / 状态徽章。HP 进度条按比例从绿变黄变红"},
			{"下方翻译流", "每条带时间戳和分隔线；源文 dim 灰、译文正常色"},
			{"全屏弹窗", "剧情 / 物品菜单 / 商店清单等会整段抓取送翻，结构保持"},
			{"短消息", "远观结果（kobold, doorway 等）也会翻译，放心多按 ;"},
			{"翻译缓存", "同一段英文翻一次后写入 SQLite；后续命中瞬出、零费用"},
			{"职业称号", "默认显示英文。想看中文，往 glossary 表加条目（见下）"},
			{"自动学词", "长文本翻译后异步抽词进 glossary，人工词条永不被覆盖"},
			{"数据库快照", "每次正常退出复制一份到 db/ 目录，保留最近 20 份"},
			{"-debug 标志", "启动时加 -debug 会写 nh-helper.raw.log（事件流）和 nh-helper.translate.log（LLM 请求 / 响应详情）"},
		},
		notes: []string{
			"手动加职业称号示例（在终端运行）：",
			"  sqlite3 nh-helper.db \"INSERT INTO glossary (en, zh, category, source, updated) VALUES ('Troglodyte', '穴居人', 'class_title', 'manual', strftime('%s','now'))\"",
			"加了之后状态条标题会自动变成「玩家名 · 穴居人 (Troglodyte) · 守序 · T 129」。",
		},
	},
}

// ensureManual generates the manual PDF if it doesn't already exist next
// to the binary. Failures are non-fatal — we print a warning and let the
// rest of the program run. Returns the absolute path of the file (whether
// newly created or pre-existing) and a bool indicating whether we wrote
// it this invocation.
func ensureManual() (string, bool, error) {
	dir, err := binaryDir()
	if err != nil {
		return "", false, err
	}
	path := filepath.Join(dir, manualFileName)
	if _, err := os.Stat(path); err == nil {
		return path, false, nil
	}

	font, err := findCJKFont()
	if err != nil {
		return path, false, err
	}
	if err := generateManual(path, font); err != nil {
		return path, false, err
	}
	return path, true, nil
}

func findCJKFont() (string, error) {
	for _, p := range cjkFontCandidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no CJK font found; tried %v", cjkFontCandidates)
}

// pdfWriter wraps a gopdf.GoPdf with cursor + page-break tracking so the
// section renderers can think in terms of "draw this block" instead of
// fiddling with absolute Y coordinates.
type pdfWriter struct {
	pdf      *gopdf.GoPdf
	fontName string
	pageW    float64
	pageH    float64
	marginL  float64
	marginR  float64
	marginT  float64
	marginB  float64
	y        float64
}

func (w *pdfWriter) newPage() {
	w.pdf.AddPage()
	w.y = w.marginT
}

func (w *pdfWriter) needSpace(h float64) {
	if w.y+h > w.pageH-w.marginB {
		w.newPage()
	}
}

func (w *pdfWriter) heading(text string, size float64) {
	w.needSpace(size * 2)
	w.y += size * 0.4
	w.pdf.SetFont(w.fontName, "", size)
	w.pdf.SetX(w.marginL)
	w.pdf.SetY(w.y)
	_ = w.pdf.Cell(nil, text)
	w.y += size * 1.2
	w.y += 2
}

// item draws a key column on the left and a description column on the
// right, wrapping the description if it overflows.
func (w *pdfWriter) item(key, desc string) {
	const (
		bodySize = 10.5
		keyColW  = 130.0
		gutter   = 12.0
	)
	descX := w.marginL + keyColW + gutter
	descW := w.pageW - descX - w.marginR

	w.pdf.SetFont(w.fontName, "", bodySize)
	lines := wrapText(w.pdf, desc, descW)
	if len(lines) == 0 {
		lines = []string{""}
	}
	rowH := bodySize * 1.35
	needed := float64(len(lines)) * rowH
	w.needSpace(needed + 2)

	w.pdf.SetX(w.marginL)
	w.pdf.SetY(w.y)
	_ = w.pdf.Cell(nil, key)

	for i, line := range lines {
		w.pdf.SetX(descX)
		w.pdf.SetY(w.y + float64(i)*rowH)
		_ = w.pdf.Cell(nil, line)
	}
	w.y += needed + 1
}

func (w *pdfWriter) paragraph(text string) {
	const bodySize = 10.5
	width := w.pageW - w.marginL - w.marginR
	w.pdf.SetFont(w.fontName, "", bodySize)
	lines := wrapText(w.pdf, text, width)
	rowH := bodySize * 1.4
	needed := float64(len(lines)) * rowH
	w.needSpace(needed + 4)
	for _, line := range lines {
		w.pdf.SetX(w.marginL)
		w.pdf.SetY(w.y)
		_ = w.pdf.Cell(nil, line)
		w.y += rowH
	}
	w.y += 4
}

// wrapText character-wraps text at the given pixel width. CJK runs cleanly
// because every rune is a candidate break point; for ASCII we accept that
// breaks may land mid-word (acceptable for a reference card).
func wrapText(pdf *gopdf.GoPdf, text string, width float64) []string {
	var lines []string
	for _, paragraph := range strings.Split(text, "\n") {
		var current strings.Builder
		var currentW float64
		for _, r := range paragraph {
			rw, _ := pdf.MeasureTextWidth(string(r))
			if currentW+rw > width && current.Len() > 0 {
				lines = append(lines, current.String())
				current.Reset()
				currentW = 0
			}
			current.WriteRune(r)
			currentW += rw
		}
		lines = append(lines, current.String())
	}
	return lines
}

func generateManual(path, fontPath string) error {
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})

	if err := pdf.AddTTFFont("cn", fontPath); err != nil {
		return fmt.Errorf("add CJK font %s: %w", fontPath, err)
	}

	w := &pdfWriter{
		pdf:      pdf,
		fontName: "cn",
		pageW:    gopdf.PageSizeA4.W,
		pageH:    gopdf.PageSizeA4.H,
		marginL:  48,
		marginR:  48,
		marginT:  56,
		marginB:  56,
	}
	w.newPage()

	// Title block.
	w.heading("NetHack 中文速查手册", 22)
	w.pdf.SetFont(w.fontName, "", 10)
	w.pdf.SetX(w.marginL)
	w.pdf.SetY(w.y)
	_ = w.pdf.Cell(nil, "by nh-helper · 实时翻译助手自带的入门速查")
	w.y += 24

	for _, sec := range manualContent {
		w.heading(sec.title, 15)
		if sec.intro != "" {
			w.paragraph(sec.intro)
		}
		for _, it := range sec.items {
			w.item(it.key, it.desc)
		}
		for _, note := range sec.notes {
			w.paragraph(note)
		}
		w.y += 8
	}

	return pdf.WritePdf(path)
}
