package hermes

import (
	"strings"
	"testing"
)

func TestRenderManagedEnvAppendsBlockAndSortsNames(t *testing.T) {
	current := []byte("EXISTING=value\n# operator comment\n")
	rendered, err := RenderManagedEnv(current, map[string]string{
		"OPENAI_API_KEY":    "sk-openai",
		"ANTHROPIC_API_KEY": "sk-anthropic",
	})
	if err != nil {
		t.Fatalf("render managed env: %v", err)
	}
	want := "EXISTING=value\n# operator comment\n" +
		"# >>> sideplane-managed >>>\n" +
		"ANTHROPIC_API_KEY=sk-anthropic\n" +
		"OPENAI_API_KEY=sk-openai\n" +
		"# <<< sideplane-managed <<<\n"
	if string(rendered) != want {
		t.Fatalf("rendered env = %q, want %q", rendered, want)
	}
}

func TestRenderManagedEnvAppendsAfterNonNewlineInput(t *testing.T) {
	rendered, err := RenderManagedEnv([]byte("EXISTING=value"), map[string]string{"OPENAI_API_KEY": "sk-openai"})
	if err != nil {
		t.Fatalf("render managed env: %v", err)
	}
	if !strings.HasPrefix(string(rendered), "EXISTING=value\n# >>> sideplane-managed >>>\n") {
		t.Fatalf("rendered env did not insert newline before block: %q", rendered)
	}
}

func TestRenderManagedEnvReplacesExistingBlockAndPreservesRest(t *testing.T) {
	current := []byte("A=1\r\n# >>> sideplane-managed >>>\nOLD_KEY=old\n# <<< sideplane-managed <<<\nB=two # untouched\n")
	rendered, err := RenderManagedEnv(current, map[string]string{"OPENAI_API_KEY": "sk-openai"})
	if err != nil {
		t.Fatalf("render managed env: %v", err)
	}
	want := "A=1\r\n" +
		"# >>> sideplane-managed >>>\n" +
		"OPENAI_API_KEY=sk-openai\n" +
		"# <<< sideplane-managed <<<\n" +
		"B=two # untouched\n"
	if string(rendered) != want {
		t.Fatalf("rendered env = %q, want %q", rendered, want)
	}
	if strings.Contains(string(rendered), "OLD_KEY") {
		t.Fatalf("old managed env remained after replacement: %q", rendered)
	}
}

func TestRenderManagedEnvQuotesAndEscapesSpecialValues(t *testing.T) {
	rendered, err := RenderManagedEnv(nil, map[string]string{
		"BACKSLASH_KEY": `path\to"value`,
		"EMPTY_KEY":     "",
		"HASH_KEY":      "sk#value",
		"MULTILINE_KEY": "line1\nline2",
		"SPACE_KEY":     "sk with space",
		"SAFE_KEY":      "sk-safe_123:/@%+=,.",
		"TAB_KEY":       "sk\twith-tab",
	})
	if err != nil {
		t.Fatalf("render managed env: %v", err)
	}
	out := string(rendered)
	for _, want := range []string{
		`BACKSLASH_KEY="path\\to\"value"`,
		`EMPTY_KEY=""`,
		`HASH_KEY="sk#value"`,
		`MULTILINE_KEY="line1\nline2"`,
		`SPACE_KEY="sk with space"`,
		`SAFE_KEY=sk-safe_123:/@%+=,.`,
		`TAB_KEY="sk\twith-tab"`,
	} {
		if !strings.Contains(out, want+"\n") {
			t.Fatalf("rendered env missing %q:\n%s", want, out)
		}
	}
}

func TestRenderManagedEnvRejectsInvalidName(t *testing.T) {
	if _, err := RenderManagedEnv(nil, map[string]string{"1_BAD": "value"}); err == nil {
		t.Fatal("RenderManagedEnv accepted invalid env name")
	}
}
