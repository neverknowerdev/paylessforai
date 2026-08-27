package statserver

import "testing"

func TestNormalizeFormattingAliases(t *testing.T) {
	for _, pair := range [][2]string{{"DeepSeek V4 Pro", "deepseek-v4-pro"}, {"DeepSeek V4 Flash 0730", "deepseek_v4_flash_0730"}, {"MMLU Pro", "mmlu-pro"}} {
		if Normalize(pair[0]) != Normalize(pair[1]) {
			t.Fatalf("%q and %q did not normalize equally", pair[0], pair[1])
		}
	}
}

func TestNormalizePreservesSemanticTokens(t *testing.T) {
	a, b := Normalize("DeepSeek V4 Flash 0730"), Normalize("DeepSeek V4 Flash Latest")
	if a == b {
		t.Fatalf("revision/latest aliases must not be permanent equality")
	}
	if NormalizeBenchmark("MMLU") == NormalizeBenchmark("MMLU Pro") {
		t.Fatalf("MMLU and MMLU Pro must remain distinct")
	}
}

func TestCanonicalSlugStable(t *testing.T) {
	if got := canonicalSlug("DeepSeek", "V4 Flash", "0730"); got != "deepseek-v4-flash-0730" {
		t.Fatalf("got %q", got)
	}
}
