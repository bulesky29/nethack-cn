package app

import (
	"strings"
	"testing"
)

// TestClassifyMessage locks in the heuristic that routes farlook detail
// cards to the menu window and everything else to the text window.
func TestClassifyMessage(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want string
	}{
		// Narrative — text window.
		{"action result", "You kick the kobold.  You kill the kobold!", roleText},
		{"greeting", "Hello bulesky29, the dwarven Caveman, welcome back to NetHack!", roleText},
		{"prompt", "In what direction?", roleText},
		{"farlook prompt", "Pick a monster, object or location.", roleText},
		{"trap warning", "Be careful!  New moon tonight.", roleText},

		// Short farlook noun phrase — stays in text (too short to
		// confidently classify as info card).
		{"short label", "kobold", roleText},
		{"short label 2", "open door", roleText},
		{"short label 3", "tame little dog called Slasher", roleText},

		// Single-item pickup notifications — menu window.
		{"pickup gold", "$ - 2 gold pieces.", roleMenu},
		{"pickup shield", "f - a +0 small shield.", roleMenu},
		{"pickup blessed weapon", "a - a blessed +1 quarterstaff.", roleMenu},
		{"pickup uppercase letter", "A - an uncursed luckstone.", roleMenu},
		{"pickup bag overflow letter", "# - an empty sack.", roleMenu},

		// "You pick up …" narrative form stays in text.
		{"pick up narrative", "You pick up 3 gold pieces.", roleText},

		// Long farlook detail cards — menu window.
		{"glyph card with [seen:]",
			"d        a kobold (kobold) [seen: normal vision, infravision]",
			roleMenu},
		{"glyph card without [seen:]",
			"<        a staircase up or a ladder up or a branch staircase up",
			roleMenu},
		{"glyph card with descriptor",
			"#        can be many things (corridor)",
			roleMenu},
		{"glyph card insect",
			"x        a xan or other mythical/fantastic insect (grid bug) [seen: normal vision]",
			roleMenu},
		{"glyph card fungus",
			"F        a fungus or mold (lichen)",
			roleMenu},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyMessage(tc.msg)
			if got != tc.want {
				t.Errorf("classifyMessage(%q) = %s, want %s", tc.msg, got, tc.want)
			}
		})
	}
}

// TestExtractMenuContent locks in dungeon-bleed stripping for inventory
// popups. The input is a verbatim capture of the user's `i` screen with
// the dungeon view visible through the menu overlay.
func TestExtractMenuContent(t *testing.T) {
	raw := strings.Join([]string{
		"Coins    ('$')",
		"$ - 4 gold pieces",
		"Weapons  (')')",
		"b - a +2 sling (alternate weapon; not wielded)",
		"-------             #   a - a +1 club (weapon in right hand)",
		"|...%.|          ---|-- Armor    ('[')",
		"#-.....|   ##(####..d..| e - a blessed +0 leather armor (being worn)",
		"#|.....-####      |..!.| Food  ('%')",
		"#|.>...|          |....| f - a food ration",
		"#-------          |.$..| Potions  ('!')",
		"####              -|---- h - a fizzy potion",
		"#               ##    Gems/Stones  ('*')",
		"#                #### g - a black gem",
		"#                ---. c - 18 uncursed flint stones (in quiver pouch)",
		"#                |... d - 27 uncursed rocks",
	}, "\n")

	out := extractMenuContent(raw)
	lines := strings.Split(out, "\n")

	mustHave := []string{
		"Coins    ('$')",
		"$ - 4 gold pieces",
		"Weapons  (')')",
		"b - a +2 sling (alternate weapon; not wielded)",
		"a - a +1 club (weapon in right hand)",
		"Armor    ('[')",
		"e - a blessed +0 leather armor (being worn)",
		"Food  ('%')",
		"f - a food ration",
		"Potions  ('!')",
		"h - a fizzy potion",
		"Gems/Stones  ('*')",
		"g - a black gem",
		"c - 18 uncursed flint stones (in quiver pouch)",
		"d - 27 uncursed rocks",
	}
	for _, want := range mustHave {
		if !containsLine(lines, want) {
			t.Errorf("missing line %q in cleaned output:\n%s", want, out)
		}
	}

	mustNotHave := []string{"---|--", "|...%.|", "####", "-------"}
	for _, bad := range mustNotHave {
		for _, line := range lines {
			if strings.HasPrefix(line, bad) || line == bad {
				t.Errorf("dungeon bleed %q survived: %q", bad, line)
			}
		}
	}
}

// TestClassifyPopupHandlesDungeonBleed makes sure the loosened
// menuLinePattern still flags an inventory popup as menu even when
// every menu row is prefixed with dungeon characters.
func TestClassifyPopupHandlesDungeonBleed(t *testing.T) {
	raw := strings.Join([]string{
		"-------             #   a - a +1 club",
		"|.....|          ---|-- Armor    ('[')",
		"|.....|   ##....|       e - a blessed +0 leather armor",
		"|.....|          |..!.| Potions  ('!')",
		"|.....|          |....| h - a fizzy potion",
	}, "\n")
	if got := classifyPopup(raw); got != roleMenu {
		t.Errorf("dungeon-prefixed inventory misclassified as %s", got)
	}
}

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}
