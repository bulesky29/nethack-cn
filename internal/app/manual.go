package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/border"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/consts/pagesize"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/core/entity"
	"github.com/johnfercher/maroto/v2/pkg/props"
)

// manualFileName is the PDF written next to the binary on first startup.
// Delete and re-launch to regenerate.
const manualFileName = "nh-helper-手册.pdf"

// cjkFontCandidates lists font files we probe for full CJK coverage.
// Probed in order; first hit wins. We prefer plain TTF (maroto's gofpdf
// backend wants pure TTF, not TTC) and fall back to TTC only as a long
// shot — see .agent/roadmap.md for the TTC extraction follow-up.
//
//	macOS:   Arial Unicode.ttf ships on every Mac since 10.5.
//	Linux:   distros usually have WenQuanYi or Noto Sans CJK installed.
//	Windows: Microsoft YaHei and SimSun ship with Windows since Vista.
var cjkFontCandidates = []string{
	// macOS
	"/Library/Fonts/Arial Unicode.ttf",
	"/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
	// Linux
	"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
	"/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc",
	"/usr/share/fonts/wqy-microhei/wqy-microhei.ttc",
	"/usr/share/fonts/wqy-zenhei/wqy-zenhei.ttc",
	"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
	"/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
	"/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc",
	// Windows
	`C:\Windows\Fonts\msyh.ttc`,
	`C:\Windows\Fonts\msyh.ttf`,
	`C:\Windows\Fonts\simsun.ttc`,
	`C:\Windows\Fonts\simhei.ttf`,
}

const fontFamily = "cn"

// Palette --------------------------------------------------------------

var (
	colInk     = &props.Color{Red: 28, Green: 28, Blue: 32}
	colMuted   = &props.Color{Red: 110, Green: 110, Blue: 115}
	colKey     = &props.Color{Red: 16, Green: 80, Blue: 180}
	colAccent  = &props.Color{Red: 200, Green: 110, Blue: 0}
	colWarn    = &props.Color{Red: 175, Green: 30, Blue: 30}
	colHeading = &props.Color{Red: 0, Green: 0, Blue: 0}
	colLight   = &props.Color{Red: 255, Green: 255, Blue: 255}

	bgHero    = &props.Color{Red: 255, Green: 244, Blue: 214}
	bgSection = &props.Color{Red: 244, Green: 246, Blue: 250}
	bgTip     = &props.Color{Red: 255, Green: 235, Blue: 235}
	bgBand    = &props.Color{Red: 32, Green: 60, Blue: 110}
)

// Content --------------------------------------------------------------

type entry struct {
	key, desc string
}

// heroEntries — the eight things a new player must internalize. Visually
// emphasized at the top of page 1. Descriptions include the English
// etymology in parentheses so the single-letter keys are easier to recall.
var heroEntries = []entry{
	{"s × n", "搜隐藏门 / 陷阱 (search)：原地多按几次，走廊里没事多按"},
	{";", "远观 (farlook)：指地图任意位置查它是什么。走之前先看"},
	{"#pray", "向神祈祷 (pray)。HP 危急时新角色能保底救一次"},
	{"i", "查背包 (inventory)"},
	{",", "捡脚下东西 (pickup)"},
	{"r  q  e", "读卷轴 (read) / 喝药水 (quaff) / 吃东西 (eat)"},
	{"<  >", "上 / 下楼梯（站在 < 或 > 上）"},
	{"S", "存档退出 (save)，下次启动继续"},
}

var combatEntries = []entry{
	{"F + 方向", "强行攻击该方向 (fight)；打隐形怪"},
	{"t", "投掷物品 (throw)：选物 + 方向"},
	{"z", "使用法杖 (zap)：选杖 + 方向"},
	{"Z", "施法 (cast)：咒语在 + 菜单学"},
	{"^d", "踢 (kick)"},
}

var gearEntries = []entry{
	{"W / T", "穿 / 脱盔甲 (wear / take off)"},
	{"w", "装备武器 (wield)；- 切回徒手"},
	{"P / R", "戴 / 摘戒指护符 (put on / remove)"},
	{"d / D", "丢一个 / 按类型批量丢 (drop)"},
	{"a", "使用工具 (apply)：绳钩、口哨等"},
}

