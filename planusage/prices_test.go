package planusage

import "testing"

func TestEveryCurrentModelHasAPrice(t *testing.T) {
	for _, m := range []string{
		"claude-fable-5-1", "claude-mythos-5-1", "claude-fable-5", "claude-mythos-5",
		"claude-opus-5-5", "claude-opus-5", "claude-opus-4-8", "claude-opus-4-7", "claude-opus-4-6",
		"claude-sonnet-5-5", "claude-sonnet-5", "claude-sonnet-4-6",
		"claude-haiku-5-5", "claude-haiku-4-5",
	} {
		pr, ok := PriceFor(m, nil)
		if !ok || pr.Input <= 0 || pr.Output <= 0 || pr.CacheRead <= 0 || pr.CacheWrite5m <= 0 || pr.CacheWrite1h <= 0 {
			t.Errorf("%s: %+v %v", m, pr, ok)
		}
	}
}

func TestPriceForNormalizesIDs(t *testing.T) {
	for in, want := range map[string]string{
		"claude-haiku-4-5-20251001":           "claude-haiku-4-5",
		"claude-opus-5-5[1m]":                 "claude-opus-5-5",
		"us.anthropic.claude-sonnet-4-5-v1:0": "claude-sonnet-4-5",
		"claude-opus-4-5@20251101":            "claude-opus-4-5",
		"Claude-Sonnet-5-5":                   "claude-sonnet-5-5",
	} {
		got, ok := PriceFor(in, nil)
		if !ok || got != prices[want] {
			t.Errorf("%s: %+v %v", in, got, ok)
		}
	}
	if _, ok := PriceFor("claude-x-9", nil); ok {
		t.Error("unknown model priced")
	}
}

func TestPriceOverride(t *testing.T) {
	over := map[string]Price{"claude-x-9": {Input: 2, Output: 8}, "claude-opus-5-5": {Input: 1, Output: 2, CacheRead: 0.05}}
	got, ok := PriceFor("claude-x-9-20270101", over)
	if !ok || got.CacheWrite5m != 2.5 || got.CacheWrite1h != 4 || got.CacheRead != 0.2 || got.Output != 8 {
		t.Fatalf("%+v %v", got, ok)
	}
	if got, _ := PriceFor("claude-opus-5-5", over); got.Input != 1 || got.CacheRead != 0.05 {
		t.Fatalf("%+v", got)
	}
}
