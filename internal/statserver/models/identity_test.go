package models

import "testing"

func TestNormalizeFormattingAndSemanticAliases(t *testing.T) {
	if Normalize("DeepSeek V4 Pro") != Normalize("deepseek-v4-pro") {
		t.Fatal("formatting alias did not normalize")
	}
	if Normalize("DeepSeek V4 Flash 0730") == Normalize("DeepSeek V4 Flash Latest") {
		t.Fatal("revision tokens were lost")
	}
	if NormalizeBenchmark("MMLU") == NormalizeBenchmark("MMLU Pro") {
		t.Fatal("benchmark semantic token was lost")
	}
	if got := CanonicalSlug("DeepSeek", "V4 Flash", "0730"); got != "deepseek-v4-flash-0730" {
		t.Fatalf("slug=%q", got)
	}
}