var exploreEntries = []entry{
	{"o / c", "开 (open) / 关 (close) 门"},
	{":", "看脚下 (look here)：同 . 但更明确"},
	{"\\", "已发现物品 / 怪物列表"},
	{"^x", "查看自己的属性面板 (extended status)"},
	{"?", "游戏内帮助菜单 (help)"},
}

var extEntries = []entry{
	{"#pray", "祈祷救命（HP < 1/7、中毒、饿死时）"},
	{"#offer", "在祭坛 _ 上献祭怪物尸体"},
	{"#enhance", "升级武器熟练度（每次 Xp 升级后看一眼）"},
	{"#chat", "与 NPC 交谈"},
	{"#loot", "翻开脚下的箱子 / 容器"},
	{"#dip", "蘸药水 / 水"},
	{"#force", "用武器砸锁（武器可能损坏）"},
	{"#invoke", "唤起神器特殊能力"},
	{"#ride", "骑乘被驯服的座骑"},
	{"#name", "给物品 / 怪物起名（标记用）"},
	{"#quit", "放弃当前角色（不可挽回）"},
}

var symbolEntries = []entry{
	{"@", "你自己（或人类 NPC）"},
	{"a-z A-Z", "怪物（k 狗头人, F 真菌, d 狗 ...）"},
	{".", "地面"},
	{"#", "走廊"},
	{"| - +", "墙壁 / 门"},
	{"< >", "上 / 下楼梯"},
	{"_  {  }", "祭坛 / 喷泉 / 池水"},
	{"$", "金币"},
	{") (  [", "武器 / 武器堆 / 盔甲"},
	{"=  \"", "戒指 / 护符"},
	{"!  ?  /  +", "药水 / 卷轴 / 法杖 / 法术书"},
	{"*  %  ^", "宝石 / 食物 / 陷阱"},
}

// survivalTips — top 10, in priority order. First three carry warn colour.
var survivalTips = []string{
	"HP 低于最大值 1/7 立刻 #pray；新角色至少能救一次",
	"永远不要徒手戴未鉴定戒指 / 护符。扔到祭坛 _ 上、或等鉴定卷轴",
	"看到陷阱标记 ^ 永远绕开走；反复经过迟早出事",
	"商店货想买就放回原位、走到门口、老板自动结账。直接拿走老板会变 angry shopkeeper（极强）",
	"跨过门 / 拐角前用 ; 远观对面有没有怪",
	"背包常备 2-3 份食物。饥饿状态战斗力大降，濒死直接 #pray",
	"每升一级用 #enhance 看武器熟练度能不能 unlock",
	"祭坛阵营要和你一致；献祭异己阵营尸体会激怒你的神",
	"喷泉 / 王座 / 水槽新手期慎用 —— 风险 > 收益",
	"进新楼前 S 存档；万一被秒杀还能从同一档继续摸索",
}

// nhHelperHints — short summary of how to read the translation window.
var nhHelperHints = []entry{
	{"顶部状态面板", "4 行固定面板：标题 / HP·Pw 进度条 / 六维属性 / 状态徽章。HP 按比例自动变色"},
	{"下方翻译流", "时间戳分割；源文 dim 灰、译文正常色"},
	{"全屏弹窗", "整段抓取送翻，菜单 / 剧情 / 商店列表都按结构翻"},
	{"翻译缓存", "重复内容只翻一次（SQLite 持久化），命中瞬出零费用"},
	{"自动学词", "长文本翻完后异步抽 NetHack 术语进 glossary；人工词条永不被覆盖"},
	{"-debug 标志", "启动加 -debug 写 log/nh-helper.{raw,translate}.log"},
}

// Generation -----------------------------------------------------------

