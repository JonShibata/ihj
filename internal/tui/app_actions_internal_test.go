package tui

import "testing"

func TestBuildViewProcess_SubstitutesKey(t *testing.T) {
	cmd, err := buildViewProcess("/usr/local/bin/jiv {key} --no-comments", "PROJ-42")
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if cmd.Path == "" || cmd.Args[0] != "/usr/local/bin/jiv" {
		t.Errorf("Args[0] = %q; want \"/usr/local/bin/jiv\"", cmd.Args[0])
	}
	if cmd.Args[1] != "PROJ-42" {
		t.Errorf("Args[1] = %q; want PROJ-42 (substituted from {key})", cmd.Args[1])
	}
	if cmd.Args[2] != "--no-comments" {
		t.Errorf("Args[2] = %q; want \"--no-comments\"", cmd.Args[2])
	}
}

func TestBuildViewProcess_EmptyTemplate(t *testing.T) {
	if _, err := buildViewProcess("   ", "X-1"); err == nil {
		t.Errorf("want error for empty template")
	}
}

func TestBuildViewProcess_NoSubstitution(t *testing.T) {
	// Templates that don't reference {key} are still legal — useful for
	// commands that read the issue from the environment or stdin.
	cmd, err := buildViewProcess("less /tmp/issue.md", "PROJ-1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, a := range cmd.Args {
		if a == "PROJ-1" {
			t.Errorf("unexpected substitution: %v", cmd.Args)
		}
	}
}
