package abuse

import "testing"

func TestInjectedEvaluationReproducibleAndBounded(t *testing.T) {
	first, err := EvaluateInjected(47211)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EvaluateInjected(47211)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("nondeterministic: %+v %+v", first, second)
	}
	if first.TruePositive+first.FalseNegative != first.InjectedBots {
		t.Fatalf("bad bot accounting: %+v", first)
	}
	if first.Precision < 0 || first.Precision > 1 || first.Recall < 0 || first.Recall > 1 {
		t.Fatalf("invalid metrics: %+v", first)
	}
}
