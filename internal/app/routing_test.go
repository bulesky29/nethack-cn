package app

import "testing"

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