func EnsureManual() (string, bool, error) {
	dir, err := dataDir()
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

func generateManual(path, fontPath string) error {
	// maroto's File: registration looks for a JSON metric sidecar that
	// regular TTFs don't ship with; passing the raw bytes bypasses that
	// and lets the underlying gofpdf parse the TTF directly.
	fontBytes, err := os.ReadFile(fontPath)
	if err != nil {
		return fmt.Errorf("read font %s: %w", fontPath, err)
	}

	cfg := config.NewBuilder().
		WithPageSize(pagesize.A4).
		WithLeftMargin(14).
		WithRightMargin(14).
		WithTopMargin(14).
		WithBottomMargin(14).
		WithCustomFonts([]*entity.CustomFont{
			// Register the same TTF under both Normal and Bold so any
			// fontstyle.Bold prop falls back gracefully — Arial Unicode
			// only ships one weight; bold renders as same glyph but we
			// keep the layout consistent.
			{Family: fontFamily, Style: fontstyle.Normal, Bytes: fontBytes},
			{Family: fontFamily, Style: fontstyle.Bold, Bytes: fontBytes},
		}).
		WithDefaultFont(&props.Font{Family: fontFamily, Size: 9, Color: colInk}).
		WithTitle("NetHack 中文速查手册", true).
		WithAuthor("nh-helper", true).
		Build()

	m := maroto.New(cfg)

	addTitle(m)
	addHeroSection(m, "第一天必学", heroEntries)
	addSymbolGrid(m, "地图符号速查", symbolEntries)
	addTwoColSection(m, "战斗", combatEntries, "装备 / 物品", gearEntries)
	addSection(m, "探索 / 互动", exploreEntries, bgSection)
	addSection(m, "进阶 # 命令", extEntries, bgSection)
	addTipsSection(m, "新手存活 Top 10", survivalTips)
	addSection(m, "nh-helper 客户端使用", nhHelperHints, bgSection)
	addFooter(m)

	doc, err := m.Generate()
	if err != nil {
		return fmt.Errorf("maroto generate: %w", err)
	}
	return doc.Save(path)
}

// Layout helpers -------------------------------------------------------

func addTitle(m core.Maroto) {
	m.AddRow(14,
		col.New(12).
			WithStyle(&props.Cell{BackgroundColor: bgBand}).
			Add(text.New("NetHack 中文速查手册", props.Text{
				Family: fontFamily, Size: 18, Align: align.Center,
				Style: fontstyle.Bold, Color: colLight, Top: 3.5,
			})),
	)
	m.AddRow(6,
		text.NewCol(12, "by nh-helper · 实时翻译助手自带的入门速查", props.Text{
			Family: fontFamily, Size: 8, Align: align.Center, Color: colMuted, Top: 1.5,
		}),
	)
}

// addHeroSection — single column with each entry on its own coloured
// bar, key in accent colour, slightly larger font.
func addHeroSection(m core.Maroto, title string, entries []entry) {
	addHeading(m, title, colAccent)
	for _, e := range entries {
		m.AddRow(8,
			col.New(12).
				WithStyle(&props.Cell{BackgroundColor: bgHero, BorderColor: colMuted, BorderType: border.Bottom, BorderThickness: 0.1}).
				Add(
					text.New(e.key, props.Text{
						Family: fontFamily, Size: 11, Color: colKey, Left: 4, Top: 1.4,
						Style: fontstyle.Bold,
					}),
					text.New(e.desc, props.Text{
						Family: fontFamily, Size: 9, Color: colInk, Left: 60, Top: 2,
					}),
				),
		)
	}
	m.AddRow(3)
}

// addSection — coloured section heading + a list of key/desc rows in a
// single column. Uses a 4 / 8 grid split internally.
func addSection(m core.Maroto, title string, entries []entry, bg *props.Color) {
	addHeading(m, title, colHeading)
	for i, e := range entries {
		cellBG := colLight
		if i%2 == 0 {
			cellBG = bg
		}
		m.AddRow(6.4,
			col.New(4).
				WithStyle(&props.Cell{BackgroundColor: cellBG}).
				Add(text.New(e.key, props.Text{
					Family: fontFamily, Size: 9.5, Color: colKey, Left: 3, Top: 1.4,
					Style: fontstyle.Bold,
				})),
			col.New(8).
				WithStyle(&props.Cell{BackgroundColor: cellBG}).
				Add(text.New(e.desc, props.Text{
					Family: fontFamily, Size: 9, Color: colInk, Left: 2, Top: 1.6,
				})),
		)
	}
	m.AddRow(3)
}

// addTwoColSection — two side-by-side sections on the same horizontal
// band, each with its own heading. Lets short topics pack 2-per-row.
func addTwoColSection(m core.Maroto, leftTitle string, leftEntries []entry,
	rightTitle string, rightEntries []entry) {

	headerProps := props.Text{
		Family: fontFamily, Size: 11.5, Color: colHeading, Left: 3, Top: 2,
		Style: fontstyle.Bold,
	}
	m.AddRow(7,
		col.New(6).
			WithStyle(&props.Cell{BackgroundColor: bgSection, BorderColor: colAccent,
				BorderType: border.Left, BorderThickness: 1.0}).
			Add(text.New(leftTitle, headerProps)),
		col.New(6).
			WithStyle(&props.Cell{BackgroundColor: bgSection, BorderColor: colAccent,
				BorderType: border.Left, BorderThickness: 1.0}).
			Add(text.New(rightTitle, headerProps)),
	)

	rows := len(leftEntries)
	if len(rightEntries) > rows {
		rows = len(rightEntries)
	}
	for i := 0; i < rows; i++ {
		m.AddRow(6.4,
			twoColEntryCol(leftEntries, i),
			twoColEntryCol(rightEntries, i),
		)
	}
	m.AddRow(3)
}

// twoColEntryCol packs a single (key, desc) into a 6-wide outer column,
// internally splitting into a 2/4 sub-grid for key vs description.
func twoColEntryCol(entries []entry, i int) core.Col {
	if i >= len(entries) {
		return col.New(6)
	}
	e := entries[i]
	outer := col.New(6)
	// Maroto doesn't nest rows inside cols, so we draw the key + desc
	// as two stacked text components in the same column with manual
	// left offset for the description.
	outer.Add(
		text.New(e.key, props.Text{
			Family: fontFamily, Size: 9.5, Color: colKey, Left: 3, Top: 1.4,
			Style: fontstyle.Bold,
		}),
		text.New(e.desc, props.Text{
			Family: fontFamily, Size: 9, Color: colInk, Left: 38, Top: 1.4,
		}),
	)
	return outer
}

// addSymbolGrid — denser 3-column legend.
func addSymbolGrid(m core.Maroto, title string, entries []entry) {
	addHeading(m, title, colHeading)
	for i := 0; i < len(entries); i += 3 {
		var cols []core.Col
		for j := 0; j < 3; j++ {
			if i+j < len(entries) {
				e := entries[i+j]
				cols = append(cols, col.New(4).
					WithStyle(&props.Cell{BackgroundColor: bgSection}).
					Add(
						text.New(e.key, props.Text{
							Family: fontFamily, Size: 10, Color: colKey, Left: 3, Top: 1.4,
							Style: fontstyle.Bold,
						}),
						text.New(e.desc, props.Text{
							Family: fontFamily, Size: 8.5, Color: colInk, Left: 28, Top: 1.6,
						}),
					))
			} else {
				cols = append(cols, col.New(4))
			}
		}
		m.AddRow(6.4, cols...)
	}
	m.AddRow(3)
}

// addTipsSection — numbered top-10 tips, first three highlighted red.
func addTipsSection(m core.Maroto, title string, tips []string) {
	addHeading(m, title, colWarn)
	for i, tip := range tips {
		numCol := colKey
		bg := bgSection
		if i < 3 {
			numCol = colWarn
			bg = bgTip
		}
		m.AddRow(8,
			col.New(1).
				WithStyle(&props.Cell{BackgroundColor: bg}).
				Add(text.New(fmt.Sprintf("%02d", i+1), props.Text{
					Family: fontFamily, Size: 11, Color: numCol, Align: align.Center, Top: 2.2,
					Style: fontstyle.Bold,
				})),
			col.New(11).
				WithStyle(&props.Cell{BackgroundColor: bg}).
				Add(text.New(tip, props.Text{
					Family: fontFamily, Size: 9.2, Color: colInk, Left: 3, Top: 2.3,
				})),
		)
	}
	m.AddRow(3)
}

// addHeading — full-width band with text and an accent underline.
func addHeading(m core.Maroto, label string, c *props.Color) {
	m.AddRow(8,
		col.New(12).
			WithStyle(&props.Cell{
				BorderColor:     c,
				BorderType:      border.Bottom,
				BorderThickness: 0.6,
			}).
			Add(text.New(label, props.Text{
				Family: fontFamily, Size: 13, Color: c, Left: 1, Top: 2.5,
				Style: fontstyle.Bold,
			})),
	)
	m.AddRow(2)
}

func addFooter(m core.Maroto) {
	m.AddRow(6, text.NewCol(12,
		"完整命令请在游戏里按 ? 查看，或访问 https://nethackwiki.com/  ·  本手册由 nh-helper 自动生成，删除 PDF 后重启即可刷新",
		props.Text{Family: fontFamily, Size: 7.5, Align: align.Center, Color: colMuted, Top: 1.5},
	))
}
